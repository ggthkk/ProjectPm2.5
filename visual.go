package main

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg"
	"image/png"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/go-text/render"
	"github.com/go-text/typesetting/di"
	"github.com/go-text/typesetting/font"
	"github.com/go-text/typesetting/shaping"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/math/fixed"
	_ "golang.org/x/image/webp"
	htmlparser "golang.org/x/net/html"
)

//go:embed assets/fonts/Sarabun-Regular.ttf
var cardFont []byte

var markdownLink = regexp.MustCompile(`\[([^\]]+)\]\((https?://[^)]+)\)`)
var boldValue = regexp.MustCompile(`\*\*([^*]+)\*\*`)

func visualText(s string) string {
	s = markdownLink.ReplaceAllString(s, "$1")
	s = strings.ReplaceAll(s, "**", "")
	s = strings.NewReplacer("＠", "@", "［", "[", "］", "]", "＊", "*").Replace(s)
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if (unicode.Is(unicode.So, r) && r != '°') || r == '\ufe0f' {
			return -1
		}
		return r
	}, s))
}

func articleArtworkURL(ctx context.Context, raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() != "sheapgamer.com" {
		return ""
	}
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if r.URL.Scheme != "https" || r.URL.Hostname() != "sheapgamer.com" || len(via) > 2 {
			return fmt.Errorf("article redirect refused")
		}
		return nil
	}}
	b, err := get(ctx, client, raw)
	if err != nil {
		return ""
	}
	tok := htmlparser.NewTokenizer(bytes.NewReader(b))
	for {
		t := tok.Next()
		if t == htmlparser.ErrorToken {
			return ""
		}
		if t != htmlparser.StartTagToken && t != htmlparser.SelfClosingTagToken {
			continue
		}
		tag := tok.Token()
		if tag.Data != "meta" {
			continue
		}
		property, content := "", ""
		for _, a := range tag.Attr {
			if a.Key == "property" {
				property = a.Val
			}
			if a.Key == "content" {
				content = a.Val
			}
		}
		if property == "og:image" {
			return safeURL(content)
		}
	}
}

type singleCardFont struct{ face *font.Face }

func (f singleCardFont) ResolveFace(rune) *font.Face { return f.face }

type cardCanvas struct {
	img  *image.RGBA
	face *font.Face
	pen  render.Renderer
}

func (c *cardCanvas) paragraph(s string, x, y, width, size, maxLines int, ink color.Color) int {
	runes := []rune(visualText(s))
	if len(runes) == 0 {
		return y
	}
	in := shaping.Input{Text: runes, RunEnd: len(runes), Face: c.face, Size: fixed.I(size), Direction: di.DirectionLTR}
	var segmenter shaping.Segmenter
	var shaper shaping.HarfbuzzShaper
	var outputs []shaping.Output
	for _, part := range segmenter.Split(in, singleCardFont{c.face}) {
		outputs = append(outputs, shaper.Shape(part))
	}
	var wrapper shaping.LineWrapper
	ellipsis := in
	ellipsis.Text = []rune("…")
	ellipsis.RunStart = 0
	ellipsis.RunEnd = 1
	cfg := shaping.WrapConfig{Direction: di.DirectionLTR, TruncateAfterLines: maxLines}.WithTruncator(&shaper, ellipsis)
	lines, _ := wrapper.WrapParagraph(cfg, width, runes, shaping.NewSliceIterator(outputs))
	c.pen.FontSize = float32(size)
	c.pen.Color = ink
	for _, line := range lines {
		px := x
		for _, run := range line {
			px = c.pen.DrawShapedRunAt(run, c.img, px, y+size)
		}
		y += size * 3 / 2
	}
	return y
}
func (c *cardCanvas) box(x, y, w, h int, ink color.Color) {
	draw.Draw(c.img, image.Rect(x, y, x+w, y+h), image.NewUniform(ink), image.Point{}, draw.Src)
}

// Only fetch known publishers' public artwork, with bounded decoding. Failure is
// cosmetic; never prevent the report or follow a redirect to an arbitrary host.
func cardArtwork(ctx context.Context, raw string) image.Image {
	allowed := func(raw string) bool {
		u, e := url.Parse(raw)
		if e != nil || u.Scheme != "https" || u.User != nil {
			return false
		}
		return u.Hostname() == "i.kym-cdn.com" || u.Hostname() == "www.gamerpower.com" || u.Hostname() == "sheapgamer.com"
	}
	if !allowed(raw) {
		return nil
	}
	client := &http.Client{Timeout: 5e9, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) > 2 || !allowed(r.URL.String()) {
			return fmt.Errorf("artwork redirect refused")
		}
		return nil
	}}
	b, e := get(ctx, client, raw)
	if e != nil {
		return nil
	}
	cfg, _, e := image.DecodeConfig(bytes.NewReader(b))
	if e != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 4096 || cfg.Height > 4096 || cfg.Width*cfg.Height > 4000000 {
		return nil
	}
	im, _, _ := image.Decode(bytes.NewReader(b))
	return im
}

