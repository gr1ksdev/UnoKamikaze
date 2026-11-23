package telegram

import (
	"strings"
	"testing"
	"time"

	"github.com/gr1ksdev/UnoKamikaze/internal/game"
	"github.com/gr1ksdev/UnoKamikaze/internal/uno"
	"github.com/mymmrac/telego"
)

func TestRoomLockCommands(t *testing.T) {
	api := newMockBotAPI()
	svc, err := game.NewService()
	if err != nil {
		t.Fatal(err)
	}
	r := NewRenderer(nil)
	h := NewCommandHandler(api, svc, r, NewTokenStore(100, 10, time.Now, nil), "unokamikaze", nil)
	send := func(id int64, command string, want string) {
		t.Helper()
		h.HandleMessage(t.Context(), &telego.Message{Chat: telego.Chat{ID: -10, Type: "supergroup"}, From: &telego.User{ID: id, FirstName: "Player"}, Text: command + "@unokamikaze"})
		if !strings.Contains(api.LastSentMessage(), want) {
			t.Fatalf("%s: %s, want %s", command, api.LastSentMessage(), want)
		}
	}
	send(99, "/close", "Nenhuma partida ativa")
	send(99, "/new", "Partida de UNO")
	send(1, "/join", "1/10")
	send(2, "/join", "2/10")
	for _, active := range []bool{false, true} {
		if active {
			send(1, "/start", "Partida iniciada")
		}
		send(1, "/close", "Apenas o responsável")
		send(99, "/close", "🔒 A partida foi trancada.\nNovos jogadores não poderão entrar.")
		send(99, "/close", "🔒 A partida já está trancada.")
		send(3, "/join", "🔒 Esta partida está trancada e não aceita novos jogadores.")
		send(1, "/open", "Apenas o responsável")
		send(99, "/open", "🔓 A partida foi destrancada.\nNovos jogadores podem entrar novamente.")
		send(99, "/open", "🔓 A partida já está aberta.")
	}
	send(3, "/join", "entrou na partida em andamento")
	summary, _ := svc.FindChatGame(t.Context(), -10)
	view, _ := svc.PublicView(t.Context(), summary.GameID)
	if view.OwnerID != 99 || len(view.Players) != 3 || view.Phase != uno.TakingTurn {
		t.Fatal(view)
	}
	send(99, "/kill", "Partida cancelada")
	send(99, "/open", "Nenhuma partida ativa")
	send(99, "/new", "Partida de UNO")
	send(4, "/join", "1/10")
	for _, text := range []string{"/close", "Impede novos jogadores de entrar", "/open", "Permite novas entradas"} {
		if !strings.Contains(r.RenderHelp("unokamikaze"), text) {
			t.Fatal("help missing", text)
		}
	}
}
