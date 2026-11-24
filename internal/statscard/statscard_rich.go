// Package statscard — text rendering for the card.
//
// Keeps the x/image pipeline for Latin text (stable and fast), falls back to
// Noto Sinhala / STIX Math one glyph at a time for scripts DejaVu lacks, and
// composites colour emoji from Noto's CBDT table.
package statscard

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"sync"

	"github.com/gogpu/gg/text"
	"github.com/gogpu/gg/text/emoji"
	"github.com/gr1ksdev/UnoKamikaze/assets"
	xdraw "golang.org/x/image/draw"
	xfont "golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

var (
	emojiMu   sync.Mutex
	emojiFace *text.FontSource
	cbdt      *emoji.CBDTExtractor
)

func sfntTable(data []byte, tag string) []byte {
	n := int(binary.BigEndian.Uint16(data[4:6]))
	for i := 0; i < n; i++ {
		o := 12 + i*16
		if o+16 > len(data) {
			break
		}
		if string(data[o:o+4]) == tag {
			off := int(binary.BigEndian.Uint32(data[o+8 : o+12]))
			l := int(binary.BigEndian.Uint32(data[o+12 : o+16]))
			if off+l <= len(data) {
				return data[off : off+l]
			}
		}
	}
	return nil
}

func isRichEmoji(r rune) bool { return r >= 0x1F000 && r <= 0x1FAFF }

// baseHasGlyph reports whether the original DejaVu face can draw r.
func baseHasGlyph(r rune) bool {
	f, err := loadFont()
	if err != nil {
		return false
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: 48, DPI: 72})
	if err != nil {
		return false
	}
	defer face.Close()
	_, _, ok := face.GlyphBounds(r)
	return ok
}

// fallbackFont picks a bundled font for glyphs DejaVu lacks.
func fallbackFont(r rune) []byte {
	if r >= 0x0D80 && r <= 0x0DFF { // Sinhala
		return assets.SinhalaFont
	}
	if r >= 0x1D400 && r <= 0x1D7FF { // Mathematical alphanumerics
		return assets.MathFont
	}
	return nil
}

// richAdvance returns the horizontal advance of one rune at the given size.
func richAdvance(r rune, size float64) float64 {
	if isRichEmoji(r) {
		return size
	}
	face, err := newFace(size)
	if err == nil {
		if _, advance, ok := face.GlyphBounds(r); ok {
			defer face.Close()
			return float64(advance.Round())
		}
		face.Close()
	}
	data := fallbackFont(r)
	if data == nil {
		return size * 0.75
	}
	src, err := text.NewFontSource(data)
	if err != nil {
		return size * 0.75
	}
	return src.Face(size).Advance(string(r))
}

// measureRich measures a whole string, treating each complete emoji sequence
// (flags, ZWJ families, skin tones) as one glyph.
func measureRich(s string, size float64) float64 {
	total := 0.0
	for _, run := range emoji.Segment(s) {
		if run.IsEmoji {
			total += size
			continue
		}
		for _, r := range run.Text {
			total += richAdvance(r, size)
		}
	}
	return total
}

func fitRich(s string, size float64, maxWidth int) string {
	if measureRich(s, size) <= float64(maxWidth) {
		return s
	}
	runes := []rune(s)
	for len(runes) > 0 {
		runes = runes[:len(runes)-1]
		candidate := string(runes) + "…"
		if measureRich(candidate, size) <= float64(maxWidth) {
			return candidate
		}
	}
	return ""
}

// drawFallbackGlyph draws one glyph with gg using a fresh FontSource, avoiding
// gg's cross-font glyph-cache interference.
func drawFallbackGlyph(canvas *image.RGBA, r rune, x, baseline, size float64) {
	data := fallbackFont(r)
	if data == nil {
		return
	}
	src, err := text.NewFontSource(data)
	if err != nil {
		return
	}
	text.Draw(canvas, string(r), src.Face(size), x, baseline, textColor)
}

