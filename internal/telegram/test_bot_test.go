package telegram

import (
	"github.com/gr1ksdev/UnoKamikaze/internal/game"
	"log/slog"
	"time"
)

// Handler-level fixtures have a known identity without running Telegram startup.
func newTestBot(api BotAPI, service *game.Service, tokens *TokenStore, renderer *Renderer, ttl time.Duration, logger *slog.Logger) *Bot {
	b := New(api, service, tokens, renderer, ttl, logger)
	b.username = "unokamikaze"
	b.cmdHandler.botUsername = "unokamikaze"
	return b
}
