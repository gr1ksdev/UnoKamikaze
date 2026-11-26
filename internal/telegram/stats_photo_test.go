package telegram

import (
	"bytes"
	"context"
	"errors"
	"image"
	_ "image/jpeg"
	"image/png"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/gr1ksdev/UnoKamikaze/internal/game"
	"github.com/gr1ksdev/UnoKamikaze/internal/groups"
	"github.com/gr1ksdev/UnoKamikaze/internal/ranking"
	"github.com/gr1ksdev/UnoKamikaze/internal/statscard"
	"github.com/mymmrac/telego"
)

type mockAvatarFetcher struct {
	calls  int
	data   []byte
	err    error
	userID int64
}

func (m *mockAvatarFetcher) Fetch(_ context.Context, userID int64) ([]byte, error) {
	m.calls++
	m.userID = userID
	return m.data, m.err
}

func photoPayload(t *testing.T, params telego.SendPhotoParams) []byte {
	t.Helper()
	nb, ok := params.Photo.File.(namedBytes)
	if !ok {
		t.Fatalf("photo file type = %T, want namedBytes", params.Photo.File)
	}
	if nb.Name() != "stats.jpg" {
		t.Fatalf("photo name = %s", nb.Name())
	}
	if _, err := nb.Reader.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(nb.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		t.Fatalf("photo is not a decodable image: %v", err)
	}
	return data
}

func statsHandler(t *testing.T, api *mockBotAPI, avatar AvatarFetcher, onStats func(context.Context, int64, int64, time.Time) (ranking.PlayerStats, error)) *CommandHandler {
	t.Helper()
	svc, err := game.NewService()
	if err != nil {
		t.Fatal(err)
	}
	h := NewCommandHandler(api, svc, NewRenderer(NewUserCache(100)), NewTokenStore(100, 10, time.Now, nil), "unokamikaze", nil)
	h.rankingService = &ranking.Service{Repository: &mockPrivateRankingRepo{onPlayerStats: onStats}}
	h.avatarFetcher = avatar
	return h
}

func TestStatsPrivateSendsPhotoToTheRequestingChat(t *testing.T) {
	api := newMockBotAPI()
	avatar := &mockAvatarFetcher{}
	var scope int64 = -1
	h := statsHandler(t, api, avatar, func(_ context.Context, chatID, _ int64, _ time.Time) (ranking.PlayerStats, error) {
		scope = chatID
		return ranking.PlayerStats{System: groups.Updated, Games: 3, Wins: 1, Score: 700}, nil
	})
	h.HandleMessage(t.Context(), &telego.Message{
		MessageID: 777,
		Chat:      telego.Chat{ID: 555, Type: "private"},
		From:      &telego.User{ID: 42, FirstName: "Ana"},
		Text:      "/stats",
	})
	if scope != 0 {
		t.Fatalf("stats scope = %d, want 0 (aggregate)", scope)
	}
	if len(api.SentPhotos) != 1 || api.SentPhotos[0].ChatID.ID != 555 {
		t.Fatalf("photos = %+v", api.SentPhotos)
	}
	if avatar.calls != 1 || avatar.userID != 42 {
		t.Fatalf("avatar calls = %d, user = %d", avatar.calls, avatar.userID)
	}
	photoPayload(t, api.SentPhotos[0])
	if reply := api.SentPhotos[0].ReplyParameters; reply == nil || reply.MessageID != 777 {
		t.Fatalf("photo reply = %+v, want reply to 777", reply)
	}
	if reply := api.SentMessages[0].ReplyParameters; reply == nil || reply.MessageID != 777 {
		t.Fatalf("loading reply = %+v, want reply to 777", reply)
	}
	if len(api.DeletedMessages) != 1 || api.DeletedMessages[0].MessageID != 1 {
		t.Fatalf("loading message not removed: %+v", api.DeletedMessages)
	}
	for _, want := range []string{"Suas estatísticas globais", "<blockquote>", "<b>Pontos:</b>", "7,00 pts", "Soma das partidas pontuadas em todos os grupos", "Não há posição global"} {
		if !strings.Contains(api.SentPhotos[0].Caption, want) {
			t.Fatalf("private caption missing %q: %s", want, api.SentPhotos[0].Caption)
		}
	}
	if strings.Contains(api.SentPhotos[0].Caption, "Suas estatísticas em ") {
		t.Fatalf("private caption leaked a group scope: %s", api.SentPhotos[0].Caption)
	}
}

