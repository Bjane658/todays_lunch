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

	"github.com/gocolly/colly/v2"
)

type Options struct {
	CustomDate string
	MenuUrl    string
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
	weekday := map[string]string{
		"Monday":    "Montag",
		"Tuesday":   "Dienstag",
		"Wednesday": "Mittwoch",
		"Thursday":  "Donnerstag",
		"Friday":    "Freitag",
		"Saturday":  "Samstag",
		"Sunday":    "Sonntag",
	}[today.Weekday().String()]
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

	todayStr := fmt.Sprintf("%s, %d. %s", weekday, today.Day(), month)
	if options.CustomDate != "" {
		todayStr = options.CustomDate
	}
	fmt.Println("Checking menu for %s", todayStr)

	c.OnHTML("div.divider", func(e *colly.HTMLElement) {
		days := strings.Split(e.Text, "–")
		for _, day := range days {
			day = strings.TrimSpace(day)
			if day == "" {
				continue
			}
			//todayStr = "Montag, 14. Juli"

			if strings.Contains(day, todayStr) {
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

func sendToSlack(message, webhookURL string) error {
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

func main() {
	var opts Options

	opts.MenuUrl = os.Getenv("MENU_URL")
	webhookURL := os.Getenv("SLACK_WEBHOOK_URL")

	if opts.MenuUrl == "" || webhookURL == "" {
		log.Fatal("Missing required environment variables")
	}

	if len(os.Args) > 1 {
		opts.CustomDate = os.Args[1]
	}

	todaysLunch, err := getTodaysLunch(opts)
	if err != nil {
		log.Fatalf("Failed to fetch menu: %v", err)
	}

	err = sendToSlack(todaysLunch, webhookURL)
	if err != nil {
		log.Fatalf("Failed to send Slack message: %v", err)
	}
	fmt.Println("Successfully sent menu to Slack.")
}