// drawRichEmoji composites the CBDT bitmap for one complete emoji sequence.
// Shaping the whole sequence gives the single ligature glyph for flags, ZWJ
// families and skin-tone variants instead of drawing their components apart.
func drawRichEmoji(canvas *image.RGBA, sequence string, x, baseline, size float64) {
	emojiMu.Lock()
	defer emojiMu.Unlock()
	if cbdt == nil {
		emojiFace, _ = text.NewFontSource(assets.ColorEmojiFont)
		if emojiFace == nil {
			return
		}
		cbdt, _ = emoji.NewCBDTExtractor(
			sfntTable(assets.ColorEmojiFont, "CBDT"),
			sfntTable(assets.ColorEmojiFont, "CBLC"),
		)
	}
	if cbdt == nil {
		return
	}
	shaped := text.Shape(sequence, emojiFace.Face(size))
	if len(shaped) == 0 {
		return
	}
	gid := uint16(shaped[0].GID)
	bitmap, err := cbdt.GetGlyph(gid, uint16(size))
	if err != nil {
		return
	}
	img, err := bitmap.Decode()
	if err != nil {
		return
	}
	scale := float64(size) / float64(max(bitmap.PPEM, 1))
	w := max(1, int(float64(img.Bounds().Dx())*scale))
	h := max(1, int(float64(img.Bounds().Dy())*scale))
	target := image.Rect(int(x), int(baseline)-h+int(size/5), int(x)+w, int(baseline)-h+int(size/5)+h)
	scaled := image.NewRGBA(target)
	xdraw.CatmullRom.Scale(scaled, target, img, img.Bounds(), draw.Over, nil)
	draw.Draw(canvas, target, scaled, target.Min, draw.Over)
}

// drawRich draws s centred in box.
func drawRich(canvas *image.RGBA, box image.Rectangle, s string, size float64) {
	total := measureRich(s, size)
	x := float64(box.Min.X) + float64(box.Dx())/2 - total/2
	baseline := float64(box.Min.Y) + float64(box.Dy()+int(size))/2

	base, err := newFace(size)
	if err != nil {
		return
	}
	defer base.Close()
	dr := &xfont.Drawer{Dst: canvas, Src: image.NewUniform(textColor), Face: base}
	for _, run := range emoji.Segment(s) {
		if run.IsEmoji {
			drawRichEmoji(canvas, run.Text, x, baseline, size)
			x += size
			continue
		}
		for _, r := range run.Text {
			_, advance, ok := base.GlyphBounds(r)
			if ok {
				dr.Dot = fixed.P(int(x), int(baseline))
				dr.DrawString(string(r))
				x += float64(advance.Round())
				continue
			}
			drawFallbackGlyph(canvas, r, x, baseline, size)
			x += richAdvance(r, size)
		}
	}
}

// drawRichAvatar draws initials using the same fallback chain.
func drawRichAvatar(canvas *image.RGBA, name string) error {
	circle := avatarCircle()
	filled := image.NewRGBA(circle)
	draw.Draw(filled, circle, &image.Uniform{C: initialFill}, image.Point{}, draw.Src)
	draw.DrawMask(canvas, circle, filled, circle.Min, avatarMask(), circle.Min, draw.Over)
	initials := nameInitials(name)
	if initials == "" {
		initials = "?"
	}
	size := float64(min(circle.Dx(), circle.Dy())) * 0.42
	drawRich(canvas, circle, initials, size)
	return nil
}

// Render draws the data onto a copy of the template and returns JPEG bytes.
func Render(d Data) ([]byte, error) {
	base, err := baseTemplate()
	if err != nil {
		return nil, err
	}
	canvas := canvasPool.Get().(*image.RGBA)
	defer canvasPool.Put(canvas)
	draw.Draw(canvas, canvas.Bounds(), base, base.Bounds().Min, draw.Src)

	if len(d.Avatar) > 0 {
		if err := drawAvatar(canvas, d.Avatar); err != nil {
			// A corrupt or unsupported photo falls back to rich initials.
			if err := drawRichAvatar(canvas, d.Name); err != nil {
				return nil, err
			}
		}
	} else if err := drawRichAvatar(canvas, d.Name); err != nil {
		return nil, err
	}

	nameSize := 58 * fontScale
	drawRich(canvas, nameBox, fitRich(d.Name, nameSize, nameBox.Dx()), nameSize)
	if d.HasHistory {
		statSize := 96 * fontScale
		for _, item := range []struct {
			box image.Rectangle
			n   int64
		}{{gamesBox, d.Games}, {winsBox, d.Wins}} {
			area := image.Rect(item.box.Min.X+item.box.Dx()/3, item.box.Min.Y, item.box.Max.X-12, item.box.Max.Y)
			drawRich(canvas, area, fmt.Sprintf("%d", item.n), statSize)
		}
	}

	var out bytes.Buffer
	if err := jpeg.Encode(&out, canvas, &jpeg.Options{Quality: outputQuality}); err != nil {
		return nil, fmt.Errorf("statscard: encode jpeg: %w", err)
	}
	return out.Bytes(), nil
}
