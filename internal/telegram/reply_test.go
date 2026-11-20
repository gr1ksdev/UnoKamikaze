package telegram

import (
	"testing"

	"github.com/mymmrac/telego"
)

func TestCommandResponsesMarkTheCommand(t *testing.T) {
	api := newMockBotAPI()
	h := statsHandler(t, api, nil, nil)

	h.HandleMessage(t.Context(), &telego.Message{
		MessageID: 123,
		Chat:      telego.Chat{ID: 55, Type: "private"},
		From:      &telego.User{ID: 1, FirstName: "Ana"},
		Text:      "/start",
	})
	if len(api.SentMessages) != 1 {
		t.Fatalf("sent %d messages, want 1", len(api.SentMessages))
	}
	reply := api.SentMessages[0].ReplyParameters
	if reply == nil || reply.MessageID != 123 || !reply.AllowSendingWithoutReply {
		t.Fatalf("/start reply = %+v, want reply to 123", reply)
	}

	h.HandleMessage(t.Context(), &telego.Message{
		MessageID: 456,
		Chat:      telego.Chat{ID: -10, Type: "supergroup", Title: "Grupo"},
		From:      &telego.User{ID: 2, FirstName: "Bob"},
		Text:      "/ajuda@unokamikaze",
	})
	reply = api.SentMessages[len(api.SentMessages)-1].ReplyParameters
	if reply == nil || reply.MessageID != 456 {
		t.Fatalf("/ajuda reply = %+v, want reply to 456", reply)
	}
}

func TestPlainReplyIsNotMarked(t *testing.T) {
	api := newMockBotAPI()
	h := statsHandler(t, api, nil, nil)
	h.reply(t.Context(), 55, "texto", nil)
	if got := api.SentMessages[0].ReplyParameters; got != nil {
		t.Fatalf("plain reply has ReplyParameters: %+v", got)
	}
}

func TestResetResponseMarksTheCommand(t *testing.T) {
	api := newMockBotAPI()
	h := statsHandler(t, api, nil, nil)
	h.HandleMessage(t.Context(), &telego.Message{
		MessageID: 789,
		Chat:      telego.Chat{ID: -11, Type: "supergroup", Title: "Grupo"},
		From:      &telego.User{ID: 3, FirstName: "Carol"},
		Text:      "/reset@unokamikaze",
	})
	// /reset is normally routed to the recovery lane before HandleMessage;
	// here it exercises the same handler and must still mark the command.
	for _, msg := range api.SentMessages {
		if msg.ReplyParameters != nil && msg.ReplyParameters.MessageID == 789 {
			return
		}
	}
	t.Fatalf("no response marked to 789: %+v", api.SentMessages)
}
