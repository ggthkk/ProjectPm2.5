package main

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	APIKey, Locations, DataDir, Zone, DigestTime                           string
	Webhook, WeatherWebhook, GamesWebhook, NewsWebhook, AlertsWebhook      string
	Poll                                                                   time.Duration
	WaterRadiusKM                                                          float64
	QuietStart, QuietEnd, MaxAlerts                                        int
	Provinces, AreaKeywords, NewsFeeds, Platforms                          []string
	Air, Valorant, Games, News, Water, Disasters, Riot                     bool
	AirURL, ValorantURL, GamesURL, WaterURL, GDACSURL, WarningURL, RiotURL string
	Location                                                               *time.Location
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
func list(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
func loadConfig() (Config, error) {
	c := Config{APIKey: env("API_KEY", ""), Locations: env("MAP", env("map", "")), DataDir: env("DATA_DIR", "data"), Zone: env("TZ", "Asia/Bangkok"), DigestTime: env("DIGEST_TIME", "09:00"), Webhook: env("DISCORD_WEBHOOK_URL", ""),
		Provinces: list(env("WATER_PROVINCES", "10,12,13")), AreaKeywords: list(env("ALERT_AREAS", "กรุงเทพ,นนทบุรี,ปทุมธานี,Bangkok,Nonthaburi,Pathum Thani")),
		NewsFeeds: list(env("NEWS_FEEDS", "https://www.bangkokpost.com/rss/data/topstories.xml,https://www.blognone.com/atom.xml")), Platforms: list(env("GAME_PLATFORMS", "Steam,Epic Games Store,GOG")),
		AirURL: "https://api.airvisual.com/v2/nearest_city", ValorantURL: "https://playvalorant.com/th-th/news/", GamesURL: "https://www.gamerpower.com/api/giveaways?type=game", WaterURL: "https://api-v3.thaiwater.net/api/v1/thaiwater30/public/waterlevel_load", GDACSURL: "https://www.gdacs.org/xml/rss.xml", WarningURL: "https://www.tmd.go.th/api/xml/warning-news", RiotURL: "https://valorant.secure.dyn.riotcdn.net/channels/public/x/status/ap.json"}
	var err error
	c.WaterRadiusKM, err = strconv.ParseFloat(env("WATER_RADIUS_KM", "10"), 64)
	if err != nil || !(c.WaterRadiusKM > 0 && c.WaterRadiusKM <= 200) {
		return c, fmt.Errorf("WATER_RADIUS_KM must be between 0 and 200 km")
	}
	c.Location, err = time.LoadLocation(c.Zone)
	if err != nil {
		return c, fmt.Errorf("invalid TZ")
	}
	if _, err = time.Parse("15:04", c.DigestTime); err != nil {
		return c, fmt.Errorf("DIGEST_TIME must be HH:MM")
	}
	c.Poll, err = time.ParseDuration(env("POLL_INTERVAL", "30m"))
	if err != nil || c.Poll < time.Minute {
		return c, fmt.Errorf("POLL_INTERVAL must be at least 1m")
	}
	for key, dst := range map[string]*int{"QUIET_START": &c.QuietStart, "QUIET_END": &c.QuietEnd, "MAX_ALERTS_PER_DAY": &c.MaxAlerts} {
		fallback := "23"
		if key == "QUIET_END" {
			fallback = "8"
		}
		if key == "MAX_ALERTS_PER_DAY" {
			fallback = "3"
		}
		*dst, err = strconv.Atoi(env(key, fallback))
		if err != nil || *dst < 0 || (key != "MAX_ALERTS_PER_DAY" && *dst > 23) || (key == "MAX_ALERTS_PER_DAY" && *dst > 20) {
			return c, fmt.Errorf("invalid %s", key)
		}
	}
	for key, dst := range map[string]*bool{"ENABLE_AIR": &c.Air, "ENABLE_VALORANT": &c.Valorant, "ENABLE_GAMES": &c.Games, "ENABLE_NEWS": &c.News, "ENABLE_WATER": &c.Water, "ENABLE_DISASTERS": &c.Disasters, "ENABLE_RIOT_STATUS": &c.Riot} {
		*dst, err = strconv.ParseBool(env(key, "true"))
		if err != nil {
			return c, fmt.Errorf("invalid %s", key)
		}
	}
	c.WeatherWebhook = env("DISCORD_WEATHER_WEBHOOK_URL", c.Webhook)
	c.GamesWebhook = env("DISCORD_GAMES_WEBHOOK_URL", c.Webhook)
	c.NewsWebhook = env("DISCORD_NEWS_WEBHOOK_URL", c.Webhook)
	c.AlertsWebhook = env("DISCORD_ALERTS_WEBHOOK_URL", c.Webhook)
	for _, hook := range []string{c.Webhook, c.WeatherWebhook, c.GamesWebhook, c.NewsWebhook, c.AlertsWebhook} {
		if hook != "" && !validWebhook(hook) {
			return c, fmt.Errorf("webhooks must use Discord HTTPS /api/webhooks URLs")
		}
	}
	if (c.Air || c.Water) && c.Locations != "" {
		if _, err := parseLocations(c.Locations); err != nil {
			return c, err
		}
	}
	return c, nil
}
func validWebhook(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && (u.Host == "discord.com" || u.Host == "discordapp.com" || u.Host == "canary.discord.com" || u.Host == "ptb.discord.com") && strings.HasPrefix(u.Path, "/api/webhooks/") && len(strings.Split(strings.Trim(u.Path, "/"), "/")) == 4 && u.User == nil && u.Fragment == ""
}
func quiet(c Config, now time.Time) bool {
	h := now.In(c.Location).Hour()
	if c.QuietStart == c.QuietEnd {
		return false
	}
	if c.QuietStart < c.QuietEnd {
		return h >= c.QuietStart && h < c.QuietEnd
	}
	return h >= c.QuietStart || h < c.QuietEnd
}
