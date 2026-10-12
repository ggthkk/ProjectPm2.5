package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testConfig(t *testing.T) Config {
	t.Helper()
	c, e := loadConfig()
	if e != nil {
		t.Fatal(e)
	}
	c.DataDir = t.TempDir()
	return c
}
func TestAirRejectsFailureMissingAndStale(t *testing.T) {
	now := time.Now().UTC()
	for _, body := range []string{`{"status":"fail","data":{"message":"call_limit_reached"}}`, `{"status":"success","data":{"city":"A","current":{"pollution":{"ts":"2020-01-01T00:00:00Z","aqius":0}}}}`, `{"status":"success","data":{"city":"A","current":{"pollution":{}}}}`} {
		if _, err := decodeAir([]byte(body), now); err == nil {
			t.Fatal("unavailable data accepted")
		}
	}
	body := `{"status":"success","data":{"city":"A","current":{"pollution":{"ts":"` + now.Format(time.RFC3339) + `","aqius":0}}}}`
	if _, err := decodeAir([]byte(body), now); err != nil {
		t.Fatalf("real observed zero must remain valid: %v", err)
	}
}
func TestCoordinateValidation(t *testing.T) {
	for _, raw := range []string{"", "90.1,100", "13,181", "NaN,100", "13,100,1"} {
		if _, err := parseLocations(raw); err == nil {
			t.Errorf("accepted invalid coordinates %q", raw)
		}
	}
	rows, err := parseLocations("13.8,100.7; 13.9,100.6;")
	if err != nil || len(rows) != 2 {
		t.Fatal(rows, err)
	}
}
func TestFeedFormatsAndFreshness(t *testing.T) {
	rss := `<rss><channel><title>Publisher</title><item><title>A &amp; B</title><link>https://example.com/a?utm_source=x</link><pubDate>Mon, 12 Oct 2026 12:00:00 +0700</pubDate><description><![CDATA[<p>Headline <b>summary</b></p>]]></description></item></channel></rss>`
	_, items, _, err := parseFeed([]byte(rss))
	if err != nil || len(items) != 1 || items[0].ID != "https://example.com/a" || items[0].Summary != "Headline summary" {
		t.Fatal(items, err)
	}
	atom := `<feed xmlns="http://www.w3.org/2005/Atom"><title>Tech</title><entry><title>Update</title><link href="https://example.com/tech"/><updated>2026-10-12T12:00:00Z</updated><summary>Short summary</summary></entry></feed>`
	_, items, _, err = parseFeed([]byte(atom))
	if err != nil || len(items) != 1 || items[0].Published.IsZero() {
		t.Fatal(items, err)
	}
	if fresh(time.Now().Add(24*time.Hour), time.Now(), time.Hour) {
		t.Fatal("future timestamp accepted")
	}
	if _, _, _, err = parseFeed([]byte(`<html>blocked</html>`)); err == nil {
		t.Fatal("HTML block page treated as feed")
	}
}
func TestValorantCardParsing(t *testing.T) {
	body := `<script id="__NEXT_DATA__" type="application/json">{"cards":[{"title":"Patch","action":{"payload":{"url":"/th-th/news/game-updates/patch"}},"category":{"machineName":"game_updates"},"publishedAt":"2026-10-12T12:00:00Z","description":{"body":"<b>Agent updates</b>"}},{"title":"Unrelated","category":{"machineName":"esports"}}]}</script>`
	items, err := parseValorant([]byte(body))
	if err != nil || len(items) != 1 || items[0].Summary != "Agent updates" || !strings.HasPrefix(items[0].URL, "https://playvalorant.com/") {
		t.Fatal(items, err)
	}
	if _, err = parseValorant([]byte(`<html></html>`)); err == nil {
		t.Fatal("schema change silently ignored")
	}
}
func TestWaterUsesOverBankText(t *testing.T) {
	r := WaterRow{Diff: "ล้นตลิ่ง (ม.)"}
	if waterLevel(r) != 5 {
		t.Fatal("over-bank station with missing numeric status ignored")
	}
}
func TestQuietHours(t *testing.T) {
	c := testConfig(t)
	for _, tc := range []struct {
		hour int
		want bool
	}{{22, false}, {23, true}, {0, true}, {7, true}, {8, false}} {
		now := time.Date(2026, 10, 13, tc.hour, 0, 0, 0, c.Location)
		if quiet(c, now) != tc.want {
			t.Fatal(tc)
		}
	}
	c.QuietStart = 12
	c.QuietEnd = 14
	if !quiet(c, time.Date(2026, 10, 13, 13, 0, 0, 0, c.Location)) {
		t.Fatal("daytime quiet interval")
	}
}
func TestDiscordJSONMentionsAndDefiniteRejection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p Payload
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			t.Error(err)
		}
		if r.URL.Query().Get("wait") != "true" || p.AllowedMentions.Parse == nil || len(p.AllowedMentions.Parse) != 0 {
			t.Error("unsafe mentions or missing receipt request")
		}
		if p.Embeds[0].Description != "quotes \" slash \\ newline\n@everyone" {
			t.Error("payload escaping changed content")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"receipt"}`))
	}))
	defer server.Close()
	id, ok, err := send(context.Background(), server.Client(), server.URL, []Embed{{Title: "Test", Description: "quotes \" slash \\ newline\n@everyone"}})
	if err != nil || !ok || id != "receipt" {
		t.Fatal(id, ok, err)
	}
}
func TestDeliverySurvivesRestartAndSuppressesDuplicates(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.Write([]byte(`{"id":"receipt"}`)) }))
	defer server.Close()
	c := testConfig(t)
	a, _ := newApp(c, false)
	a.client = server.Client()
	p := Plan{Hook: server.URL, ID: "digest:day", Embeds: []Embed{{Title: "Test", Description: "Message"}}, Keys: []string{"news:one"}}
	now := time.Now()
	if err := a.deliver(context.Background(), p, now); err != nil {
		t.Fatal(err)
	}
	b, err := newApp(c, false)
	if err != nil {
		t.Fatal(err)
	}
	b.client = server.Client()
	if err = b.deliver(context.Background(), p, now); err != nil || calls != 1 || b.unseen("news:one") {
		t.Fatal(err, calls)
	}
	data, _ := os.ReadFile(filepath.Join(c.DataDir, "state.json"))
	if strings.Contains(string(data), server.URL) {
		t.Fatal("webhook persisted in state")
	}
}
func TestAmbiguousDeliveryIsNotRetried(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) }))
	defer server.Close()
	c := testConfig(t)
	a, _ := newApp(c, false)
	a.client = server.Client()
	p := Plan{Hook: server.URL, ID: "alert:x", Embeds: []Embed{{Title: "Test", Description: "Message"}}, Keys: []string{"news:uncertain"}, Alert: true}
	if err := a.deliver(context.Background(), p, time.Now()); err == nil {
		t.Fatal("500 should be uncertain")
	}
	b, _ := newApp(c, false)
	b.client = server.Client()
	if b.unseen("news:uncertain") {
		t.Fatal("pending item may be duplicated in another bundle")
	}
	if err := b.deliver(context.Background(), p, time.Now()); err != nil || calls != 1 || b.state.AlertCount != 1 {
		t.Fatal(calls, err)
	}
}
func TestRejectedDeliveryCanRetry(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(429)
			return
		}
		w.Write([]byte(`{"id":"ok"}`))
	}))
	defer server.Close()
	a, _ := newApp(testConfig(t), false)
	a.client = server.Client()
	p := Plan{Hook: server.URL, ID: "alert:retry", Embeds: []Embed{{Title: "Test", Description: "Message"}}, Alert: true}
	if a.deliver(context.Background(), p, time.Now()) == nil {
		t.Fatal("429 should be reported")
	}
	if a.state.AlertCount != 0 {
		t.Fatal("rejected send consumed quota")
	}
	if err := a.deliver(context.Background(), p, time.Now()); err != nil || calls != 2 {
		t.Fatal(calls, err)
	}
}
func TestInvalidStateFailsClosed(t *testing.T) {
	dir := t.TempDir()
	for _, data := range []string{`broken`, `{}`, `{"version":1}`} {
		os.WriteFile(filepath.Join(dir, "state.json"), []byte(data), 0600)
		if _, err := loadState(dir); err == nil {
			t.Fatal("corrupt history must not be reset")
		}
	}
}
func TestDryRunDoesNotWriteOrSend(t *testing.T) {
	c := testConfig(t)
	a, _ := newApp(c, true)
	p := Plan{ID: "dry", Embeds: []Embed{{Title: "Preview", Description: "Text"}}}
	if err := a.deliver(context.Background(), p, time.Now()); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(c.DataDir)
	if len(entries) > 0 {
		t.Fatal("dry run wrote persistent state")
	}
}
