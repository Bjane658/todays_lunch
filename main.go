package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/gocolly/colly/v2"
)

type Options struct {
	CustomDate string
	MenuUrl    string
}

type ChatCompletionRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatCompletionResponse struct {
	Choices []struct {
		Message ChatMessage `json:"message"`
	} `json:"choices"`
}

type openAIImageResp struct {
	Data []struct {
		B64JSON string `json:"b64_json"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type slackUploadV2Resp struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	File  *struct {
		ID string `json:"id"`
	} `json:"file,omitempty"`
	// Some workspaces return files as an array:
	Files []struct {
		ID string `json:"id"`
	} `json:"files,omitempty"`
}

// Minimal response from files.getUploadURLExternal
type getUploadURLResp struct {
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
	UploadURL string `json:"upload_url"`
	FileID    string `json:"file_id"`
}

// Minimal response from files.completeUploadExternal
type completeUploadResp struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Files []struct {
		ID string `json:"id"`
	} `json:"files"`
}

func extractMenuSection(s string) string {
	start := strings.Index(s, "Mittag")
	end := strings.Index(s, "Dessert")

	if start == -1 || end == -1 || end <= start {
		return "" // Not found or invalid order
	}

	// Slice from after "Mittag" to just before "Dessert"
	return strings.TrimSpace(s[start+len("Mittag") : end])
}

func getTodaysLunch(options Options) (string, error) {
	c := colly.NewCollector()
	lunch := ""

	// Build today's date string in German format, e.g., "Donnerstag, 20. Juli"
	today := time.Now()
	weekday := today.Weekday().String()
	weekdayTranslated := map[string]string{
		"Monday":    "Montag",
		"Tuesday":   "Dienstag",
		"Wednesday": "Mittwoch",
		"Thursday":  "Donnerstag",
		"Friday":    "Freitag",
		"Saturday":  "Samstag",
		"Sunday":    "Sonntag",
	}[weekday]
	month := map[time.Month]string{
		time.January:   "Januar",
		time.February:  "Februar",
		time.March:     "März",
		time.April:     "April",
		time.May:       "Mai",
		time.June:      "Juni",
		time.July:      "Juli",
		time.August:    "August",
		time.September: "September",
		time.October:   "Oktober",
		time.November:  "November",
		time.December:  "Dezember",
	}[today.Month()]

	todayStr := fmt.Sprintf("%s, %d. %s", weekdayTranslated, today.Day(), month)
	todayDay := fmt.Sprintf("%d", today.Day())
	if options.CustomDate != "" {
		todayStr = options.CustomDate
	}
	fmt.Println("Checking menu for ", todayStr)

	c.OnHTML("div.block div.divider", func(e *colly.HTMLElement) {
		days := strings.Split(e.Text, "–")
		for _, day := range days {
			day = strings.TrimSpace(day)
			if day == "" {
				continue
			}
			strippedDay := removeAllWhitespace(day)

			fmt.Printf("")
			if (strings.Contains(strippedDay, weekday) || strings.Contains(strippedDay, weekdayTranslated)) && strings.Contains(strippedDay, todayDay) && strings.Contains(strippedDay, month) {
				lunch = extractMenuSection(day)
				fmt.Printf("Today: %s, on the menu: %s\n", todayStr, lunch)
			}

		}
	})

	err := c.Visit(options.MenuUrl)
	if err != nil {
		return "", err
	}

	if lunch == "" {
		return "", fmt.Errorf("lunch for today not found")
	}

	fmt.Println("lunch:", lunch)

	return lunch, nil
}

func sendToSlackWithDescription(message, slackToken, openaiToken, channelId string) error {
	payload := map[string]interface{}{"channel": channelId, "text": message}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", "https://slack.com/api/chat.postMessage", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+slackToken)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var postResp map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&postResp)
	fmt.Println("Send response:", postResp)

	chatMessage := []ChatMessage{{Role: "user", Content: fmt.Sprintf("Heute Mittag gibt es %s zu essen. Ich kann mir leider nichts darunter vorstellen. Bitte beschreibe mir dieses Gericht. Bitte verzichte auf höflichkeitsformen in deiner Antwort wie z.B. Gerne!", message)}}
	lunchDescription, err := createChatCompletion(openaiToken, "gpt-4.1", chatMessage)
	if err != nil {
		fmt.Println("Error creating lunch description:", err)
		return err
	}

	// Extract the ts
	ts := postResp["ts"].(string)
	fmt.Printf("Ts: %s\n", ts)
	payload = map[string]interface{}{"channel": channelId, "text": lunchDescription, "thread_ts": ts}
	body, _ = json.Marshal(payload)

	req, err = http.NewRequest("POST", "https://slack.com/api/chat.postMessage", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+slackToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err = client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = generateLunchImageAndPostToSlack(openaiToken, slackToken, channelId, ts, fmt.Sprintf("Generiere ein Bild von $s", message))
	if err != nil {
		return err
	}

	return nil
}

func pushToSlack(message, webhookURL string) error {
	payload := map[string]string{"text": message}
	body, _ := json.Marshal(payload)

	resp, err := http.Post(webhookURL, "application/json", bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("Slack webhook failed with status: %s", resp.Status)
	}
	return nil
}

func removeAllWhitespace(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

func createChatCompletion(token, model string, messages []ChatMessage) (string, error) {
	reqBody := ChatCompletionRequest{
		Model:    model,
		Messages: messages,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", "https://api.openai.com/v1/chat/completions", bytes.NewBuffer(bodyBytes))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result ChatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}

	return result.Choices[0].Message.Content, nil
}

func generateLunchImageAndPostToSlack(openAIKey, slackToken, channelID, threadTS, prompt string) (string, error) {
	httpClient := &http.Client{Timeout: 30 * time.Second}

	// 1) Generate image via OpenAI Images API
	imgBytes, err := generateImage(openAIKey, prompt, httpClient)
	if err != nil {
		return "", fmt.Errorf("openai image gen: %w", err)
	}

	fmt.Printf("Gerated image")

	// 2) Upload image to Slack thread
	fileID, err := uploadFileToSlack(slackToken, channelID, threadTS, "todays-lunch.png", imgBytes)
	if err != nil {
		return "", fmt.Errorf("slack upload: %w", err)
	}
	fmt.Printf("Uploaded file to slack")

	return fileID, nil
}

func generateImage(openAIKey, prompt string, httpClient *http.Client) ([]byte, error) {
	payload := map[string]any{
		"model":           "dall-e-3",
		"prompt":          prompt,
		"n":               1,
		"size":            "1024x1024",
		"response_format": "b64_json",
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", "https://api.openai.com/v1/images/generations", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+openAIKey)
	req.Header.Set("Content-Type", "application/json")

	res, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	var out openAIImageResp
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out.Error != nil {
		return nil, fmt.Errorf("openai error: %s", out.Error.Message)
	}
	if len(out.Data) == 0 || out.Data[0].B64JSON == "" {
		return nil, fmt.Errorf("no image returned")
	}

	raw, err := base64.StdEncoding.DecodeString(out.Data[0].B64JSON)
	if err != nil {
		return nil, fmt.Errorf("decode b64: %w", err)
	}
	return raw, nil
}

func uploadImageToSlack(slackToken, channelID, threadTS, filename string, img []byte, httpClient *http.Client) (string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	// files.uploadV2 expects the binary under "file"
	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		return "", err
	}
	if _, err := fw.Write(img); err != nil {
		return "", err
	}

	// Required/optional fields
	_ = w.WriteField("channel_id", channelID)        // destination channel
	_ = w.WriteField("thread_ts", threadTS)          // reply inside thread
	_ = w.WriteField("filename", filename)           // display filename
	_ = w.WriteField("title", "Today’s lunch image") // nice title
	// _ = w.WriteField("initial_comment", "Heute Mittag: Bò Kho 🍜") // optional

	if err := w.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", "https://slack.com/api/files.uploadV2", bytes.NewReader(buf.Bytes()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+slackToken)
	req.Header.Set("Content-Type", w.FormDataContentType())

	res, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	respBytes, err := io.ReadAll(res.Body)
	if err != nil {
		return "", err
	}

	var up slackUploadV2Resp
	if err := json.Unmarshal(respBytes, &up); err != nil {
		return "", fmt.Errorf("unmarshal slack: %w (body: %s)", err, string(respBytes))
	}
	if !up.OK {
		return "", fmt.Errorf("slack error: %s (body: %s)", up.Error, string(respBytes))
	}

	// Prefer single file, fall back to array
	if up.File != nil && up.File.ID != "" {
		return up.File.ID, nil
	}
	if len(up.Files) > 0 && up.Files[0].ID != "" {
		return up.Files[0].ID, nil
	}
	return "", fmt.Errorf("upload ok but no file id in response (body: %s)", string(respBytes))
}

func uploadFileToSlack(slackToken, channelID, threadTS, filename string, data []byte) (string, error) {
	if len(data) == 0 {
		return "", errors.New("no data provided")
	}

	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	client := &http.Client{Transport: tr, Timeout: 60 * time.Second}

	// 1) files.getUploadURLExternal (application/x-www-form-urlencoded)
	form := url.Values{}
	form.Set("filename", filename)
	form.Set("length", fmt.Sprintf("%d", len(data)))
	// Optional:
	// form.Set("alt_text", "Today’s lunch")
	// form.Set("mime_type", "image/png")

	req1, err := http.NewRequest("POST", "https://slack.com/api/files.getUploadURLExternal", bytes.NewBufferString(form.Encode()))
	if err != nil {
		return "", err
	}
	req1.Header.Set("Authorization", "Bearer "+slackToken)
	req1.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	res1, err := client.Do(req1)
	if err != nil {
		return "", err
	}
	defer res1.Body.Close()

	var up getUploadURLResp
	if err := json.NewDecoder(res1.Body).Decode(&up); err != nil {
		return "", fmt.Errorf("decode getUploadURLExternal: %w")
	}
	if !up.OK {
		return "", fmt.Errorf("slack getUploadURLExternal error: %s", up.Error)
	}
	if up.UploadURL == "" || up.FileID == "" {
		return "", errors.New("missing upload_url or file_id from Slack")
	}
	fmt.Printf("got upload url: %s\n", up.UploadURL)

	// 2) PUT binary to presigned URL (retry on transient failures)
	putAttempt := func() error {
		rd := bytes.NewReader(data)
		putReq, err := http.NewRequest("PUT", up.UploadURL, rd)
		if err != nil {
			return err
		}
		putReq.Header.Set("Content-Type", "application/octet-stream")
		putReq.ContentLength = int64(len(data))

		putRes, err := client.Do(putReq)
		if err != nil {
			return err
		}
		defer putRes.Body.Close()
		_, _ = io.Copy(io.Discard, putRes.Body)

		if putRes.StatusCode < 200 || putRes.StatusCode >= 300 {
			return fmt.Errorf("upload PUT failed: %s", putRes.Status)
		}
		return nil
	}
	var lastErr error
	for i := 0; i < 3; i++ {
		if err := putAttempt(); err != nil {
			lastErr = err
			select {
			case <-time.After(time.Duration(i+1) * 500 * time.Millisecond):
				continue
			}
			lastErr = nil
			break
		}
		if lastErr != nil {
			return "", lastErr
		}

		// 3) files.completeUploadExternal (application/json)
		compReqBody := map[string]any{
			"files": []map[string]any{
				{"id": up.FileID, "title": filename},
			},
			"channel_id": channelID,
			//"thread_ts":  threadTS,
		}
		cb, _ := json.Marshal(compReqBody)

		fmt.Printf("Sending request with body: %+v", compReqBody)
		req3, err := http.NewRequest("POST", "https://slack.com/api/files.completeUploadExternal", bytes.NewReader(cb))
		if err != nil {
			return "", err
		}
		req3.Header.Set("Authorization", "Bearer "+slackToken)
		req3.Header.Set("Content-Type", "application/json")

		res3, err := client.Do(req3)
		if err != nil {
			return "", err
		}
		defer res3.Body.Close()

		var comp completeUploadResp
		if err := json.NewDecoder(res3.Body).Decode(&comp); err != nil {
			return "", fmt.Errorf("decode completeUploadExternal: %w")
		}
		if !comp.OK {
			return "", fmt.Errorf("slack completeUploadExternal error: %s", comp.Error)
		}
		if len(comp.Files) == 0 || comp.Files[0].ID == "" {
			return "", errors.New("upload completed but no file id returned")
		}
		fmt.Printf("Got response: %+v", comp)
		return comp.Files[0].ID, nil

	}
	return "", nil
}

func main() {
	var opts Options

	opts.MenuUrl = os.Getenv("MENU_URL")
	webhookURL := os.Getenv("SLACK_WEBHOOK_URL")
	slackToken := os.Getenv("SLACK_TOKEN")
	openAiToken := os.Getenv("OPENAI_TOKEN")

	if opts.MenuUrl == "" || webhookURL == "" || openAiToken == "" || slackToken == "" {
		log.Fatal("Missing required environment variables")
	}

	if len(os.Args) > 1 {
		opts.CustomDate = os.Args[1]
	}

	todaysLunch, err := getTodaysLunch(opts)
	if err != nil {
		log.Printf("Today there seems to be no lunch: %v", err)
		return
	}

	log.Printf("Today lunch: %s", todaysLunch)

	//err = pushToSlack(todaysLunch, webhookURL)
	err = sendToSlackWithDescription(todaysLunch, slackToken, openAiToken, "C09CCHSJ98C")
	if err != nil {
		log.Fatalf("Failed to send Slack message: %v", err)
	}
	fmt.Println("Successfully sent menu to Slack.")

}
