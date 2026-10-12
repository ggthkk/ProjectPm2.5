package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Snapshot struct {
	Valorant, Games, News, Disasters, Warnings []Item
	Water                                      []WaterRow
	Riot                                       map[string]Item
	Errors                                     []error
	Good                                       map[string]bool
}
type App struct {
	cfg          Config
	client       *http.Client
	state        *State
	dry          bool
	lastPoll     time.Time
	lastDigest   time.Time
	lastSnapshot Snapshot
}
type Plan struct {
	Hook, ID string
	Embeds   []Embed
	Keys     []string
	Ack      func()
	Alert    bool
}

func newApp(c Config, dry bool) (*App, error) {
	s := emptyState()
	var err error
	if !dry {
		s, err = loadState(c.DataDir)
		if err != nil {
			return nil, err
		}
	}
	return &App{cfg: c, client: &http.Client{Timeout: 25 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 5 {
			return fmt.Errorf("too many redirects")
		}
		if req.URL.Scheme != "https" {
			return fmt.Errorf("refusing insecure redirect")
		}
		return nil
	}}, state: s, dry: dry}, nil
}
func hash(s string) string { b := sha256.Sum256([]byte(s)); return hex.EncodeToString(b[:12]) }
func (a *App) collect(ctx context.Context, now time.Time) Snapshot {
	c := a.cfg
	s := Snapshot{Good: map[string]bool{}}
	record := func(name string, err error) {
		if err != nil {
			s.Errors = append(s.Errors, fmt.Errorf("%s: %w", name, err))
		} else {
			s.Good[name] = true
		}
	}
	var e error
	if c.Valorant {
		s.Valorant, e = fetchValorant(ctx, a.client, c)
		record("valorant", e)
	}
	if c.Games {
		s.Games, e = fetchGames(ctx, a.client, c)
		record("games", e)
	}
	if c.News {
		s.News, e = fetchNews(ctx, a.client, c, now)
		record("news", e)
	}
	if c.Water {
		s.Water, e = fetchWater(ctx, a.client, c, now)
		record("water", e)
	}
	if c.Disasters {
		s.Disasters, e = fetchDisasters(ctx, a.client, c, now)
		record("gdacs", e)
		s.Warnings, e = fetchWarnings(ctx, a.client, c, now)
		record("tmd", e)
	}
	if c.Riot {
		s.Riot, e = fetchRiot(ctx, a.client, c)
		record("riot", e)
	}
	log.Printf("poll: valorant=%d giveaways=%d news=%d water_stations=%d gdacs_alerts=%d riot_notices=%d errors=%d", len(s.Valorant), len(s.Games), len(s.News), len(s.Water), len(s.Disasters), len(s.Riot), len(s.Errors))
	return s
}
func (a *App) check(ctx context.Context) error {
	now := time.Now()
	s := a.collect(ctx, now)
	log.Printf("source check: valorant=%d games=%d news=%d water_stations=%d disaster_alerts=%d tmd_warnings=%d riot_notices=%d", len(s.Valorant), len(s.Games), len(s.News), len(s.Water), len(s.Disasters), len(s.Warnings), len(s.Riot))
	if a.cfg.Air {
		e, err := fetchAir(ctx, a.client, a.cfg, now)
		if err != nil {
			s.Errors = append(s.Errors, err)
		} else {
			log.Print(e.Description)
		}
	}
	for _, rows := range [][]Item{s.Valorant, s.Games} {
		for i, item := range rows {
			if i == 3 {
				break
			}
			log.Printf("source item: %s | %s", item.Source, item.Title)
		}
	}
	return errors.Join(s.Errors...)
}
func (a *App) unseen(key string) bool {
	if _, ok := a.state.Seen[key]; ok {
		return false
	}
	for _, d := range a.state.Deliveries {
		if d.Status == "pending" {
			for _, k := range d.Keys {
				if k == key {
					return false
				}
			}
		}
	}
	return true
}
func (a *App) items(kind string, rows []Item, limit int, now time.Time, age time.Duration) ([]Item, []string) {
	var out []Item
	var keys []string
	titleSeen := map[string]bool{}
	for _, i := range rows {
		key := kind + ":" + i.ID
		title := strings.ToLower(text(i.Title))
		if !a.unseen(key) || titleSeen[title] || i.Title == "" {
			continue
		}
		if age > 0 && !fresh(i.Published, now, age) {
			continue
		}
		titleSeen[title] = true
		out = append(out, i)
		keys = append(keys, key)
		if len(out) >= limit {
			break
		}
	}
	return out, keys
}
func itemEmbed(title string, items []Item, color, budget int) Embed {
	var lines []string
	for _, i := range items {
		line := "[" + short(safeText(i.Title), 100) + "](" + i.URL + ")"
		if i.Summary != "" {
			remaining := budget/len(items) - len([]rune(line)) - len([]rune(i.Source)) - 6
			if remaining > 20 {
				line += "\n" + short(safeText(i.Summary), min(160, remaining))
			}
		}
		line += "\n" + safeText(i.Source)
		lines = append(lines, line)
	}
	return Embed{Title: title, Description: strings.Join(lines, "\n\n"), Color: color}
}
func (a *App) daily(ctx context.Context, s Snapshot, now time.Time) []Plan {
	c := a.cfg
	day := now.In(c.Location).Format("2006-01-02")
	groups := map[string]*Plan{}
	add := func(hook string, e Embed, keys []string) {
		if e.Description == "" {
			return
		}
		p := groups[hook]
		if p == nil {
			p = &Plan{Hook: hook, ID: "digest:" + day + ":" + hash(hook)}
			groups[hook] = p
		}
		p.Embeds = append(p.Embeds, e)
		p.Keys = append(p.Keys, keys...)
	}
	_, airSent := a.state.Deliveries["digest:"+day+":"+hash(c.WeatherWebhook)]
	if c.Air && !airSent {
		e, err := fetchAir(ctx, a.client, c, now)
		if err != nil {
			log.Printf("air: %s", err)
		} else {
			add(c.WeatherWebhook, e, nil)
		}
	}
	if c.News {
		rows, keys := a.items("news", s.News, 3, now, 36*time.Hour)
		if len(rows) > 0 {
			add(c.NewsWebhook, itemEmbed("📰 ข่าวที่คัดมา · "+day, rows, 0x5865F2, 1100), keys)
		}
	}
	if c.Games {
		rows, keys := a.items("games", s.Games, 3, now, 0)
		if len(rows) > 0 {
			e := itemEmbed("🎁 เกมเต็มแจกฟรี", rows, 0x57F287, 1050)
			e.Description += "\n\nข้อมูลจาก [GamerPower](https://www.gamerpower.com/) · ตรวจเงื่อนไข/ภูมิภาคก่อนรับ"
			add(c.GamesWebhook, e, keys)
		}
	}
	if c.Valorant {
		rows, keys := a.items("valorant", s.Valorant, 2, now, 7*24*time.Hour)
		if len(rows) > 0 {
			add(c.GamesWebhook, itemEmbed("🎮 ข่าวใหม่ VALORANT", rows, 0xFF4655, 800), keys)
		}
	}
	if c.Water {
		var items []Item
		for _, r := range s.Water {
			if waterLevel(r) >= 3 {
				items = append(items, waterItem(r))
			}
			if len(items) == 3 {
				break
			}
		}
		if len(items) > 0 {
			e := itemEmbed("🌊 สถานีเฝ้าระวังน้ำในพื้นที่ที่เลือก", items, 0xFEE75C, 800)
			e.Description += "\n\nสถานะสถานีไม่ใช่การยืนยันน้ำท่วมถนนหรือบ้าน · พื้นที่: " + strings.Join(c.Provinces, ", ")
			add(c.AlertsWebhook, e, nil)
		}
	}
	var plans []Plan
	for _, p := range groups {
		plans = append(plans, *p)
	}
	sort.Slice(plans, func(i, j int) bool { return plans[i].ID < plans[j].ID })
	return plans
}
func (a *App) alerts(s Snapshot, now time.Time) []Plan {
	c := a.cfg
	var plans []Plan
	add := func(hook, kind, title string, rows []Item, age time.Duration) {
		items, keys := a.items(kind, rows, 3, now, age)
		if len(items) == 0 {
			return
		}
		sort.Strings(keys)
		plans = append(plans, Plan{Hook: hook, ID: "alert:" + kind + ":" + hash(strings.Join(keys, "|")), Keys: keys, Embeds: []Embed{itemEmbed(title, items, 0xFEE75C, 1700)}, Alert: true})
	}
	add(c.GamesWebhook, "valorant", "🎮 ข่าวใหม่ VALORANT", s.Valorant, 7*24*time.Hour)
	add(c.AlertsWebhook, "gdacs", "🚨 ประกาศภัยพิบัติประเทศไทย · GDACS", s.Disasters, 48*time.Hour)
	add(c.AlertsWebhook, "tmd", "⛈ ประกาศกรมอุตุนิยมวิทยา", s.Warnings, 48*time.Hour)
	var waterItems []Item
	var selected []WaterRow
	for _, r := range s.Water {
		id := strconv.Itoa(r.Station.ID)
		level := waterLevel(r)
		old, known := a.state.Water[id]
		if level < 3 {
			a.state.Water[id] = level
			continue
		}
		if level <= old && known {
			continue
		}
		if now.Sub(a.state.WaterNotified[id]) < 12*time.Hour && level <= old {
			continue
		}
		waterItems = append(waterItems, waterItem(r))
		selected = append(selected, r)
		if len(waterItems) == 3 {
			break
		}
	}
	if len(waterItems) > 0 {
		var ids []string
		for _, i := range waterItems {
			ids = append(ids, i.ID)
		}
		e := itemEmbed("🌊 สถานะสถานีระดับน้ำเปลี่ยน", waterItems, 0xFEE75C, 1500)
		e.Description += "\n\nเป็นสถานะ ณ สถานีตรวจวัด ไม่ใช่การยืนยันน้ำท่วมถนนหรือบ้าน"
		plans = append(plans, Plan{Hook: c.AlertsWebhook, ID: "water:" + now.In(c.Location).Format("2006-01-02") + ":" + hash(strings.Join(ids, "|")), Embeds: []Embed{e}, Alert: true, Ack: func() {
			for _, r := range selected {
				id := strconv.Itoa(r.Station.ID)
				a.state.Water[id] = waterLevel(r)
				a.state.WaterNotified[id] = now
			}
		}})
	}
	if s.Good["riot"] {
		var items []Item
		var keys []string
		next := map[string]string{}
		for id, i := range s.Riot {
			next[id] = i.ID
			if a.state.Riot[id] != i.ID {
				items = append(items, i)
				keys = append(keys, "riot:"+i.ID)
			}
		}
		for id := range a.state.Riot {
			if _, ok := next[id]; !ok {
				items = append(items, Item{ID: id + ":closed", Title: "ประกาศสถานะก่อนหน้าถูกปิดหรือถอนจากหน้าสถานะ", Summary: "ตรวจสอบหน้าสถานะ Riot เพื่อยืนยันบริการก่อนเข้าเล่น", URL: "https://status.riotgames.com/valorant?region=ap&locale=th_TH", Source: "Riot Games · Asia Pacific"})
				keys = append(keys, "riot:"+id+":closed")
			}
		}
		sort.Strings(keys)
		sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
		if len(items) > 0 {
			if len(items) > 3 {
				items = items[:3]
			}
			plans = append(plans, Plan{Hook: c.GamesWebhook, ID: "riot:" + hash(strings.Join(keys, "|")), Keys: keys, Embeds: []Embed{itemEmbed("🛠 ประกาศสถานะ VALORANT · Asia Pacific", items, 0xFF4655, 1700)}, Alert: true, Ack: func() { a.state.Riot = next }})
		}
	}
	return plans
}
func (a *App) deliver(ctx context.Context, p Plan, now time.Time) error {
	if p.Hook == "" && !a.dry {
		return fmt.Errorf("missing Discord webhook for outgoing message")
	}
	if _, exists := a.state.Deliveries[p.ID]; exists {
		return nil
	}
	if a.dry {
		data, _ := json.MarshalIndent(p.Embeds, "", "  ")
		log.Printf("PREVIEW %s\n%s", p.ID, data)
		return nil
	}
	if p.Alert && a.state.AlertCount >= a.cfg.MaxAlerts {
		return nil
	}
	a.state.Deliveries[p.ID] = Delivery{Status: "pending", At: now, Keys: p.Keys}
	if p.Alert {
		a.state.AlertCount++
	}
	if err := a.state.save(a.cfg.DataDir); err != nil {
		return fmt.Errorf("persist delivery before send: %w", err)
	}
	receipt, accepted, err := send(ctx, a.client, p.Hook, p.Embeds)
	if err == nil && !accepted {
		delete(a.state.Deliveries, p.ID)
		if p.Alert {
			a.state.AlertCount--
		}
		if e := a.state.save(a.cfg.DataDir); e != nil {
			return e
		}
		return fmt.Errorf("Discord rejected message (%s); retry next poll", receipt)
	}
	if p.Ack != nil {
		p.Ack()
	}
	if err != nil {
		if e := a.state.save(a.cfg.DataDir); e != nil {
			return e
		}
		return err
	}
	for _, key := range p.Keys {
		a.state.Seen[key] = now
	}
	a.state.Deliveries[p.ID] = Delivery{Status: "sent", At: now, Receipt: receipt, Keys: p.Keys}
	if err := a.state.save(a.cfg.DataDir); err != nil {
		return fmt.Errorf("persist receipt: %w", err)
	}
	log.Printf("Discord delivered: kind=%s receipt=%s", strings.Split(p.ID, ":")[0], receipt)
	return nil
}
func (a *App) tick(ctx context.Context, now time.Time, force bool) error {
	c := a.cfg
	local := now.In(c.Location)
	day := local.Format("2006-01-02")
	if a.state.Day != day {
		a.state.Day = day
		a.state.AlertCount = 0
	}
	digestAt, _ := time.ParseInLocation("2006-01-02 15:04", day+" "+c.DigestTime, c.Location)
	due := force || (!now.Before(digestAt) && now.Before(digestAt.Add(2*time.Hour)))
	if !force && !a.lastDigest.IsZero() && now.Sub(a.lastDigest) < c.Poll {
		due = false
	}
	// Minute heartbeat and schedule; expensive sources are polled only at POLL_INTERVAL.
	if !force && !a.lastPoll.IsZero() && now.Sub(a.lastPoll) < c.Poll && !due {
		return a.heartbeat()
	}
	if due && !force && !a.lastPoll.IsZero() && now.Sub(a.lastPoll) < c.Poll {
		// A new day can become due between source polls; use the cached fresh snapshot.
	} else {
		a.lastSnapshot = a.collect(ctx, now)
		a.lastPoll = now
	}
	s := a.lastSnapshot
	for _, e := range s.Errors {
		log.Print(e)
	}
	// Establish a baseline of existing news; installing the bot must not dump history.
	if s.Good["valorant"] && !a.state.Initialized["valorant"] {
		for _, i := range s.Valorant {
			a.state.Seen["valorant:"+i.ID] = now
		}
		a.state.Initialized["valorant"] = true
		if a.dry {
			a.state.Seen = map[string]time.Time{}
		}
	}
	var errs []error
	if due {
		a.lastDigest = now
		for _, p := range a.daily(ctx, s, now) {
			if err := a.deliver(ctx, p, now); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if !quiet(c, now) {
		plans := a.alerts(s, now)
		sort.SliceStable(plans, func(i, j int) bool {
			priority := func(p Plan) int {
				if strings.HasPrefix(p.ID, "riot:") {
					return 0
				}
				if strings.HasPrefix(p.ID, "alert:gdacs:") || strings.HasPrefix(p.ID, "alert:tmd:") {
					return 1
				}
				return 2
			}
			return priority(plans[i]) < priority(plans[j])
		})
		for _, p := range plans {
			if err := a.deliver(ctx, p, now); err != nil {
				errs = append(errs, err)
			}
		}
	}
	a.state.prune(now)
	if !a.dry {
		if err := a.state.save(c.DataDir); err != nil {
			return err
		}
	}
	if err := a.heartbeat(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}
func (a *App) heartbeat() error {
	if a.dry {
		return nil
	}
	if err := os.MkdirAll(a.cfg.DataDir, 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(a.cfg.DataDir, "heartbeat"), []byte(time.Now().UTC().Format(time.RFC3339)), 0600)
}
