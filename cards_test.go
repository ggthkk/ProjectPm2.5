package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestWaterBaselineAndDailyAcknowledgement(t *testing.T) {
	c := testConfig(t)
	c.Air, c.News, c.Games, c.Valorant = false, false, false, false
	a, err := newApp(c, false)
	if err != nil {
		t.Fatal(err)
	}
	level := 3
	msl := "1.25"
	row := WaterRow{Level: &level, MSL: &msl}
	row.Station.ID = 123
	s := Snapshot{Water: []WaterRow{row}}
	now := time.Now()
	if plans := a.alerts(s, now); len(plans) != 0 {
		t.Fatal("first observation must establish baseline, not claim a change")
	}
	level = 4
	if plans := a.alerts(s, now); len(plans) != 1 {
		t.Fatal("worsening known station must alert", len(plans))
	}
	level = 5
	plans := a.daily(context.Background(), s, now)
	if len(plans) != 1 || plans[0].Ack == nil {
		t.Fatal("daily water report must acknowledge the shown observations")
	}
	plans[0].Ack()
	if alerts := a.alerts(s, now); len(alerts) != 0 {
		t.Fatal("same readings repeated immediately after daily report")
	}
	if err := a.state.save(c.DataDir); err != nil {
		t.Fatal(err)
	}
	b, err := newApp(c, false)
	if err != nil {
		t.Fatal(err)
	}
	if alerts := b.alerts(s, now); len(alerts) != 0 {
		t.Fatal("restart repeated acknowledged water observations")
	}
}

func TestEmbedFieldsAreIncludedInDiscordLimits(t *testing.T) {
	for _, fields := range [][]EmbedField{
		{{Name: "value too long", Value: strings.Repeat("x", 1025)}},
		{{Name: "", Value: "empty name"}},
		{{Name: "one", Value: strings.Repeat("x", 1000)}, {Name: "two", Value: strings.Repeat("x", 1000)}, {Name: "three", Value: strings.Repeat("x", 1000)}, {Name: "four", Value: strings.Repeat("x", 1000)}, {Name: "five", Value: strings.Repeat("x", 1000)}, {Name: "six", Value: strings.Repeat("x", 1000)}},
	} {
		_, _, err := send(context.Background(), http.DefaultClient, "invalid", []Embed{{Title: "Limits", Fields: fields}})
		if err == nil || !(strings.Contains(err.Error(), "field") || strings.Contains(err.Error(), "limits")) {
			t.Fatalf("field limits not checked before transport: %v", err)
		}
	}
}
