package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/gocolly/colly/v2"
	"log"
	"net/http"
	"os"
	"time"
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

type ImageGenerationRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	N      int    `json:"n"`
	Size   string `json:"size"`
}

type ImageGenerationResponse struct {
	Data []struct {
		URL string `json:"url"`
	} `json:"data"`
}

func identifyTodaysLunch(options Options, openaiToken string) (string, error) {
	c := colly.NewCollector()
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

	todaysDate := fmt.Sprintf("%s, %d. %s", weekdayTranslated, today.Day(), month)
	fmt.Println("Checking menu for ", todaysDate)
	todayStr := fmt.Sprintf("%s, %d. %s", weekdayTranslated, today.Day(), month)
	if options.CustomDate != "" {
		todayStr = options.CustomDate
	}
	completeMenu := ""
	fmt.Println("Checking menu for ", todayStr)
	c.OnHTML("section.text", func(e *colly.HTMLElement) {
		fmt.Println(e.Text)
		completeMenu += e.Text
	})

	err := c.Visit(options.MenuUrl)
	if err != nil {
		return "", err
	}

	chatMessage := []ChatMessage{{Role: "user", Content: fmt.Sprintf("Was gibt es heute Mittag am %s zu essen?. Bitte antworte nur mit dem gefundenen Mittagsessen.\n%s", todaysDate, completeMenu)}}
	todaysLunch, err := createChatCompletion(openaiToken, "gpt-5", chatMessage)
	if err != nil {
		fmt.Println("Error creating lunch description:", err)
		return "", err
	}
	fmt.Println("TodaysLunch", todaysLunch)

	return todaysLunch, nil
}

func sendToSlackWithDescription(message, slackToken, openaiToken string) error {
	// Generate image first
	/*
		imageURL, err := generateMealImage(openaiToken, message)
		if err != nil {
			fmt.Println("Warning: Could not generate image:", err)
			// Continue without image
			imageURL = ""
		}
	*/

	// Create blocks for better formatting
	blocks := []map[string]interface{}{
		{
			"type": "section",
			"text": map[string]string{
				"type": "mrkdwn",
				"text": fmt.Sprintf("%s", message),
			},
		},
	}

	/*
		// Add image block if generation was successful
		if imageURL != "" {
			blocks = append(blocks, map[string]interface{}{
				"type":      "image",
				"image_url": imageURL,
				"alt_text":  message,
			})
		}
	*/

	payload := map[string]interface{}{
		"channel": "#heute-mittag",
		"text":    message,
		"blocks":  blocks,
	}
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
	payload = map[string]interface{}{"channel": "#heute-mittag", "text": lunchDescription, "thread_ts": ts}
	body, _ = json.Marshal(payload)

	req, err = http.NewRequest("POST", "https://slack.com/api/chat.postMessage", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+slackToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err = client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
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

func generateMealImage(token, mealName string) (string, error) {
	prompt := fmt.Sprintf("Professional food photography of %s, appetizing, well-lit, restaurant quality, top view, natural lighting, ultra realistic", mealName)
	reqBody := ImageGenerationRequest{
		Model:  "dall-e-3",
		Prompt: prompt,
		N:      1,
		Size:   "1024x1024",
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", "https://api.openai.com/v1/images/generations", bytes.NewBuffer(bodyBytes))
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

	var result ImageGenerationResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if len(result.Data) == 0 {
		return "", fmt.Errorf("no image generated")
	}

	return result.Data[0].URL, nil
}

func main() {
	var opts Options

	opts.MenuUrl = os.Getenv("MENU_URL")
	slackToken := os.Getenv("SLACK_TOKEN")
	openAiToken := os.Getenv("OPENAI_TOKEN")

	if opts.MenuUrl == "" || openAiToken == "" || slackToken == "" {
		log.Fatal("Missing required environment variables")
	}

	if len(os.Args) > 1 {
		opts.CustomDate = os.Args[1]
	}

	todaysLunch, err := identifyTodaysLunch(opts, openAiToken)
	if err != nil {
		return
	}
	err = sendToSlackWithDescription(todaysLunch, slackToken, openAiToken)
	if err != nil {
		log.Fatalf("Failed to send Slack message: %v", err)
	}
	fmt.Println("Successfully sent menu to Slack.")
}
