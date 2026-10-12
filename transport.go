package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type Embed struct {
	Title       string       `json:"title"`
	Description string       `json:"description"`
	URL         string       `json:"url,omitempty"`
	Color       int          `json:"color"`
	Fields      []EmbedField `json:"fields,omitempty"`
}
type EmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}
type Payload struct {
	Embeds          []Embed `json:"embeds"`
	AllowedMentions struct {
		Parse []string `json:"parse"`
	} `json:"allowed_mentions"`
}

func get(ctx context.Context, client *http.Client, raw string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid source URL")
	}
	req.Header.Set("User-Agent", "ProjectPm2.5-DiscordDigest/1.0")
	req.Header.Set("Accept", "*/*")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("source request failed (%s)", req.URL.Hostname())
	}
	defer resp.Body.Close()
	if resp.StatusCode == 204 || resp.StatusCode == 201 {
		return []byte("[]"), nil
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("source %s HTTP %d", req.URL.Hostname(), resp.StatusCode)
	}
	const limit = 8 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read source failed")
	}
	if len(data) > limit {
		return nil, fmt.Errorf("source response too large")
	}
	return data, nil
}

// A transport error is ambiguous: leave a durable pending record rather than resend.
func send(ctx context.Context, client *http.Client, hook string, embeds []Embed) (string, bool, error) {
	p := Payload{Embeds: embeds}
	p.AllowedMentions.Parse = []string{}
	total := 0
	if len(embeds) == 0 || len(embeds) > 10 {
		return "", false, fmt.Errorf("invalid embed count")
	}
	for _, e := range embeds {
		total += len([]rune(e.Title)) + len([]rune(e.Description))
		if len(e.Fields) > 25 {
			return "", false, fmt.Errorf("too many embed fields")
		}
		for _, f := range e.Fields {
			if strings.TrimSpace(f.Name) == "" || strings.TrimSpace(f.Value) == "" || len([]rune(f.Name)) > 256 || len([]rune(f.Value)) > 1024 {
				return "", false, fmt.Errorf("invalid embed field")
			}
			total += len([]rune(f.Name)) + len([]rune(f.Value))
		}
		if len([]rune(e.Title)) > 256 || len([]rune(e.Description)) > 4096 {
			return "", false, fmt.Errorf("embed exceeds Discord limits")
		}
	}
	if total > 5900 {
		return "", false, fmt.Errorf("message exceeds Discord limits")
	}
	body, err := json.Marshal(p)
	if err != nil {
		return "", false, fmt.Errorf("encode Discord payload")
	}
	u, err := url.Parse(hook)
	if err != nil {
		return "", false, fmt.Errorf("invalid webhook")
	}
	q := u.Query()
	q.Set("wait", "true")
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return "", false, fmt.Errorf("invalid webhook request")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("Discord result uncertain; automatic resend suppressed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode >= 500 {
			return "", false, fmt.Errorf("Discord HTTP %d; result uncertain", resp.StatusCode)
		}
		return fmt.Sprintf("HTTP %d", resp.StatusCode), false, nil
	}
	var result struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 16384)).Decode(&result)
	return result.ID, true, nil
}
func short(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
func safeText(s string) string {
	s = strings.ReplaceAll(s, "@", "＠")
	return strings.NewReplacer("[", "［", "]", "］", "*", "＊", "`", "ˋ", "<", "‹", ">", "›").Replace(s)
}
