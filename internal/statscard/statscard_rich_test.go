package statscard

import (
	"bytes"
	"image/jpeg"
	"testing"
)

// Render must cover scripts DejaVu lacks and real colour emoji, without
// breaking the ordinary Portuguese/Latin path.
func TestRenderCoversFallbackScriptsAndEmoji(t *testing.T) {
	for _, name := range []string{
		"ටී𝒆𝒏𝒛ු",
		"Freddy 🎮 😀 🚫",
		"Ana Silva",
		"José ∅ 𝒆",
	} {
		t.Run(name, func(t *testing.T) {
			data, err := Render(Data{Name: name, Games: 12, Wins: 3, HasHistory: true})
			if err != nil {
				t.Fatal(err)
			}
			img, err := jpeg.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			if img.Bounds().Dx() != outputWidth || img.Bounds().Dy() != outputHeight {
				t.Fatalf("unexpected size: %v", img.Bounds())
			}
		})
	}
}

func TestRichAdvanceIsFiniteForEveryGlyph(t *testing.T) {
	for _, r := range []rune{'A', 'é', 'ට', '𝒆', '😀', '🚫'} {
		got := richAdvance(r, 48)
		if got <= 0 || got > 200 {
			t.Fatalf("advance for %U = %f", r, got)
		}
	}
	// Sinhala vowel signs are combining marks: zero advance is correct.
	if got := richAdvance('ී', 48); got != 0 {
		t.Fatalf("combining mark advance = %f", got)
	}
}

func TestRichTemplateVersionInvalidatesOldCache(t *testing.T) {
	if TemplateVersion != "stats-card-v4-rich" {
		t.Fatalf("unexpected template version %q", TemplateVersion)
	}
}
