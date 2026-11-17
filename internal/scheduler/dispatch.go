package scheduler

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/url"
	"strings"
	"time"
)

func isDisallowedOutboundIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip4 := ip.To4(); ip4 != nil {
		if ip4[0] == 0 || (ip4[0] == 100 && ip4[1]&0xc0 == 64) {
			return true
		}
		ip = ip4
	}
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified()
}

func validateSafeWebhookURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("invalid http(s) URL")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return nil, errors.New("disallowed internal hostname")
	}
	if ip := net.ParseIP(host); ip != nil && isDisallowedOutboundIP(ip) {
		return nil, errors.New("disallowed IP address")
	}
	return u, nil
}

func newSafeWebhookClient() *http.Client {
	dialer := &net.Dialer{
		Timeout:   8 * time.Second,
		KeepAlive: 15 * time.Second,
	}
	transport := &http.Transport{
		TLSHandshakeTimeout:   8 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			hostLower := strings.ToLower(host)
			if hostLower == "localhost" || strings.HasSuffix(hostLower, ".localhost") || strings.HasSuffix(hostLower, ".local") || strings.HasSuffix(hostLower, ".internal") {
				return nil, errors.New("blocked internal host")
			}
			if ip := net.ParseIP(host); ip != nil {
				if isDisallowedOutboundIP(ip) {
					return nil, errors.New("blocked private or loopback IP")
				}
				return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			if len(ips) == 0 {
				return nil, errors.New("host resolved to no IP addresses")
			}
			for _, ipAddr := range ips {
				if isDisallowedOutboundIP(ipAddr.IP) {
					return nil, errors.New("blocked host resolving to private or loopback IP")
				}
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
		},
	}
	return &http.Client{
		Timeout:   10 * time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func sanitizeHeaderValue(s string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(strings.TrimSpace(s))
}

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
	rawURL := strings.TrimSpace(config["url"])
	topic := sanitizeHeaderValue(config["topic"])
	if rawURL == "" || topic == "" || strings.ContainsAny(topic, "/\\") || strings.Contains(topic, "..") {
		return
	}
	if _, err := validateSafeWebhookURL(rawURL); err != nil {
		log.Printf("ntfy: blocked unsafe URL: %v", err)
		return
	}
	endpoint := strings.TrimRight(rawURL, "/") + "/" + url.PathEscape(topic)
	req, err := http.NewRequest("POST", endpoint, strings.NewReader(body))
	if err != nil {
		log.Printf("ntfy: request error: %v", err)
		return
	}
	req.Header.Set("Title", sanitizeHeaderValue(subject))
	req.Header.Set("Priority", "default")
	client := newSafeWebhookClient()
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("ntfy: send error: %v", err)
		return
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 300 {
		log.Printf("ntfy: unexpected status %d", resp.StatusCode)
	}
}

func sendWebhook(config map[string]string, subject, body string) {
	rawURL := strings.TrimSpace(config["url"])
	if rawURL == "" {
		return
	}
	if _, err := validateSafeWebhookURL(rawURL); err != nil {
		log.Printf("webhook: blocked unsafe URL: %v", err)
		return
	}
	payload, _ := json.Marshal(map[string]string{"subject": subject, "body": body, "timestamp": time.Now().UTC().Format(time.RFC3339)})
	client := newSafeWebhookClient()
	resp, err := client.Post(rawURL, "application/json", bytes.NewReader(payload))
	if err != nil {
		log.Printf("webhook: send error: %v", err)
		return
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
}

func sendEmail(config map[string]string, subject, body string) {
	host := sanitizeHeaderValue(config["smtp_host"])
	port := sanitizeHeaderValue(config["smtp_port"])
	from := sanitizeHeaderValue(config["from"])
	to := sanitizeHeaderValue(config["to"])
	username := sanitizeHeaderValue(config["username"])
	password := config["password"]
	if host == "" || from == "" || to == "" {
		return
	}
	if port == "" {
		port = "587"
	}

	fromAddr, err := mail.ParseAddress(from)
	if err != nil {
		log.Printf("email: invalid from address: %v", err)
		return
	}
	toAddr, err := mail.ParseAddress(to)
	if err != nil {
		log.Printf("email: invalid to address: %v", err)
		return
	}

	cleanSubject := sanitizeHeaderValue(subject)
	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/html; charset=utf-8\r\n\r\n%s",
		fromAddr.String(), toAddr.String(), cleanSubject, body)

	addr := net.JoinHostPort(host, port)
	deadline := time.Now().Add(15 * time.Second)
	var conn net.Conn
	if port == "465" {
		conn, err = tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", addr, &tls.Config{
			ServerName: host,
			MinVersion: tls.VersionTLS12,
		})
	} else {
		conn, err = net.DialTimeout("tcp", addr, 10*time.Second)
	}
	if err != nil {
		log.Printf("email: connect error: %v", err)
		return
	}
	defer conn.Close()
	_ = conn.SetDeadline(deadline)

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		log.Printf("email: smtp client error: %v", err)
		return
	}
	defer client.Close()

	if port != "465" {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
				log.Printf("email: starttls error: %v", err)
				return
			}
		}
	}

	if username != "" {
		auth := smtp.PlainAuth("", username, password, host)
		if err := client.Auth(auth); err != nil {
			log.Printf("email: auth error: %v", err)
			return
		}
	}

	if err := client.Mail(fromAddr.Address); err != nil {
		log.Printf("email: mail from error: %v", err)
		return
	}
	if err := client.Rcpt(toAddr.Address); err != nil {
		log.Printf("email: rcpt to error: %v", err)
		return
	}
	wc, err := client.Data()
	if err != nil {
		log.Printf("email: data error: %v", err)
		return
	}
	if _, err := wc.Write([]byte(msg)); err != nil {
		_ = wc.Close()
		log.Printf("email: write error: %v", err)
		return
	}
	if err := wc.Close(); err != nil {
		log.Printf("email: close error: %v", err)
		return
	}
	_ = client.Quit()
}

func sendPushover(config map[string]string, subject, body string) {
	userKey := strings.TrimSpace(config["user_key"])
	apiToken := strings.TrimSpace(config["api_token"])
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
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
}