func TestStatsPrivateFallbackShowsGlobalStats(t *testing.T) {
	api := newMockBotAPI()
	api.SendPhotoErr = errors.New("telegram: photo failed")
	h := statsHandler(t, api, nil, func(context.Context, int64, int64, time.Time) (ranking.PlayerStats, error) {
		return ranking.PlayerStats{System: groups.Updated, Games: 3, Wins: 1, Score: 700, BestPosition: 1, AvgPosition: "2.14"}, nil
	})
	h.HandleMessage(t.Context(), &telego.Message{
		MessageID: 778,
		Chat:      telego.Chat{ID: 555, Type: "private"},
		From:      &telego.User{ID: 42, FirstName: "Ana"},
		Text:      "/stats",
	})
	fallback := api.SentMessages[len(api.SentMessages)-1].Text
	for _, want := range []string{"Suas estatísticas globais", "<blockquote>", "<b>Partidas pontuadas:</b> 3", "<b>Vitórias:</b> 1", "<b>Pontos:</b>", "7,00 pts", "<b>Melhor colocação:</b> 1º", "<b>Colocação média:</b> 2.14", "Soma das partidas pontuadas em todos os grupos"} {
		if !strings.Contains(fallback, want) {
			t.Fatalf("private fallback missing %q: %s", want, fallback)
		}
	}
}

func TestStatsPrivateWithoutHistorySaysNoGames(t *testing.T) {
	api := newMockBotAPI()
	h := statsHandler(t, api, nil, func(context.Context, int64, int64, time.Time) (ranking.PlayerStats, error) {
		return ranking.PlayerStats{System: groups.Updated}, nil
	})
	h.HandleMessage(t.Context(), &telego.Message{
		Chat: telego.Chat{ID: 555, Type: "private"},
		From: &telego.User{ID: 42, FirstName: "Ana"},
		Text: "/stats",
	})
	if len(api.SentPhotos) != 1 {
		t.Fatalf("sent %d photos, want 1", len(api.SentPhotos))
	}
	caption := api.SentPhotos[0].Caption
	if !strings.Contains(caption, "Suas estatísticas globais") || !strings.Contains(caption, "Você ainda não tem partidas pontuadas em nenhum grupo") {
		t.Fatalf("unexpected private caption: %s", caption)
	}
	if strings.Contains(caption, "<blockquote>") {
		t.Fatalf("empty stats must not render a blockquote: %s", caption)
	}
}

func TestStatsWithoutHistorySendsCardWithoutNumbers(t *testing.T) {
	api := newMockBotAPI()
	h := statsHandler(t, api, nil, func(context.Context, int64, int64, time.Time) (ranking.PlayerStats, error) {
		return ranking.PlayerStats{System: groups.Updated}, nil
	})
	h.HandleMessage(t.Context(), &telego.Message{
		Chat: telego.Chat{ID: -900, Type: "group", Title: "Grupo"},
		From: &telego.User{ID: 7, FirstName: "Novo"},
		Text: "/stats@unokamikaze",
	})
	if len(api.SentPhotos) != 1 {
		t.Fatalf("sent %d photos, want 1", len(api.SentPhotos))
	}
	photoPayload(t, api.SentPhotos[0])
	if !strings.Contains(api.SentPhotos[0].Caption, "Você ainda não tem partidas pontuadas aqui") {
		t.Fatalf("unexpected caption: %s", api.SentPhotos[0].Caption)
	}
}

func TestStatsFallsBackToTextWhenPhotoFails(t *testing.T) {
	api := newMockBotAPI()
	api.SendPhotoErr = errors.New("telegram: photo failed")
	h := statsHandler(t, api, nil, func(context.Context, int64, int64, time.Time) (ranking.PlayerStats, error) {
		return ranking.PlayerStats{System: groups.Updated, Games: 2, Wins: 1, Score: 400}, nil
	})
	h.HandleMessage(t.Context(), &telego.Message{
		MessageID: 888,
		Chat:      telego.Chat{ID: -901, Type: "group", Title: "Grupo"},
		From:      &telego.User{ID: 8, FirstName: "Ana"},
		Text:      "/stats@unokamikaze",
	})
	if len(api.SentPhotos) != 1 {
		t.Fatalf("sent %d photos, want 1", len(api.SentPhotos))
	}
	if len(api.SentMessages) != 2 {
		t.Fatalf("sent %d texts, want loading + fallback", len(api.SentMessages))
	}
	if !strings.Contains(api.SentMessages[0].Text, "⏳") {
		t.Fatalf("missing loading message: %+v", api.SentMessages[0])
	}
	fallback := api.SentMessages[len(api.SentMessages)-1]
	if reply := fallback.ReplyParameters; reply == nil || reply.MessageID != 888 {
		t.Fatalf("fallback reply = %+v, want reply to 888", reply)
	}
	if !strings.Contains(fallback.Text, "Partidas pontuadas: 2") {
		t.Fatalf("unexpected fallback: %s", fallback.Text)
	}
	if len(api.DeletedMessages) != 1 || api.DeletedMessages[0].MessageID != 1 {
		t.Fatalf("loading message not removed: %+v", api.DeletedMessages)
	}
}

