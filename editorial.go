package main

import (
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

var bodyLabel = regexp.MustCompile(`(?i)\s+Body\s+`)
var sentenceEnd = regexp.MustCompile(`[.!?。](?:\s+|$)`)
var feedImage = regexp.MustCompile(`(?i)<img\b[^>]*\bsrc=["']([^"']+)["']`)

func feedThumbnail(raw string) string {
	if m := feedImage.FindStringSubmatch(raw); len(m) == 2 {
		return safeURL(m[1])
	}
	return ""
}

// Extract a complete additional sentence, never invent facts or translate them.
// If the feed offers only a headline or a truncated excerpt, the link is enough.
func digestSummary(i Item, limit int) string {
	s := text(i.Summary)
	title := text(i.Title)
	if len(s) >= len(title) && strings.EqualFold(s[:len(title)], title) {
		s = strings.TrimSpace(s[len(title):])
	}
	s = strings.TrimLeft(s, " :–—-\n")
	s = strings.TrimSpace(bodyLabel.ReplaceAllString(" "+s, " "))
	if s == "" || strings.EqualFold(s, title) {
		return ""
	}
	// Prefer the first complete sentence that fits, rather than cutting mid-fact.
	start := 0
	for _, at := range sentenceEnd.FindAllStringIndex(s, -1) {
		candidate := strings.TrimSpace(s[start:at[1]])
		start = at[1]
		if len([]rune(candidate)) <= limit && !strings.HasSuffix(candidate, "...") && candidate != "" {
			return candidate
		}
	}
	if strings.HasSuffix(s, "…") || strings.HasSuffix(s, "...") || len([]rune(s)) > limit {
		return ""
	}
	return s
}

func sourceLabel(i Item) string {
	u, _ := url.Parse(i.URL)
	if u != nil {
		host := strings.TrimPrefix(u.Hostname(), "www.")
		switch host {
		case "sheapgamer.com":
			return "เกมถูกบอกด้วย"
		case "knowyourmeme.com", "trending.knowyourmeme.com":
			return "Know Your Meme"
		case "blognone.com":
			return "Blognone"
		case "bangkokpost.com":
			return "Bangkok Post"
		case "playvalorant.com":
			return "VALORANT"
		case "gamerpower.com":
			return "GamerPower"
		}
		if host != "" {
			return host
		}
	}
	return short(text(i.Source), 32)
}

// Rules express relevance to this server, not popularity or emergency severity.
func newsTopic(i Item, areas []string) (int, string) {
	u, _ := url.Parse(i.URL)
	if u != nil {
		switch u.Hostname() {
		case "sheapgamer.com", "www.sheapgamer.com":
			return 3, "🎮 เกมและเรื่องน่าคุย"
		case "knowyourmeme.com", "trending.knowyourmeme.com":
			return 3, "😂 ที่มาของมีม"
		}
	}
	t := strings.ToLower(text(i.Title))
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(t, strings.ToLower(w)) {
				return true
			}
		}
		return false
	}
	if has("valorant", "วาโลแรนต์") {
		return 4, "🎮 VALORANT"
	}
	if has("น้ำท่วม", "ฝน", "พายุ", "flood", "storm", "ไฟดับ", "ปิดถนน") {
		for _, area := range areas {
			if area != "" && has(area) {
				return 4, "📍 เหตุในพื้นที่"
			}
		}
	}
	if has("ช่องโหว่", "ข้อมูลรั่ว", "มัลแวร์", "หลอกลวง", "phishing", "vulnerability", "data breach", "ransomware") {
		return 3, "🛡 ความปลอดภัย"
	}
	if has("steam", "epic games", "เกม", "gaming", "game ") {
		return 2, "🎮 เกม"
	}
	if has("apple", "microsoft", "google", "android", "ios", " ai ", "openai", "ซอฟต์แวร์", "อัปเดต") {
		return 1, "💻 เทคโนโลยี"
	}
	return 0, "📰 ข่าวทั่วไป"
}

func storyKey(title string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, text(title))
}

func rankedNews(rows []Item, areas []string) []Item {
	out := append([]Item(nil), rows...)
	sort.SliceStable(out, func(i, j int) bool {
		a, _ := newsTopic(out[i], areas)
		b, _ := newsTopic(out[j], areas)
		if a != b {
			return a > b
		}
		return out[i].Published.After(out[j].Published)
	})
	return out
}
