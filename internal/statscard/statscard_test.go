package statscard

import (
	"bytes"
	"image"
	"image/color"
	_ "image/jpeg"
	"image/png"
	"testing"

	"github.com/gr1ksdev/UnoKamikaze/assets"
)

func decode(t *testing.T, data []byte) *image.RGBA {
	t.Helper()
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode image: %v", err)
	}
	rgba := image.NewRGBA(img.Bounds())
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			rgba.Set(x, y, img.At(x, y))
		}
	}
	return rgba
}

func changedPixels(a, b *image.RGBA, rect image.Rectangle) int {
	count := 0
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			r1, g1, b1, a1 := a.At(x, y).RGBA()
			r2, g2, b2, a2 := b.At(x, y).RGBA()
			if r1 != r2 || g1 != g2 || b1 != b2 || a1 != a2 {
				count++
			}
		}
	}
	return count
}

func TestTokenChangesWithVisualData(t *testing.T) {
	base := Data{Name: "Ana", Games: 3, Wins: 1, HasHistory: true, Avatar: []byte("a")}
	token := Token(base)
	if token == "" || token != Token(base) {
		t.Fatal("token must be deterministic and non-empty")
	}
	cases := map[string]Data{
		"name":    {Name: "Bob", Games: 3, Wins: 1, HasHistory: true, Avatar: []byte("a")},
		"games":   {Name: "Ana", Games: 4, Wins: 1, HasHistory: true, Avatar: []byte("a")},
		"wins":    {Name: "Ana", Games: 3, Wins: 2, HasHistory: true, Avatar: []byte("a")},
		"history": {Name: "Ana", Games: 3, Wins: 1, HasHistory: false, Avatar: []byte("a")},
		"avatar":  {Name: "Ana", Games: 3, Wins: 1, HasHistory: true, Avatar: []byte("b")},
	}
	for name, data := range cases {
		if Token(data) == token {
			t.Errorf("token did not change for %s", name)
		}
	}
}

func BenchmarkRender(b *testing.B) {
	photo := image.NewRGBA(image.Rect(0, 0, 128, 128))
	var avatar bytes.Buffer
	if err := png.Encode(&avatar, photo); err != nil {
		b.Fatal(err)
	}
	data := Data{Name: "Freddy Kamikaze", Games: 27, Wins: 9, HasHistory: true, Avatar: avatar.Bytes()}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		card, err := Render(data)
		if err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(len(card))/1024, "KB/card")
	}
}

func brightPixels(img *image.RGBA, rect image.Rectangle) int {
	count := 0
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if r>>8 > 180 && g>>8 > 170 && b>>8 > 180 {
				count++
			}
		}
	}
	return count
}

func numberArea(box image.Rectangle) image.Rectangle {
	return image.Rect(box.Min.X+box.Dx()/2, box.Min.Y, box.Max.X-16, box.Max.Y)
}

func TestRenderProducesCardWithStats(t *testing.T) {
	got, err := Render(Data{Name: "Freddy Kamikaze", Games: 27, Wins: 9, HasHistory: true})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	card := decode(t, got)
	if card.Bounds().Dx() != width || card.Bounds().Dy() != height {
		t.Fatalf("size = %dx%d, want %dx%d", card.Bounds().Dx(), card.Bounds().Dy(), width, height)
	}
	if n := brightPixels(card, numberArea(gamesBox)); n < 100 {
		t.Errorf("games number has only %d bright pixels", n)
	}
	if n := brightPixels(card, numberArea(winsBox)); n < 50 {
		t.Errorf("wins number has only %d bright pixels", n)
	}
}

func TestRenderWithoutHistoryKeepsStatBoxesEmpty(t *testing.T) {
	got, err := Render(Data{Name: "Ana", HasHistory: false})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	card := decode(t, got)
	if n := brightPixels(card, numberArea(gamesBox)); n != 0 {
		t.Errorf("games number drawn with no history: %d pixels", n)
	}
	if n := brightPixels(card, numberArea(winsBox)); n != 0 {
		t.Errorf("wins number drawn with no history: %d pixels", n)
	}
}

func TestRenderUsesInitialsWhenAvatarIsInvalid(t *testing.T) {
	base := decode(t, assets.StatsCard)
	got, err := Render(Data{Name: "João Silva", HasHistory: true, Avatar: []byte("not an image")})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	card := decode(t, got)
	if n := changedPixels(base, card, avatarBox); n < 1000 {
		t.Errorf("avatar area has only %d changed pixels", n)
	}
}

func TestRenderUsesProfilePhoto(t *testing.T) {
	var buf bytes.Buffer
	photo := image.NewRGBA(image.Rect(0, 0, 200, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 200; x++ {
			photo.Set(x, y, color.RGBA{R: 40, G: 200, B: 90, A: 255})
		}
	}
	if err := png.Encode(&buf, photo); err != nil {
		t.Fatal(err)
	}
	got, err := Render(Data{Name: "Ana", HasHistory: false, Avatar: buf.Bytes()})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	card := decode(t, got)
	// A pixel near the middle of the circle must come from the photo.
	r, g, b, _ := card.At(250, 350).RGBA()
	close := func(got, want uint32) bool { return got+20 >= want && want+20 >= got }
	if !close(r>>8, 40) || !close(g>>8, 200) || !close(b>>8, 90) {
		t.Fatalf("avatar pixel = (%d,%d,%d), want ~ (40,200,90)", r>>8, g>>8, b>>8)
	}
}
