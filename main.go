package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
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
	//todayDay := fmt.Sprintf("%d", today.Day())
	if options.CustomDate != "" {
		todayStr = options.CustomDate
	}
	fmt.Println("Checking menu for ", todayStr)
	c.OnHTML("div.divider", func(e *colly.HTMLElement) {
		fmt.Println(e.Text)
	})

	/*
		c.OnHTML("div.container div.divider", func(e *colly.HTMLElement) {
			days := strings.Split(e.Text, "–")
			for _, day := range days {
				day = strings.TrimSpace(day)
				if day == "" {
					continue
				}
				strippedDay := removeAllWhitespace(day)

				fmt.Printf("strippedDay: %s\n", strippedDay)
				if (strings.Contains(strippedDay, weekday) || strings.Contains(strippedDay, weekdayTranslated)) && strings.Contains(strippedDay, todayDay) && strings.Contains(strippedDay, month) {
					lunch = extractMenuSection(day)
					fmt.Printf("Today: %s, on the menu: %s\n", todayStr, lunch)
				}

			}
		})
	*/

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
	//var todaysDate = "14. Oktober 2025"
	//todayDay := fmt.Sprintf("%d", today.Day())
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
	payload := map[string]interface{}{"channel": "#heute-mittag", "text": message}
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

	todaysLunch, err := identifyTodaysLunch(opts, openAiToken)
	if err != nil {
		return
	}
	err = sendToSlackWithDescription(todaysLunch, slackToken, openAiToken)
	if err != nil {
		log.Fatalf("Failed to send Slack message: %v", err)
	}
	fmt.Println("Successfully sent menu to Slack.")

	/*	todaysLunch, err := getTodaysLunch(opts)
		if err != nil {
			log.Printf("Today there seems to be no lunch: %v", err)
			return
		}

		log.Printf("Today lunch: %s", todaysLunch)

		//err = pushToSlack(todaysLunch, webhookURL)
		err = sendToSlackWithDescription(todaysLunch, slackToken, openAiToken)
		if err != nil {
			log.Fatalf("Failed to send Slack message: %v", err)
		}
		fmt.Println("Successfully sent menu to Slack.")
	*/
}
