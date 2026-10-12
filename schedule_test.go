package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDailyDoesNotRefetchAirAfterDelivery(t *testing.T) {
	calls := 0
	now := time.Now().UTC()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprintf(w, `{"status":"success","data":{"city":"Observed city","current":{"pollution":{"aqius":42,"ts":%q}}}}`, now.Format(time.RFC3339))
	}))
	defer server.Close()
	c := testConfig(t)
	c.AirURL = server.URL
	c.APIKey = "test-only"
	c.Locations = "13,100"
	c.News = false
	c.Games = false
	c.Valorant = false
	c.Water = false
	a, _ := newApp(c, false)
	a.client = server.Client()
	plans := a.daily(context.Background(), Snapshot{}, now)
	if len(plans) != 1 {
		t.Fatal(plans)
	}
	a.state.Deliveries[plans[0].ID] = Delivery{Status: "sent", At: now}
	if more := a.daily(context.Background(), Snapshot{}, now); len(more) != 0 || calls != 1 {
		t.Fatal("daily report fetched or planned twice", calls, len(more))
	}
}
func TestDailyAlertBudgetPersists(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.Write([]byte(`{"id":"ok"}`)) }))
	defer server.Close()
	c := testConfig(t)
	c.MaxAlerts = 1
	a, _ := newApp(c, false)
	a.client = server.Client()
	for _, id := range []string{"one", "two"} {
		if e := a.deliver(context.Background(), Plan{ID: id, Hook: server.URL, Alert: true, Embeds: []Embed{{Title: "Alert", Description: "Text"}}}, time.Now()); e != nil {
			t.Fatal(e)
		}
	}
	b, e := newApp(c, false)
	if e != nil || b.state.AlertCount != 1 || calls != 1 {
		t.Fatal(e, calls)
	}
}
