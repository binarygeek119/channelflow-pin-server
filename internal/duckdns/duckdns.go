package duckdns

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Update sets the DuckDNS A record. Empty ip lets DuckDNS detect the caller.
func Update(subdomain, token, ip string) error {
	subdomain = strings.TrimSpace(subdomain)
	token = strings.TrimSpace(token)
	if subdomain == "" || token == "" {
		return fmt.Errorf("duckdns subdomain and token are required")
	}
	q := url.Values{}
	q.Set("domains", subdomain)
	q.Set("token", token)
	q.Set("ip", ip)
	u := "https://www.duckdns.org/update?" + q.Encode()
	client := &http.Client{Timeout: 15 * time.Second}
	res, err := client.Get(u)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 64))
	if res.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != "OK" {
		return fmt.Errorf("duckdns update failed")
	}
	return nil
}

func Hostname(subdomain string) string {
	subdomain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(subdomain)), ".duckdns.org")
	return subdomain + ".duckdns.org"
}
