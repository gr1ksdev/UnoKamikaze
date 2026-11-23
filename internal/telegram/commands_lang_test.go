package telegram

import (
	"strings"
	"testing"
	"time"

	"github.com/gr1ksdev/UnoKamikaze/internal/game"
	"github.com/mymmrac/telego"
)

func TestResolveCommandOnlyAcceptsEnglish(t *testing.T) {
	for _, tc := range []struct {
		accepted string
		want     commandID
	}{
		{"new", cmdNew},
		{"join", cmdJoin},
		{"settings", cmdConfig},
		{"help", cmdHelp},
	} {
		if id, ok := resolveCommand(tc.accepted); !ok || id != tc.want {
			t.Fatalf("%s should resolve to %s, got %s %v", tc.accepted, tc.want, id, ok)
		}
	}
	for _, rejected := range []string{"novo", "entrar", "iniciar", "ajuda", "config", "pular"} {
		if _, ok := resolveCommand(rejected); ok {
			t.Fatalf("Portuguese command /%s must not be accepted", rejected)
		}
	}
}

func TestCommandListIsEnglishWithPortugueseDescriptions(t *testing.T) {
	names := groupCommandNames()
	for _, want := range []string{"new", "join", "start", "help", "settings"} {
		if !contains(names, want) {
			t.Fatalf("menu missing /%s", want)
		}
	}
	for _, forbidden := range []string{"novo", "entrar", "ajuda", "config"} {
		if contains(names, forbidden) {
			t.Fatalf("menu exposes /%s", forbidden)
		}
	}
	for _, cmd := range commandList() {
		if cmd.Description == "" || strings.Contains(cmd.Description, "https://") {
			t.Fatalf("missing Portuguese description for /%s", cmd.Command)
		}
	}
}

func TestGroupAcceptsOnlyEnglishCommands(t *testing.T) {
	api := newMockBotAPI()
	svc, _ := game.NewService()
	h := NewCommandHandler(api, svc, NewRenderer(NewUserCache(100)), NewTokenStore(100, 10, time.Now, nil), "unokamikaze", nil)
	chat := telego.Chat{ID: -55, Type: "supergroup"}
	h.HandleMessage(t.Context(), &telego.Message{Chat: chat, From: &telego.User{ID: 1, FirstName: "Owner"}, Text: "/new@unokamikaze"})
	summary, err := svc.FindChatGame(t.Context(), -55)
	if err != nil {
		t.Fatal(err)
	}

	h.HandleMessage(t.Context(), &telego.Message{Chat: chat, From: &telego.User{ID: 2, FirstName: "PT"}, Text: "/entrar@unokamikaze"})
	view, _ := svc.PublicView(t.Context(), summary.GameID)
	if len(view.Players) != 0 {
		t.Fatal("Portuguese command was accepted")
	}

	h.HandleMessage(t.Context(), &telego.Message{Chat: chat, From: &telego.User{ID: 2, FirstName: "EN"}, Text: "/join@unokamikaze"})
	view, _ = svc.PublicView(t.Context(), summary.GameID)
	if len(view.Players) != 1 || view.Players[0].ID != 2 {
		t.Fatal("English command was refused")
	}
}

func TestHelpUsesEnglishCommandsAndPortugueseText(t *testing.T) {
	help := NewRenderer(nil).RenderHelp("ExampleBot")
	for _, want := range []string{"/help", "/new", "/settings", "Comandos", "Em grupos"} {
		if !strings.Contains(help, want) {
			t.Fatalf("help missing %s", want)
		}
	}
	for _, forbidden := range []string{"/ajuda", "/novo", "/config"} {
		if strings.Contains(help, forbidden) {
			t.Fatalf("help still exposes %s", forbidden)
		}
	}
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
