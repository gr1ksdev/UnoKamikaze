package telegram

import (
	"testing"
	"time"

	"github.com/gr1ksdev/UnoKamikaze/internal/game"
	"github.com/mymmrac/telego"
)

func TestGroupCommandsRequireOwnUsername(t *testing.T) {
	for _, chatType := range []string{"group", "supergroup"} {
		for _, command := range []string{"/new", "/help", "/join", "/reset", "/new@otherbot", "/reset@otherbot"} {
			api := newMockBotAPI()
			svc, _ := game.NewService()
			h := NewCommandHandler(api, svc, NewRenderer(nil), nil, "unokamikaze", nil)
			msg := &telego.Message{Chat: telego.Chat{ID: -10, Type: chatType}, From: &telego.User{ID: 1}, Text: command, SenderChat: &telego.Chat{ID: -11}, IsTopicMessage: true}
			h.HandleMessage(t.Context(), msg)
			h.HandleReset(t.Context(), msg, func() { t.Fatal("unaddressed reset ran") })
			if len(api.SentMessages) != 0 {
				t.Fatalf("%s %s responded without target", chatType, command)
			}
		}
	}
}

func TestGroupCommandsAreEnglishOnly(t *testing.T) {
	api := newMockBotAPI()
	svc, _ := game.NewService()
	h := NewCommandHandler(api, svc, NewRenderer(NewUserCache(100)), NewTokenStore(100, 10, time.Now, nil), "unokamikaze", nil)
	msg := &telego.Message{Chat: telego.Chat{ID: -10, Type: "supergroup"}, From: &telego.User{ID: 1}, Text: commandName(cmdNew) + "@unokamikaze"}
	h.HandleMessage(t.Context(), msg)
	summary, err := svc.FindChatGame(t.Context(), -10)
	if err != nil {
		t.Fatal(err)
	}

	// The old Portuguese spelling is silently ignored.
	msg.Text = "/entrar@unokamikaze"
	msg.From = &telego.User{ID: 2}
	h.HandleMessage(t.Context(), msg)
	view, _ := svc.PublicView(t.Context(), summary.GameID)
	if len(view.Players) != 0 {
		t.Fatal("Portuguese command was accepted")
	}

	msg.Text = "/join@unokamikaze"
	h.HandleMessage(t.Context(), msg)
	view, _ = svc.PublicView(t.Context(), summary.GameID)
	if len(view.Players) != 1 || view.Players[0].ID != 2 {
		t.Fatal("English command was refused")
	}
}

func TestPrivateCommandsStillAllowBare(t *testing.T) {
	api := newMockBotAPI()
	svc, _ := game.NewService()
	h := NewCommandHandler(api, svc, NewRenderer(nil), nil, "unokamikaze", nil)
	for _, command := range []string{"/start", "/help"} {
		h.HandleMessage(t.Context(), &telego.Message{Chat: telego.Chat{ID: 1, Type: "private"}, From: &telego.User{ID: 1}, Text: command})
	}
	if len(api.SentMessages) != 2 {
		t.Fatal("private commands require username")
	}
	h.HandleMessage(t.Context(), &telego.Message{Chat: telego.Chat{ID: 1, Type: "private"}, From: &telego.User{ID: 1}, Text: "/ajuda"})
	if len(api.SentMessages) != 2 {
		t.Fatal("Portuguese command alias was accepted in private chat")
	}
}
