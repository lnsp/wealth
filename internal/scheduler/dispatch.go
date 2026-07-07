package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/smtp"
	"net/url"
	"strings"
	"time"
)

// channelConfig holds parsed channel configuration.
type channelConfig struct {
	Type   string
	Config map[string]string
}

// dispatchMessage sends a message to all enabled channels matching the given category.
// For digest category, pass the desired frequency (e.g. "weekly", "monthly", "quarterly")
// to only dispatch to channels matching that frequency. Pass "" to skip frequency filtering.
func (s *Scheduler) dispatchMessage(ctx context.Context, category, subject, body string, digestFreq ...string) {
	channels, err := s.queries.ListNotificationChannels(ctx)
	if err != nil {
		log.Printf("dispatch: failed to list channels: %v", err)
		return
	}

	freq := ""
	if len(digestFreq) > 0 {
		freq = digestFreq[0]
	}

	for _, ch := range channels {
		if !ch.Enabled {
			continue
		}
		if ch.ChannelFor != "all" && ch.ChannelFor != category {
			continue
		}
		// For digest messages, respect the frequency setting
		if category == "digest" {
			if ch.DigestFrequency == "never" {
				continue
			}
			// If a specific frequency was requested, only send to matching channels
			if freq != "" && ch.DigestFrequency != freq {
				continue
			}
		}

		var config map[string]string
		if err := json.Unmarshal(ch.Config, &config); err != nil {
			log.Printf("dispatch: invalid config for channel %s: %v", ch.Name, err)
			continue
		}

		switch ch.Type {
		case "ntfy":
			go sendNtfy(config, subject, body)
		case "webhook":
			go sendWebhook(config, subject, body)
		case "email":
			go sendEmail(config, subject, body)
		case "pushover":
			go sendPushover(config, subject, body)
		}
	}
}

func sendNtfy(config map[string]string, subject, body string) {
	url := config["url"]
	topic := config["topic"]
	if url == "" || topic == "" {
		return
	}
	endpoint := strings.TrimRight(url, "/") + "/" + topic
	req, err := http.NewRequest("POST", endpoint, strings.NewReader(body))
	if err != nil {
		log.Printf("ntfy: request error: %v", err)
		return
	}
	req.Header.Set("Title", subject)
	req.Header.Set("Priority", "default")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("ntfy: send error: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		log.Printf("ntfy: unexpected status %d", resp.StatusCode)
	}
}

func sendWebhook(config map[string]string, subject, body string) {
	url := config["url"]
	if url == "" {
		return
	}
	payload, _ := json.Marshal(map[string]string{"subject": subject, "body": body, "timestamp": time.Now().Format(time.RFC3339)})
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		log.Printf("webhook: send error: %v", err)
		return
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
}

func sendEmail(config map[string]string, subject, body string) {
	host := config["smtp_host"]
	port := config["smtp_port"]
	from := config["from"]
	to := config["to"]
	username := config["username"]
	password := config["password"]
	if host == "" || from == "" || to == "" {
		return
	}
	if port == "" {
		port = "587"
	}

	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/html; charset=utf-8\r\n\r\n%s", from, to, subject, body)

	var auth smtp.Auth
	if username != "" {
		auth = smtp.PlainAuth("", username, password, host)
	}
	err := smtp.SendMail(host+":"+port, auth, from, []string{to}, []byte(msg))
	if err != nil {
		log.Printf("email: send error: %v", err)
	}
}

func sendPushover(config map[string]string, subject, body string) {
	userKey := config["user_key"]
	apiToken := config["api_token"]
	if userKey == "" || apiToken == "" {
		return
	}
	vals := url.Values{}
	vals.Set("token", apiToken)
	vals.Set("user", userKey)
	vals.Set("title", subject)
	vals.Set("message", body)
	vals.Set("html", "1")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post("https://api.pushover.net/1/messages.json", "application/x-www-form-urlencoded", strings.NewReader(vals.Encode()))
	if err != nil {
		log.Printf("pushover: send error: %v", err)
		return
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
}
