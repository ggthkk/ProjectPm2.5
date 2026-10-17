package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestNewsBalancesDefaultSourcesAndSkipsOldFillers(t *testing.T) {
	c := testConfig(t)
	a, _ := newApp(c, true)
	now := time.Now()
	var rows []Item
	for _, host := range []string{"sheapgamer.com", "knowyourmeme.com"} {
		for n := 0; n < 4; n++ {
			rows = append(rows, Item{ID: fmt.Sprintf("%s/%d", host, n), Title: fmt.Sprintf("%s story %d", host, n), URL: fmt.Sprintf("https://%s/%d", host, n), Published: now.Add(-time.Duration(n) * time.Hour)})
		}
	}
	got, _ := a.items("news", rows, 4, now, 36*time.Hour)
	counts := map[string]int{}
	for _, i := range got {
		counts[sourceLabel(i)]++
	}
	if len(got) != 4 || counts["เกมถูกบอกด้วย"] != 2 || counts["Know Your Meme"] != 2 {
		t.Fatal(counts)
	}
	for n := range rows {
		if strings.Contains(rows[n].URL, "knowyourmeme") {
			rows[n].Published = now.Add(-48 * time.Hour)
		}
	}
	got, _ = a.items("news", rows, 4, now, 36*time.Hour)
	if len(got) != 2 {
		t.Fatal("must not fill missing foreign items with old stories", got)
	}
}

func TestWaterUnavailableRemainsExplicit(t *testing.T) {
	c := testConfig(t)
	c.Air, c.News, c.Games, c.Valorant = false, false, false, false
	a, _ := newApp(c, true)
	p := a.daily(context.Background(), Snapshot{Good: map[string]bool{"water": true}}, time.Now())
	if len(p) != 1 || len(p[0].Embeds) != 1 || len(p[0].Embeds[0].Fields) != 1 || !strings.Contains(p[0].Embeds[0].Fields[0].Value, "ไม่ได้หมายความว่าไม่มีน้ำท่วม") {
		t.Fatal(p)
	}
}

func TestFeedThumbnailRejectsNonWebURL(t *testing.T) {
	if got := feedThumbnail(`<img src="javascript:alert(1)">`); got != "" {
		t.Fatal(got)
	}
	if got := feedThumbnail(`<img src="https://example.com/photo.jpg">`); got != "https://example.com/photo.jpg" {
		t.Fatal(got)
	}
}

func TestSummaryDoesNotRepeatOrInvent(t *testing.T) {
	for _, tc := range []struct{ title, body, want string }{
		{"Patch released", "Patch released Body Fixes a login issue. More details here.", "Fixes a login issue."},
		{"ข่าวใหม่", "ข่าวใหม่", ""},
		{"Update", "Incomplete excerpt...", ""},
		{"Update", "A complete fact. Another fact.", "A complete fact."},
	} {
		if got := digestSummary(Item{Title: tc.title, Summary: tc.body}, 100); got != tc.want {
			t.Errorf("%q != %q", got, tc.want)
		}
	}
}

func TestNewsSelectionRelevanceFreshnessAndDuplicateURLs(t *testing.T) {
	c := testConfig(t)
	a, _ := newApp(c, true)
	now := time.Now()
	rows := []Item{
		{ID: "general", Title: "Apple product review", URL: "https://example.com/review", Published: now},
		{ID: "patch", Title: "VALORANT patch", URL: "https://example.com/patch", Published: now.Add(-time.Hour)},
		{ID: "duplicate", Title: "Another headline", URL: "https://example.com/patch?utm_source=rss", Published: now.Add(-2 * time.Hour)},
		{ID: "stale", Title: "VALORANT old", URL: "https://example.com/old", Published: now.Add(-48 * time.Hour)},
		{ID: "security", Title: "ช่องโหว่ใหม่", URL: "https://other.example/security", Published: now.Add(-3 * time.Hour)},
	}
	got, keys := a.items("news", rows, 3, now, 36*time.Hour)
	if len(got) != 3 || got[0].ID != "patch" || got[1].ID != "security" || got[2].ID != "general" || len(keys) != 3 {
		t.Fatal(got, keys)
	}
	if rows[0].ID != "general" {
		t.Fatal("selection mutated snapshot")
	}
}