func TestStatsUsesProfilePhotoWhenAvailable(t *testing.T) {
	api := newMockBotAPI()
	avatar := &mockAvatarFetcher{data: validAvatarPNG(t)}
	h := statsHandler(t, api, avatar, func(context.Context, int64, int64, time.Time) (ranking.PlayerStats, error) {
		return ranking.PlayerStats{System: groups.Updated, Games: 1}, nil
	})
	h.HandleMessage(t.Context(), &telego.Message{
		Chat: telego.Chat{ID: -902, Type: "group", Title: "Grupo"},
		From: &telego.User{ID: 9, FirstName: "Bob"},
		Text: "/stats@unokamikaze",
	})
	if len(api.SentPhotos) != 1 {
		t.Fatalf("sent %d photos, want 1", len(api.SentPhotos))
	}
	if len(photoPayload(t, api.SentPhotos[0])) == 0 {
		t.Fatal("empty photo payload")
	}
}

func TestStatsCacheReusesFileID(t *testing.T) {
	api := newMockBotAPI()
	api.PhotoFileID = "file_123"
	stats := ranking.PlayerStats{System: groups.Updated, Games: 3, Wins: 1, Score: 700}
	h := statsHandler(t, api, &mockAvatarFetcher{}, func(context.Context, int64, int64, time.Time) (ranking.PlayerStats, error) {
		return stats, nil
	})
	renders := 0
	h.renderCard = func(d statscard.Data) ([]byte, error) {
		renders++
		return statscard.Render(d)
	}
	msg := &telego.Message{
		Chat: telego.Chat{ID: -910, Type: "group", Title: "Grupo"},
		From: &telego.User{ID: 10, FirstName: "Ana"},
		Text: "/stats@unokamikaze",
	}
	h.HandleMessage(t.Context(), msg)
	h.HandleMessage(t.Context(), msg)

	if renders != 1 {
		t.Fatalf("rendered %d times, want 1", renders)
	}
	if len(api.SentPhotos) != 2 {
		t.Fatalf("sent %d photos, want 2", len(api.SentPhotos))
	}
	if first := api.SentPhotos[0]; first.Photo.FileID != "" || first.Photo.File == nil {
		t.Fatalf("first send should upload bytes: %+v", first.Photo)
	}
	if second := api.SentPhotos[1]; second.Photo.FileID != "file_123" || second.Photo.File != nil {
		t.Fatalf("second send should reuse file_id: %+v", second.Photo)
	}
}

func TestStatsCacheInvalidatesOnDataChange(t *testing.T) {
	api := newMockBotAPI()
	api.PhotoFileID = "file_123"
	avatar := &mockAvatarFetcher{}
	stats := ranking.PlayerStats{System: groups.Updated, Games: 3, Wins: 1}
	h := statsHandler(t, api, avatar, func(context.Context, int64, int64, time.Time) (ranking.PlayerStats, error) {
		return stats, nil
	})
	renders := 0
	h.renderCard = func(d statscard.Data) ([]byte, error) {
		renders++
		return statscard.Render(d)
	}
	msg := &telego.Message{
		Chat: telego.Chat{ID: -911, Type: "group", Title: "Grupo"},
		From: &telego.User{ID: 11, FirstName: "Ana"},
		Text: "/stats@unokamikaze",
	}
	h.HandleMessage(t.Context(), msg)
	stats.Games = 4
	h.HandleMessage(t.Context(), msg)
	if renders != 2 {
		t.Fatalf("rendered %d times after stats change, want 2", renders)
	}

	avatar.data = validAvatarPNG(t)
	h.HandleMessage(t.Context(), msg)
	if renders != 3 {
		t.Fatalf("rendered %d times after avatar change, want 3", renders)
	}
}

