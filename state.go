package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Delivery struct {
	Status  string    `json:"status"`
	At      time.Time `json:"at"`
	Receipt string    `json:"receipt,omitempty"`
	Keys    []string  `json:"keys,omitempty"`
}
type State struct {
	Version       int                  `json:"version"`
	Seen          map[string]time.Time `json:"seen"`
	Initialized   map[string]bool      `json:"initialized"`
	Deliveries    map[string]Delivery  `json:"deliveries"`
	Water         map[string]int       `json:"water"`
	WaterNotified map[string]time.Time `json:"water_notified"`
	Riot          map[string]string    `json:"riot"`
	Day           string               `json:"day"`
	AlertCount    int                  `json:"alert_count"`
}

func emptyState() *State {
	return &State{Version: 1, Seen: map[string]time.Time{}, Initialized: map[string]bool{}, Deliveries: map[string]Delivery{}, Water: map[string]int{}, WaterNotified: map[string]time.Time{}, Riot: map[string]string{}}
}
func loadState(dir string) (*State, error) {
	s := emptyState()
	data, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read state: %w", err)
	}
	s = &State{}
	if json.Unmarshal(data, s) != nil || s.Version != 1 || s.Seen == nil || s.Deliveries == nil || s.Initialized == nil || s.Water == nil || s.WaterNotified == nil || s.Riot == nil {
		return nil, fmt.Errorf("invalid state; refusing to reset delivery history")
	}
	return s, nil
}
func (s *State) save(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "state-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, filepath.Join(dir, "state.json"))
}
func (s *State) prune(now time.Time) {
	for k, t := range s.Seen {
		if now.Sub(t) > 90*24*time.Hour {
			delete(s.Seen, k)
		}
	}
	for k, d := range s.Deliveries {
		if d.Status == "sent" && now.Sub(d.At) > 90*24*time.Hour {
			delete(s.Deliveries, k)
		}
	}
}