func renderCard(e Embed, artwork image.Image) ([]byte, error) {
	face, err := font.ParseTTF(bytes.NewReader(cardFont))
	if err != nil {
		return nil, err
	}
	c := cardCanvas{img: image.NewRGBA(image.Rect(0, 0, 1000, 1800)), face: face}
	bg := color.RGBA{19, 24, 35, 255}
	white := color.RGBA{240, 243, 249, 255}
	muted := color.RGBA{166, 179, 199, 255}
	accent := color.RGBA{uint8(e.Color >> 16), uint8(e.Color >> 8), uint8(e.Color), 255}
	textAccent := color.RGBA{max(accent.R, 140), max(accent.G, 155), max(accent.B, 180), 255}
	c.box(0, 0, 1000, 1800, bg)
	c.box(0, 0, 1000, 12, accent)
	c.paragraph("DAILY / เรื่องน่ารู้วันนี้", 48, 36, 900, 25, 1, muted)
	y := 100
	if artwork != nil {
		// Fit, not crop: meme captions and game artwork must remain intact.
		bounds := artwork.Bounds()
		w, h := bounds.Dx(), bounds.Dy()
		scale := min(300.0/float64(w), 320.0/float64(h))
		w, h = int(float64(w)*scale), int(float64(h)*scale)
		xdraw.CatmullRom.Scale(c.img, image.Rect(48+(300-w)/2, y, 48+(300+w)/2, y+h), artwork, bounds, draw.Over, nil)
		bottom := c.paragraph(e.Title, 380, y, 572, 40, 5, white)
		y = max(y+h, bottom) + 28
	} else {
		y = c.paragraph(e.Title, 48, y, 904, 46, 4, white) + 20
	}
	if e.Description != "" {
		y = c.paragraph(e.Description, 48, y, 904, 36, 3, muted) + 24
	}
	for i := 0; i < len(e.Fields); i++ {
		f := e.Fields[i]
		if i == 0 && len(e.Fields) > 1 && f.Inline && e.Fields[1].Inline && strings.Contains(f.Name, "AQI") {
			for col, metric := range e.Fields[:2] {
				x := 48 + col*464
				c.box(x, y, 440, 230, color.RGBA{31, 39, 54, 255})
				c.paragraph(metric.Name, x+24, y+18, 392, 27, 1, muted)
				value := boldValue.FindStringSubmatch(metric.Value)
				if len(value) > 1 {
					parts := strings.SplitN(value[1], " · ", 2)
					c.paragraph(parts[0], x+24, y+64, 392, 68, 1, accent)
					if len(parts) > 1 {
						c.paragraph(parts[1], x+24, y+152, 392, 30, 1, white)
					} else if lines := strings.Split(metric.Value, "\n"); len(lines) > 2 {
						c.paragraph(lines[1], x+24, y+152, 392, 30, 1, white)
					}
				}
				lines := strings.Split(metric.Value, "\n")
				if len(lines) > 1 {
					c.paragraph(lines[len(lines)-1], x+24, y+192, 392, 23, 1, muted)
				}
			}
			y += 258
			i++
			continue
		}
		if y > 1400 {
			break
		}
		c.box(48, y, 904, 2, color.RGBA{45, 55, 74, 255})
		y += 20
		y = c.paragraph(f.Name, 48, y, 904, 30, 2, textAccent) + 8
		y = c.paragraph(f.Value, 48, y, 904, 30, 4, white) + 26
	}
	c.paragraph("อ่านรายละเอียด / เปิดลิงก์ใต้ภาพ", 48, y+12, 904, 24, 1, muted)
	h := min(y+90, 1800)
	var out bytes.Buffer
	err = png.Encode(&out, c.img.SubImage(image.Rect(0, 0, 1000, h)))
	return out.Bytes(), err
}

func imageCaption(e Embed) string {
	var links []string
	if e.URL != "" {
		links = append(links, "[อ่านต้นทาง ↗]("+e.URL+")")
	}
	for _, f := range e.Fields {
		for _, m := range markdownLink.FindAllString(f.Value, -1) {
			if !strings.Contains(strings.Join(links, " "), m) {
				links = append(links, m)
			}
		}
	}
	return strings.Join(links, " · ")
}