func TestStatsCacheKeepsCaptionFresh(t *testing.T) {
	api := newMockBotAPI()
	api.PhotoFileID = "file_123"
	stats := ranking.PlayerStats{System: groups.Updated, Games: 3, Wins: 1, Score: 700}
	h := statsHandler(t, api, nil, func(context.Context, int64, int64, time.Time) (ranking.PlayerStats, error) {
		return stats, nil
	})
	renders := 0
	h.renderCard = func(d statscard.Data) ([]byte, error) {
		renders++
		return statscard.Render(d)
	}
	msg := &telego.Message{
		Chat: telego.Chat{ID: -912, Type: "group", Title: "Grupo"},
		From: &telego.User{ID: 12, FirstName: "Ana"},
		Text: "/stats@unokamikaze",
	}
	h.HandleMessage(t.Context(), msg)
	stats.Score = 999
	h.HandleMessage(t.Context(), msg)

	if renders != 1 {
		t.Fatalf("rendered %d times, want 1", renders)
	}
	if !strings.Contains(api.SentPhotos[1].Caption, "Pontos: 9,99 pts") {
		t.Fatalf("caption is stale: %s", api.SentPhotos[1].Caption)
	}
}

func TestStatsCacheRetriesWithFreshRenderOnInvalidFileID(t *testing.T) {
	api := newMockBotAPI()
	api.PhotoFileID = "file_123"
	h := statsHandler(t, api, nil, func(context.Context, int64, int64, time.Time) (ranking.PlayerStats, error) {
		return ranking.PlayerStats{System: groups.Updated, Games: 3, Wins: 1}, nil
	})
	renders := 0
	h.renderCard = func(d statscard.Data) ([]byte, error) {
		renders++
		return statscard.Render(d)
	}
	msg := &telego.Message{
		Chat: telego.Chat{ID: -913, Type: "group", Title: "Grupo"},
		From: &telego.User{ID: 13, FirstName: "Ana"},
		Text: "/stats@unokamikaze",
	}
	h.HandleMessage(t.Context(), msg)

	api.SendPhotoFileIDErr = errors.New("FILE_ID_INVALID")
	api.PhotoFileID = "file_456"
	h.HandleMessage(t.Context(), msg)
	if renders != 2 {
		t.Fatalf("rendered %d times, want 2", renders)
	}
	if len(api.SentPhotos) != 3 {
		t.Fatalf("sent %d photos, want 3", len(api.SentPhotos))
	}
	if last := api.SentPhotos[2]; last.Photo.FileID != "" || last.Photo.File == nil {
		t.Fatalf("retry must upload bytes: %+v", last.Photo)
	}

	api.SendPhotoFileIDErr = nil
	h.HandleMessage(t.Context(), msg)
	if renders != 2 {
		t.Fatalf("rendered %d times, want 2 after refill", renders)
	}
	if last := api.SentPhotos[3]; last.Photo.FileID != "file_456" {
		t.Fatalf("refilled cache with %+v", last.Photo)
	}
}

func TestStatsIsRoutedToReadQueue(t *testing.T) {
	api := newMockBotAPI()
	api.SentPhotoSignal = make(chan struct{}, 1)
	svc, err := game.NewService()
	if err != nil {
		t.Fatal(err)
	}
	b := New(api, svc, nil, nil, time.Minute, nil)
	defer b.dispatcher.Stop(2 * time.Second)
	b.username = "unokamikaze"
	b.cmdHandler.rankingService = &ranking.Service{Repository: &mockPrivateRankingRepo{}}

	started := make(chan struct{})
	release := make(chan struct{})
	b.dispatcher.EnqueueChat(-777, func(context.Context) {
		close(started)
		<-release
	})
	<-started

	b.processUpdate(t.Context(), telego.Update{Message: &telego.Message{
		Chat: telego.Chat{ID: -777, Type: "supergroup", Title: "Grupo"},
		From: &telego.User{ID: 42, FirstName: "Ana", Username: "ana"},
		Text: "/stats@unokamikaze",
	}})
	select {
	case <-api.SentPhotoSignal:
	case <-time.After(2 * time.Second):
		t.Fatal("stats did not run while the chat worker was blocked")
	}
	close(release)
}

func TestStatsSendUsesFreshContext(t *testing.T) {
	api := newMockBotAPI()
	h := statsHandler(t, api, nil, func(context.Context, int64, int64, time.Time) (ranking.PlayerStats, error) {
		return ranking.PlayerStats{System: groups.Updated, Games: 1}, nil
	})
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	h.handleStats(canceled, -780, -780, 42, "Grupo", "Ana")

	if api.SendPhotoCtx == nil {
		t.Fatal("sendPhoto received no context")
	}
	if _, ok := api.SendPhotoCtx.Deadline(); !ok {
		t.Fatal("sendPhoto context has no fresh deadline")
	}
	if api.SendPhotoCtxErr != nil {
		t.Fatalf("sendPhoto received an expired context: %v", api.SendPhotoCtxErr)
	}
}

func validAvatarPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
