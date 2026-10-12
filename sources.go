package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Item struct {
	ID, Title, Summary, URL, Source string
	Published, Expires              time.Time
}

var tags = regexp.MustCompile(`<[^>]*>`)
var spaces = regexp.MustCompile(`\s+`)

func text(s string) string {
	return strings.TrimSpace(spaces.ReplaceAllString(html.UnescapeString(tags.ReplaceAllString(s, " ")), " "))
}
func fresh(t, now time.Time, age time.Duration) bool {
	return !t.IsZero() && t.After(now.Add(-age)) && !t.After(now.Add(10*time.Minute))
}
func parseTime(raw string) time.Time {
	for _, f := range []string{time.RFC3339Nano, time.RFC1123Z, time.RFC1123, time.RFC822Z, "Mon, 02 Jan 2006 15:04:05 -07:00"} {
		if t, e := time.Parse(f, raw); e == nil {
			return t
		}
	}
	return time.Time{}
}
func safeURL(raw string) string {
	u, e := url.Parse(strings.TrimSpace(raw))
	if e != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return ""
	}
	return u.String()
}
func normalizedURL(raw string) string {
	u, e := url.Parse(raw)
	if e != nil {
		return raw
	}
	u.Fragment = ""
	q := u.Query()
	for k := range q {
		if strings.HasPrefix(k, "utm_") || k == "fbclid" || k == "gclid" {
			q.Del(k)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

type FeedItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	GUID        string `xml:"guid"`
	Description string `xml:"description"`
	Date        string `xml:"pubDate"`
	Country     string `xml:"country"`
	ISO         string `xml:"iso3"`
	Level       string `xml:"alertlevel"`
	Event       string `xml:"eventtype"`
	Modified    string `xml:"datemodified"`
}

func parseFeed(data []byte) (string, []Item, []FeedItem, error) {
	var root struct {
		XMLName xml.Name
		Channel struct {
			Title string     `xml:"title"`
			Items []FeedItem `xml:"item"`
		} `xml:"channel"`
		Title   string `xml:"title"`
		Entries []struct {
			Title     string `xml:"title"`
			ID        string `xml:"id"`
			Summary   string `xml:"summary"`
			Content   string `xml:"content"`
			Published string `xml:"published"`
			Updated   string `xml:"updated"`
			Links     []struct {
				Href string `xml:"href,attr"`
				Rel  string `xml:"rel,attr"`
			} `xml:"link"`
		} `xml:"entry"`
	}
	if err := xml.Unmarshal(data, &root); err != nil {
		return "", nil, nil, fmt.Errorf("invalid feed XML")
	}
	var out []Item
	if root.XMLName.Local == "rss" {
		for _, r := range root.Channel.Items {
			link := safeURL(r.Link)
			if link == "" {
				continue
			}
			out = append(out, Item{ID: normalizedURL(link), Title: text(r.Title), Summary: text(r.Description), URL: link, Source: root.Channel.Title, Published: parseTime(r.Date)})
		}
		return root.Channel.Title, out, root.Channel.Items, nil
	}
	if root.XMLName.Local != "feed" {
		return "", nil, nil, fmt.Errorf("unrecognized feed format")
	}
	for _, r := range root.Entries {
		link := ""
		for _, l := range r.Links {
			if l.Rel == "" || l.Rel == "alternate" {
				link = safeURL(l.Href)
				break
			}
		}
		if link == "" {
			continue
		}
		published := parseTime(r.Published)
		if published.IsZero() {
			published = parseTime(r.Updated)
		}
		summary := r.Summary
		if summary == "" {
			summary = r.Content
		}
		out = append(out, Item{ID: normalizedURL(link), Title: text(r.Title), Summary: text(summary), URL: link, Source: root.Title, Published: published})
	}
	return root.Title, out, nil, nil
}
func fetchNews(ctx context.Context, client *http.Client, c Config, now time.Time) ([]Item, error) {
	var out []Item
	var errs []string
	for _, feed := range c.NewsFeeds {
		data, e := get(ctx, client, feed)
		if e != nil {
			errs = append(errs, e.Error())
			continue
		}
		_, items, _, e := parseFeed(data)
		if e != nil {
			errs = append(errs, e.Error())
			continue
		}
		for _, i := range items {
			if fresh(i.Published, now, 36*time.Hour) {
				out = append(out, i)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Published.After(out[j].Published) })
	if len(out) == 0 && len(errs) > 0 {
		return nil, fmt.Errorf("news feeds unavailable: %s", strings.Join(errs, "; "))
	}
	return out, nil
}

var nextData = regexp.MustCompile(`(?s)<script[^>]*id="__NEXT_DATA__"[^>]*>(.*?)</script>`)

func parseValorant(data []byte) ([]Item, error) {
	m := nextData.FindSubmatch(data)
	if len(m) != 2 {
		return nil, fmt.Errorf("Valorant page schema changed: NEXT_DATA absent")
	}
	var root any
	if json.Unmarshal(m[1], &root) != nil {
		return nil, fmt.Errorf("invalid Valorant page data")
	}
	var out []Item
	seen := map[string]bool{}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			action, _ := x["action"].(map[string]any)
			payload, _ := action["payload"].(map[string]any)
			raw, _ := payload["url"].(string)
			title, _ := x["title"].(string)
			category, _ := x["category"].(map[string]any)
			kind, _ := category["machineName"].(string)
			if title != "" && (kind == "game_updates" || kind == "announcements") && raw != "" {
				if strings.HasPrefix(raw, "/") {
					raw = "https://playvalorant.com" + raw
				}
				link := safeURL(raw)
				published, _ := x["publishedAt"].(string)
				desc, _ := x["description"].(map[string]any)
				body, _ := desc["body"].(string)
				if link != "" && !seen[link] {
					seen[link] = true
					out = append(out, Item{ID: normalizedURL(link), Title: title, Summary: text(body), URL: link, Source: "VALORANT ทางการ", Published: parseTime(published)})
				}
			}
			for _, child := range x {
				walk(child)
			}
		case []any:
			for _, child := range x {
				walk(child)
			}
		}
	}
	walk(root)
	sort.Slice(out, func(i, j int) bool { return out[i].Published.After(out[j].Published) })
	if len(out) == 0 {
		return nil, fmt.Errorf("Valorant page has no recognized news cards")
	}
	return out, nil
}
func fetchValorant(ctx context.Context, client *http.Client, c Config) ([]Item, error) {
	data, e := get(ctx, client, c.ValorantURL)
	if e != nil {
		return nil, e
	}
	return parseValorant(data)
}
func fetchGames(ctx context.Context, client *http.Client, c Config) ([]Item, error) {
	data, e := get(ctx, client, c.GamesURL)
	if e != nil {
		return nil, e
	}
	var rows []struct {
		ID          int    `json:"id"`
		Title       string `json:"title"`
		Description string `json:"description"`
		URL         string `json:"gamerpower_url"`
		Open        string `json:"open_giveaway_url"`
		Type        string `json:"type"`
		Status      string `json:"status"`
		Platforms   string `json:"platforms"`
		End         string `json:"end_date"`
	}
	if json.Unmarshal(data, &rows) != nil {
		return nil, fmt.Errorf("invalid GamerPower response")
	}
	var out []Item
	for _, r := range rows {
		if !strings.EqualFold(r.Type, "game") || !strings.EqualFold(r.Status, "active") {
			continue
		}
		match := false
		for _, p := range list(r.Platforms) {
			for _, wanted := range c.Platforms {
				if strings.EqualFold(p, wanted) {
					match = true
				}
			}
		}
		if !match {
			continue
		}
		link := safeURL(r.Open)
		if link == "" {
			link = safeURL(r.URL)
		}
		if link == "" {
			continue
		}
		summary := r.Platforms
		if r.End != "" && r.End != "N/A" {
			summary += " · หมดเขต: " + strings.Split(r.End, " ")[0] + " (ตามต้นทาง)"
		}
		summary += " · " + short(text(r.Description), 80)
		out = append(out, Item{ID: strconv.Itoa(r.ID), Title: r.Title, Summary: summary, URL: link, Source: "GamerPower"})
	}
	return out, nil
}

type WaterRow struct {
	DistanceKM float64 `json:"-"`
	Time       string  `json:"waterlevel_datetime"`
	Level      *int    `json:"situation_level"`
	MSL        *string `json:"waterlevel_msl"`
	Diff       string  `json:"diff_wl_bank_text"`
	Station    struct {
		Lat  *float64 `json:"tele_station_lat"`
		Lon  *float64 `json:"tele_station_long"`
		ID   int      `json:"id"`
		Name struct {
			TH string `json:"th"`
		} `json:"tele_station_name"`
	} `json:"station"`
	Geo struct {
		ProvinceCode string `json:"province_code"`
		Province     struct {
			TH string `json:"th"`
		} `json:"province_name"`
	} `json:"geocode"`
}

func waterLevel(r WaterRow) int {
	if strings.HasPrefix(r.Diff, "ล้นตลิ่ง") {
		return 5
	}
	if r.Level == nil {
		return 0
	}
	return *r.Level
}
func fetchWater(ctx context.Context, client *http.Client, c Config, now time.Time) ([]WaterRow, error) {
	var locations []Coordinates
	if c.Locations != "" {
		var err error
		locations, err = parseLocations(c.Locations)
		if err != nil {
			return nil, err
		}
	}
	data, e := get(ctx, client, c.WaterURL)
	if e != nil {
		return nil, e
	}
	var root struct {
		Data *struct {
			Rows []WaterRow `json:"data"`
		} `json:"waterlevel_data"`
	}
	if json.Unmarshal(data, &root) != nil || root.Data == nil {
		return nil, fmt.Errorf("invalid ThaiWater response")
	}
	var out []WaterRow
	for _, r := range root.Data.Rows {
		match := false
		for _, p := range c.Provinces {
			if r.Geo.ProvinceCode == p {
				match = true
			}
		}
		if len(locations) > 0 {
			if r.Station.Lat == nil || r.Station.Lon == nil || !(*r.Station.Lat >= -90 && *r.Station.Lat <= 90 && *r.Station.Lon >= -180 && *r.Station.Lon <= 180) {
				continue
			}
			r.DistanceKM = nearestDistanceKM(Coordinates{*r.Station.Lat, *r.Station.Lon}, locations)
			match = r.DistanceKM <= c.WaterRadiusKM
		}
		if !match || r.MSL == nil {
			continue
		}
		v, e := strconv.ParseFloat(*r.MSL, 64)
		if e != nil || v <= -100 || v >= 1000 {
			continue
		}
		t, e := time.ParseInLocation("2006-01-02 15:04", r.Time, c.Location)
		if e != nil || !fresh(t, now, 3*time.Hour) {
			continue
		}
		out = append(out, r)
	}
	if len(out) == 0 && len(locations) == 0 {
		return nil, fmt.Errorf("no fresh ThaiWater stations for selected provinces")
	}
	sort.Slice(out, func(i, j int) bool {
		if waterLevel(out[i]) == waterLevel(out[j]) {
			return out[i].DistanceKM < out[j].DistanceKM
		}
		return waterLevel(out[i]) > waterLevel(out[j])
	})
	return out, nil
}
func waterItem(r WaterRow) Item {
	level := waterLevel(r)
	labels := map[int]string{1: "ปกติ", 2: "เฝ้าระวัง", 3: "เตือนภัย", 4: "วิกฤต", 5: "ล้นตลิ่ง"}
	return Item{ID: strconv.Itoa(r.Station.ID) + ":" + strconv.Itoa(level), Title: r.Station.Name.TH + " · " + r.Geo.Province.TH, Summary: fmt.Sprintf("สถานะสถานี: %s · %s ม.รทก. · ตรวจวัด %s น.", labels[level], *r.MSL, r.Time), URL: "https://www.thaiwater.net/water/wl", Source: "ThaiWater / สสน."}
}
func fetchDisasters(ctx context.Context, client *http.Client, c Config, now time.Time) ([]Item, error) {
	data, e := get(ctx, client, c.GDACSURL)
	if e != nil {
		return nil, e
	}
	_, _, rows, e := parseFeed(data)
	if e != nil {
		return nil, e
	}
	var out []Item
	for _, r := range rows {
		if r.ISO != "THA" || (r.Level != "Orange" && r.Level != "Red") {
			continue
		}
		published := parseTime(r.Modified)
		if published.IsZero() {
			published = parseTime(r.Date)
		}
		if !fresh(published, now, 48*time.Hour) {
			continue
		}
		link := safeURL(r.Link)
		if link == "" {
			continue
		}
		out = append(out, Item{ID: r.GUID + ":" + r.Level, Title: text(r.Title), Summary: short(text(r.Description), 220), URL: link, Source: "GDACS", Published: published})
	}
	return out, nil
}
func fetchWarnings(ctx context.Context, client *http.Client, c Config, now time.Time) ([]Item, error) {
	data, e := get(ctx, client, c.WarningURL)
	if e != nil {
		return nil, e
	}
	_, items, _, e := parseFeed(data)
	if e != nil {
		return nil, e
	}
	var out []Item
	for _, i := range items {
		if !fresh(i.Published, now, 48*time.Hour) {
			continue
		}
		for _, area := range c.AreaKeywords {
			if strings.Contains(strings.ToLower(i.Title+" "+i.Summary), strings.ToLower(area)) {
				out = append(out, i)
				break
			}
		}
	}
	return out, nil
}

type Translation struct {
	Locale  string `json:"locale"`
	Content string `json:"content"`
}
type RiotEvent struct {
	ID       int           `json:"id"`
	Titles   []Translation `json:"titles"`
	Status   string        `json:"maintenance_status"`
	Severity string        `json:"incident_severity"`
	Updates  []struct {
		ID           int           `json:"id"`
		Publish      bool          `json:"publish"`
		Created      string        `json:"created_at"`
		Updated      string        `json:"updated_at"`
		Translations []Translation `json:"translations"`
	} `json:"updates"`
}

func translated(rows []Translation) string {
	for _, locale := range []string{"th_TH", "en_US"} {
		for _, r := range rows {
			if r.Locale == locale {
				return text(r.Content)
			}
		}
	}
	return ""
}
func fetchRiot(ctx context.Context, client *http.Client, c Config) (map[string]Item, error) {
	data, e := get(ctx, client, c.RiotURL)
	if e != nil {
		return nil, e
	}
	var root struct {
		ID           string      `json:"id"`
		Incidents    []RiotEvent `json:"incidents"`
		Maintenances []RiotEvent `json:"maintenances"`
	}
	if json.Unmarshal(data, &root) != nil || root.ID != "AP" {
		return nil, fmt.Errorf("invalid Riot Asia Pacific status")
	}
	out := map[string]Item{}
	for _, r := range append(root.Incidents, root.Maintenances...) {
		latestID := 0
		summary := ""
		var published time.Time
		for _, u := range r.Updates {
			if !u.Publish {
				continue
			}
			if u.ID >= latestID {
				latestID = u.ID
				summary = translated(u.Translations)
				published = parseTime(u.Updated)
				if published.IsZero() {
					published = parseTime(u.Created)
				}
			}
		}
		if latestID == 0 {
			continue
		}
		id := strconv.Itoa(r.ID)
		out[id] = Item{ID: fmt.Sprintf("%d:%d:%s", r.ID, latestID, r.Status), Title: translated(r.Titles), Summary: summary, URL: "https://status.riotgames.com/valorant?region=ap&locale=th_TH", Source: "Riot Games · Asia Pacific", Published: published}
	}
	return out, nil
}
