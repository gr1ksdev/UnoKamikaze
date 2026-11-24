// Package statscard renders the /stats photo on top of the embedded template.
// It has no Telegram or SQL dependency: callers supply the player data and an
// optional avatar image and receive JPEG bytes.
package statscard

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png" // decode the template; jpeg also registers its decoder for avatars
	"math"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/gr1ksdev/UnoKamikaze/assets"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
)

// Canvas is the size of assets/stickers/stats/card.png.
const (
	width  = 1672
	height = 941

	// The uploaded photo is downscaled: the card is shown small in chat, and a
	// smaller JPEG cuts the Telegram upload from ~1.7s to a few hundred ms.
	outputWidth   = 1280
	outputHeight  = 720
	outputQuality = 85

	// TemplateVersion changes whenever the template, the drawing code or the
	// output size changes, so cached file_ids from an older build are not reused.
	TemplateVersion = "stats-card-v4-rich"
)

const fontScale = float64(outputWidth) / float64(width)

// templateRect converts a position from the original template into the
// downscaled canvas the renderer actually draws on.
func templateRect(r image.Rectangle) image.Rectangle {
	sx, sy := float64(outputWidth)/float64(width), float64(outputHeight)/float64(height)
	return image.Rect(
		int(float64(r.Min.X)*sx), int(float64(r.Min.Y)*sy),
		int(float64(r.Max.X)*sx), int(float64(r.Max.Y)*sy),
	)
}

// Layout follows the clean template supplied for the card, already converted
// to the output canvas size.
var (
	avatarBox   = templateRect(image.Rect(80, 130, 550, 575))
	nameBox     = templateRect(image.Rect(600, 220, 1350, 330))
	gamesBox    = templateRect(image.Rect(575, 380, 1075, 545))
	winsBox     = templateRect(image.Rect(1105, 380, 1580, 545))
	textColor   = color.RGBA{R: 245, G: 235, B: 255, A: 255}
	initialFill = color.RGBA{R: 86, G: 35, B: 150, A: 255}
)

// Data is the player information drawn on the card. Avatar is an optional
// JPEG/PNG profile photo; when absent the player's initials are used.
type Data struct {
	Name       string
	Games      int64
	Wins       int64
	HasHistory bool
	Avatar     []byte
}

// Token identifies the visual content of a card. Callers can cache the
// Telegram file_id under this token and skip rendering/upload when the player's
// name, numbers or profile photo have not changed. Length prefixes avoid
// ambiguity between concatenated fields.
func Token(d Data) string {
	h := sha256.New()
	write := func(s string) {
		fmt.Fprintf(h, "%d:%s", len(s), s)
	}
	write(TemplateVersion)
	write(d.Name)
	fmt.Fprintf(h, "%d;%d;%t;", d.Games, d.Wins, d.HasHistory)
	write(string(d.Avatar))
	return hex.EncodeToString(h.Sum(nil))
}

var (
	fontOnce sync.Once
	baseFont *opentype.Font
	fontErr  error

	templateOnce sync.Once
	templateBase *image.RGBA
	templateErr  error

	// Every render starts with a full draw over the template, so a canvas can
	// be reused safely and the allocation disappears after the first use.
	canvasPool = sync.Pool{New: func() any {
		return image.NewRGBA(image.Rect(0, 0, outputWidth, outputHeight))
	}}

	// The avatar circle is fixed by the template, so its mask is computed once.
	avatarMask = sync.OnceValue(func() *image.Alpha { return circleMask(avatarCircle()) })
)

func avatarCircle() image.Rectangle {
	side := min(avatarBox.Dx(), avatarBox.Dy())
	origin := image.Pt(
		avatarBox.Min.X+(avatarBox.Dx()-side)/2,
		avatarBox.Min.Y+(avatarBox.Dy()-side)/2,
	)
	return image.Rectangle{Min: origin, Max: origin.Add(image.Pt(side, side))}
}

// baseTemplate returns the decoded template. It is read-only: Render copies it
// into a fresh canvas instead of decoding the PNG on every call.
func baseTemplate() (*image.RGBA, error) {
	templateOnce.Do(func() {
		decoded, err := png.Decode(bytes.NewReader(assets.StatsCard))
		if err != nil {
			templateErr = fmt.Errorf("statscard: decode template: %w", err)
			return
		}
		if decoded.Bounds().Dx() != width || decoded.Bounds().Dy() != height {
			templateErr = fmt.Errorf("statscard: template is %dx%d, want %dx%d",
				decoded.Bounds().Dx(), decoded.Bounds().Dy(), width, height)
			return
		}
		base := image.NewRGBA(image.Rect(0, 0, width, height))
		draw.Draw(base, base.Bounds(), decoded, decoded.Bounds().Min, draw.Src)
		// Downscale once at startup; rendering then works directly at output size.
		small := image.NewRGBA(image.Rect(0, 0, outputWidth, outputHeight))
		xdraw.CatmullRom.Scale(small, small.Bounds(), base, base.Bounds(), draw.Src, nil)
		templateBase = small
	})
	return templateBase, templateErr
}

func loadFont() (*opentype.Font, error) {
	fontOnce.Do(func() {
		baseFont, fontErr = opentype.Parse(assets.StatsFont)
		if fontErr != nil {
			fontErr = fmt.Errorf("statscard: parse font: %w", fontErr)
		}
	})
	return baseFont, fontErr
}

func newFace(size float64) (font.Face, error) {
	f, err := loadFont()
	if err != nil {
		return nil, err
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return nil, fmt.Errorf("statscard: font face: %w", err)
	}
	return face, nil
}

// drawAvatar composites a player's profile photo into the circular slot.
// Render handles the no-photo case with rich initials.
func drawAvatar(canvas *image.RGBA, avatar []byte) error {
	circle := avatarCircle()
	src, _, err := image.Decode(bytes.NewReader(avatar))
	if err != nil {
		return err
	}
	scaled := image.NewRGBA(circle)
	xdraw.CatmullRom.Scale(scaled, circle, src, centeredSquare(src.Bounds()), draw.Over, nil)
	draw.DrawMask(canvas, circle, scaled, circle.Min, avatarMask(), circle.Min, draw.Over)
	return nil
}

func centeredSquare(b image.Rectangle) image.Rectangle {
	side := min(b.Dx(), b.Dy())
	x := b.Min.X + (b.Dx()-side)/2
	y := b.Min.Y + (b.Dy()-side)/2
	return image.Rect(x, y, x+side, y+side)
}

func circleMask(r image.Rectangle) *image.Alpha {
	mask := image.NewAlpha(r)
	side := float64(min(r.Dx(), r.Dy()))
	cx, cy := float64(r.Min.X)+side/2, float64(r.Min.Y)+side/2
	radius := side/2 - 1
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy)
			var a uint8
			switch {
			case d <= radius-1:
				a = 255
			case d < radius+1:
				a = uint8((radius + 1 - d) * 255 / 2)
			}
			mask.SetAlpha(x, y, color.Alpha{A: a})
		}
	}
	return mask
}

func nameInitials(name string) string {
	var b []rune
	for _, word := range strings.Fields(name) {
		if len(b) == 2 {
			break
		}
		r, _ := utf8.DecodeRuneInString(word)
		if r != utf8.RuneError && unicode.IsLetter(r) {
			b = append(b, unicode.ToUpper(r))
		}
	}
	if len(b) == 0 {
		return "?"
	}
	return string(b)
}
