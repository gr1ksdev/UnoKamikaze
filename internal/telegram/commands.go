package telegram

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/gr1ksdev/UnoKamikaze/internal/game"
	"github.com/gr1ksdev/UnoKamikaze/internal/groups"
	"github.com/gr1ksdev/UnoKamikaze/internal/ranking"
	"github.com/gr1ksdev/UnoKamikaze/internal/statscard"
	"github.com/gr1ksdev/UnoKamikaze/internal/uno"
	"github.com/mymmrac/telego"
	"golang.org/x/sync/singleflight"
)

type replyToKey struct{}

// withReplyTo records the command message a response should be attached to.
// It lives in the context so handlers never need a shared mutable field.
func withReplyTo(ctx context.Context, messageID int) context.Context {
	if messageID <= 0 {
		return ctx
	}
	return context.WithValue(ctx, replyToKey{}, messageID)
}

func replyToFrom(ctx context.Context) int {
	id, _ := ctx.Value(replyToKey{}).(int)
	return id
}

func replyParameters(ctx context.Context) *telego.ReplyParameters {
	if id := replyToFrom(ctx); id > 0 {
		return &telego.ReplyParameters{MessageID: id, AllowSendingWithoutReply: true}
	}
	return nil
}

type CommandHandler struct {
	rankingService *ranking.Service
	avatarFetcher  AvatarFetcher
	statsCache     *statsCardCache
	renderGroup    singleflight.Group
	renderCard     func(statscard.Data) ([]byte, error)
	current        *currentGameTracker
	knownUsers     groups.UserRepository
	finalize       func(context.Context, game.Outcome) func()
	groupConfigs   groups.Repository
	groupsService  *groups.Service
	bot            BotAPI
	service        *game.Service
	renderer       *Renderer
	tokens         *TokenStore
	botUsername    string
	logger         *slog.Logger
}

func (h *CommandHandler) SetGroupsService(s *groups.Service) {
	h.groupsService = s
}

func (h *CommandHandler) getGroupsService() *groups.Service {
	if h.groupsService != nil {
		return h.groupsService
	}
	if h.groupConfigs != nil {
		return &groups.Service{
			Repository: h.groupConfigs,
			LookupMembership: func(ctx context.Context, chatID, userID int64) (groups.Membership, error) {
				return lookupMembershipAPI(ctx, h.bot, chatID, userID)
			},
		}
	}
	return nil
}

func NewCommandHandler(
	bot BotAPI,
	service *game.Service,
	renderer *Renderer,
	tokens *TokenStore,
	botUsername string,
	logger *slog.Logger,
) *CommandHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &CommandHandler{
		bot:         bot,
		service:     service,
		renderer:    renderer,
		tokens:      tokens,
		botUsername: strings.ToLower(botUsername),
		logger:      logger,
		statsCache:  newStatsCardCache(statsCacheLimit),
		renderCard:  statscard.Render,
	}
}

func stringPtr(s string) *string {
	return &s
}

func makeGameButtons(view game.PublicGameView) *telego.InlineKeyboardMarkup {
	if view.GameID == "" || view.Closed || view.Phase == uno.Finished {
		return nil
	}
	if view.Phase == uno.Lobby {
		// In the lobby there are no cards to show yet: the button starts the
		// game instead of opening an empty hand.
		return &telego.InlineKeyboardMarkup{
			InlineKeyboard: [][]telego.InlineKeyboardButton{{
				{Text: "▶️ Iniciar", CallbackData: "start_" + string(view.GameID)},
			}},
		}
	}
	return &telego.InlineKeyboardMarkup{
		InlineKeyboard: [][]telego.InlineKeyboardButton{
			{
				{
					Text: "🃏 Suas cartas",
					// An empty inline query keeps the chat input free of any
					// internal identifier; the bot resolves the game itself.
					SwitchInlineQueryCurrentChat: stringPtr(""),
				},
			},
		},
	}
}

func makePrivateStartButtons(botUsername string) *telego.InlineKeyboardMarkup {
	username := strings.TrimPrefix(strings.TrimSpace(botUsername), "@")
	if username == "" {
		return nil
	}
	return &telego.InlineKeyboardMarkup{
		InlineKeyboard: [][]telego.InlineKeyboardButton{{{
			Text: "➕ Adicionar a um grupo",
			URL:  fmt.Sprintf("https://t.me/%s?startgroup=true", username),
		}}},
	}
}

func makeGroupConfigButtons(config groups.Config) *telego.InlineKeyboardMarkup {
	legacyText := "🔴 Vitória"
	updatedText := "🔴 Colocação"
	if config.RankingSystem == groups.Updated {
		updatedText = "🟢 Colocação"
	} else {
		legacyText = "🟢 Vitória"
	}

	return &telego.InlineKeyboardMarkup{
		InlineKeyboard: [][]telego.InlineKeyboardButton{{
			{
				Text:         legacyText,
				CallbackData: fmt.Sprintf("cfg_rank_legacy_%d", config.ChatID),
			},
			{
				Text:         updatedText,
				CallbackData: fmt.Sprintf("cfg_rank_updated_%d", config.ChatID),
			},
		}},
	}
}

func makeGroupWelcomeButtons(chatID int64) *telego.InlineKeyboardMarkup {
	return &telego.InlineKeyboardMarkup{
		InlineKeyboard: [][]telego.InlineKeyboardButton{
			{
				{
					Text:         "⚙️ Configurar",
					CallbackData: fmt.Sprintf("cfg_open_%d", chatID),
				},
			},
		},
	}
}

func (h *CommandHandler) reply(ctx context.Context, chatID int64, text string, markup *telego.InlineKeyboardMarkup) {
	params := &telego.SendMessageParams{
		ChatID:    telego.ChatID{ID: chatID},
		Text:      text,
		ParseMode: "HTML",
	}
	params.ReplyParameters = replyParameters(ctx)
	if markup != nil {
		params.ReplyMarkup = markup
	}
	_, err := h.bot.SendMessage(ctx, params)
	if err != nil {
		h.logger.WarnContext(ctx, "failed to send reply message", "chat_id", chatID, "error", err.Error())
	}
}

func (h *CommandHandler) HandleMessage(ctx context.Context, msg *telego.Message) {
	if msg == nil || msg.Text == "" {
		return
	}
	ctx = withReplyTo(ctx, msg.MessageID)

	// Update user in presentation cache if From is available
	if msg.From != nil {
		h.renderer.userCache.Put(uno.PlayerID(msg.From.ID), msg.From.FirstName, msg.From.Username)
	}

	cmdName, fields, ok := parseMessageCommand(msg, h.botUsername)
	if !ok {
		return
	}

	// Check sender identity
	if msg.From == nil || msg.From.IsBot {
		return
	}
	if msg.SenderChat != nil {
		h.reply(ctx, msg.Chat.ID, "⚠️ Ações devem ser executadas por um usuário identificável, não como canal ou administrador anônimo.", nil)
		return
	}

	// A message thread can exist outside forums; only IsTopicMessage identifies a topic.
	if msg.IsTopicMessage {
		h.reply(ctx, msg.Chat.ID, "⚠️ Tópicos de fórum ainda não são suportados. Crie e jogue a partida no chat geral do grupo.", nil)
		return
	}

	actorID := uno.PlayerID(msg.From.ID)
	chatID := game.ChatID(msg.Chat.ID)
	isGroup := msg.Chat.Type == "group" || msg.Chat.Type == "supergroup"

	// Private chats have no group setting: Portuguese spelling is the default.
	if !isGroup {
		// /start is a Telegram convention and stays universal in private.
		if cmdName == "start" {
			h.reply(ctx, msg.Chat.ID, h.renderer.RenderWelcome(), makePrivateStartButtons(h.botUsername))
			return
		}
		id, ok := resolveCommand(cmdName)
		if !ok {
			return
		}
		switch id {
		case cmdHelp:
			h.reply(ctx, msg.Chat.ID, h.renderer.RenderHelp(h.botUsername), nil)
		case cmdRanking:
			h.handlePrivateRanking(ctx, msg.Chat.ID, int64(actorID))
		case cmdStats:
			h.handleStats(ctx, msg.Chat.ID, 0, actorID, "", msg.From.FirstName)
		default:
			h.reply(ctx, msg.Chat.ID, fmt.Sprintf("⚠️ Este comando só pode ser utilizado em grupos. Adicione o bot a um grupo para jogar!\n\nUse %s para mais instruções.", commandName(cmdHelp)), nil)
		}
		return
	}

	if isGroup && msg.Chat.Title != "" {
		if svc := h.getGroupsService(); svc != nil {
			_ = svc.ObserveGroupTitle(ctx, int64(chatID), msg.Chat.Title)
		}
	}

	// Observed names stay in RAM during gameplay and are flushed at closure.
	if summary, err := h.service.FindChatGame(ctx, chatID); err == nil {
		_ = h.service.ObservePlayer(ctx, game.Actor{PlayerID: actorID, ChatID: chatID}, summary.GameID, observedName(*msg.From), msg.From.Username)
		// The inline menu resolves the game from here: this is the group the
		// player is acting in.
		h.current.Set(actorID, summary.GameID)
	}
	// /start true is the Telegram deep link used when the bot is added to a
	// group. It is protocol traffic, not a user command, so it is universal.
	if cmdName == "start" && len(fields) > 1 && fields[1] == "true" {
		h.reply(ctx, int64(msg.Chat.ID), fmt.Sprintf("👋 <b>UnoKamikaze adicionado!</b>\n\nUse %s para criar uma partida ou %s para conhecer os comandos.",
			commandName(cmdNew), commandName(cmdHelp)), nil)
		return
	}

	// Debug commands are intentionally outside the language table: /dar is
	// always /dar, and the stub is a no-op in production builds.
	if h.handleDebugCommand(ctx, msg, cmdName, fields) {
		return
	}

	id, ok := resolveCommand(cmdName)
	if !ok {
		// Unknown commands are ignored silently; Telegram only advertises the
		// English spellings registered in the command menu.
		return
	}

	switch id {
	case cmdRanking:
		h.handleRanking(ctx, msg.Chat.ID)
	case cmdStats:
		h.handleStats(ctx, msg.Chat.ID, chatID, actorID, msg.Chat.Title, msg.From.FirstName)
	case cmdNew:
		h.handleNovoObserved(ctx, actorID, chatID, msg.Chat.Title, msg.From)
	case cmdClose, cmdOpen:
		h.handleRoomLock(ctx, actorID, chatID, id == cmdClose)
	case cmdSkip:
		h.handlePular(ctx, actorID, chatID)
	case cmdKick:
		h.handleExpulsar(ctx, msg)
	case cmdJoin:
		h.handleEntrar(ctx, actorID, chatID)
	case cmdStart:
		h.handleIniciar(ctx, actorID, chatID)
	case cmdKill:
		h.handleCancelar(ctx, actorID, chatID)
	case cmdLeave:
		h.handleSair(ctx, actorID, chatID)
	case cmdState:
		h.handleEstado(ctx, chatID)
	case cmdReset:
		h.HandleReset(ctx, msg, nil)
	case cmdConfig:
		h.handleConfig(ctx, msg)
	case cmdHelp:
		h.reply(ctx, int64(msg.Chat.ID), h.renderer.RenderHelp(h.botUsername), nil)
	}
}

// Group commands must explicitly target this bot; private commands may omit it.
func parseMessageCommand(msg *telego.Message, botUsername string) (string, []string, bool) {
	if msg == nil {
		return "", nil, false
	}
	if msg.Chat.Type == "group" || msg.Chat.Type == "supergroup" {
		fields := strings.Fields(msg.Text)
		if len(fields) == 0 || botUsername == "" {
			return "", nil, false
		}
		command, username, addressed := strings.Cut(fields[0], "@")
		if !strings.HasPrefix(command, "/") || !addressed || !strings.EqualFold(username, botUsername) {
			return "", nil, false
		}
	}
	return parseBotCommand(msg.Text, botUsername)
}

func parseBotCommand(text, botUsername string) (string, []string, bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return "", nil, false
	}
	fields := strings.Fields(text)
	if len(fields) == 0 || len(fields[0]) < 2 {
		return "", nil, false
	}
	cmdParts := strings.SplitN(fields[0][1:], "@", 2)
	if len(cmdParts) == 2 && botUsername != "" && !strings.EqualFold(cmdParts[1], botUsername) {
		return "", nil, false
	}
	return strings.ToLower(cmdParts[0]), fields, true
}

// HandleReset runs through the dispatcher's independent recovery lane in
// production. resetExecution invalidates queued work only after authorization.
func (h *CommandHandler) HandleReset(ctx context.Context, msg *telego.Message, resetExecution func()) {
	if msg == nil || msg.From == nil || msg.From.IsBot {
		return
	}
	ctx = withReplyTo(ctx, msg.MessageID)
	cmdName, _, ok := parseMessageCommand(msg, h.botUsername)
	if !ok || cmdName != "reset" {
		return
	}
	if msg.SenderChat != nil {
		h.reply(ctx, msg.Chat.ID, "⚠️ O /reset deve ser executado por um administrador identificável, não como canal ou administrador anônimo.", nil)
		return
	}
	if msg.Chat.Type != "group" && msg.Chat.Type != "supergroup" {
		h.reply(ctx, msg.Chat.ID, "⚠️ Este comando só pode ser utilizado em grupos.", nil)
		return
	}
	if msg.IsTopicMessage {
		h.reply(ctx, msg.Chat.ID, "⚠️ Tópicos de fórum ainda não são suportados. Execute /reset no chat geral do grupo.", nil)
		return
	}

	actorID := uno.PlayerID(msg.From.ID)
	chatID := game.ChatID(msg.Chat.ID)
	isOwner := false
	if summary, err := h.service.FindChatGame(ctx, chatID); err == nil {
		isOwner = summary.OwnerID == actorID
	}
	isAdmin := false
	if !isOwner {
		roleCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		member, err := h.bot.GetChatMember(roleCtx, &telego.GetChatMemberParams{
			ChatID: telego.ChatID{ID: msg.Chat.ID},
			UserID: msg.From.ID,
		})
		cancel()
		if err != nil {
			h.logger.WarnContext(ctx, "failed to verify reset permission", "chat_id", chatID, "user_id", actorID, "error", err.Error())
			h.reply(ctx, msg.Chat.ID, "❌ Não foi possível confirmar sua permissão de administrador. O responsável atual ainda pode usar /reset.", nil)
			return
		}
		if member != nil {
			status := member.MemberStatus()
			isAdmin = status == telego.MemberStatusCreator || status == telego.MemberStatusAdministrator
		}
		if !isAdmin {
			h.reply(ctx, msg.Chat.ID, "⚠️ Apenas o responsável pela partida ou um administrador do grupo pode usar /reset.", nil)
			return
		}
	}

	if resetExecution != nil {
		resetExecution()
	}
	resetCtx, cancelReset := context.WithTimeout(ctx, 10*time.Second)
	result, err := h.service.ResetChat(resetCtx, game.Actor{PlayerID: actorID, ChatID: chatID, ChatAdmin: isAdmin})
	cancelReset()
	if err != nil {
		if errors.Is(err, game.ErrForbidden) {
			h.reply(ctx, msg.Chat.ID, "⚠️ Sua autorização mudou antes do reset. Tente novamente como administrador do grupo.", nil)
			return
		}
		h.logger.ErrorContext(ctx, "failed to reset chat state", "chat_id", chatID, "user_id", actorID, "error", err.Error())
		h.reply(ctx, msg.Chat.ID, "❌ Não foi possível resetar o estado deste grupo.", nil)
		return
	}
	for _, gameID := range result.GameIDs {
		h.tokens.InvalidateGame(gameID)
	}
	h.logger.WarnContext(ctx, "chat state reset",
		"chat_id", chatID,
		"user_id", actorID,
		"removed_games", len(result.GameIDs),
		"removed_active", result.RemovedActive,
		"removed_history", result.RemovedHistory,
	)
	if len(result.GameIDs) == 0 {
		h.reply(ctx, msg.Chat.ID, "♻️ <b>A fila deste grupo foi renovada.</b> Não havia partida ou histórico para remover. Use /new para iniciar.", nil)
		return
	}
	h.reply(ctx, msg.Chat.ID, "♻️ <b>Estado deste grupo resetado.</b> Tarefas e botões antigos foram invalidados. Use /new para iniciar uma nova partida.", nil)
}

func (h *CommandHandler) handleNovoObserved(ctx context.Context, actorID uno.PlayerID, chatID game.ChatID, chatTitle string, user *telego.User) {
	config := groups.Defaults(int64(chatID))
	if h.groupConfigs != nil {
		var err error
		config, err = h.groupConfigs.GetOrCreateGroupConfig(ctx, int64(chatID))
		if err != nil {
			h.logger.Warn("failed to load group config", "chat_id", chatID, "error", err)
			h.reply(ctx, int64(chatID), "❌ Não foi possível carregar a configuração do grupo. Tente novamente.", nil)
			return
		}
	}
	if h.knownUsers != nil && user != nil {
		if err := h.knownUsers.ObserveGroupUser(ctx, groups.KnownUser{ChatID: int64(chatID), UserID: user.ID, DisplayName: observedName(*user), Username: user.Username, LastSeenAt: time.Now().UTC()}); err != nil {
			h.logger.Warn("failed to observe group user", "chat_id", chatID, "error", err)
		}
	}
	req := game.CreateRequest{
		GroupConfig:  config.Snapshot(),
		ChatName:     chatTitle,
		Rules:        uno.CaseiroRules(),
		ReplaceLobby: true,
	}
	actor := game.Actor{PlayerID: actorID, ChatID: chatID}

	outcome, err := h.service.Create(ctx, actor, req)
	if err != nil {
		if errors.Is(err, game.ErrChatOccupied) {
			h.reply(ctx, int64(chatID), "⚠️ Já existe uma partida iniciada em andamento neste grupo.\nUse /state para ver ou /kill para encerrar.", nil)
			return
		}
		h.reply(ctx, int64(chatID), "❌ Não foi possível criar a partida. Tente novamente.", nil)
		return
	}
	if outcome.ReplacedGameID != "" {
		h.tokens.InvalidateGame(outcome.ReplacedGameID)
	}

	h.reply(ctx, int64(chatID), h.renderer.RenderLobby(outcome.View), h.gameMarkup(outcome.View))
}

func (h *CommandHandler) handleEntrar(ctx context.Context, actorID uno.PlayerID, chatID game.ChatID) {
	summary, err := h.service.FindChatGame(ctx, chatID)
	if err != nil {
		if errors.Is(err, game.ErrNoActiveGame) || errors.Is(err, game.ErrGameNotFound) {
			h.reply(ctx, int64(chatID), "⚠️ Nenhuma partida aberta neste grupo. Use /new para criar uma.", nil)
			return
		}
		h.reply(ctx, int64(chatID), "❌ Erro ao buscar partida.", nil)
		return
	}

	actor := game.Actor{PlayerID: actorID, ChatID: chatID}
	action := uno.Action{
		Type:     uno.JoinGame,
		PlayerID: actorID,
		Revision: summary.Revision,
	}

	outcome, err := h.service.Apply(ctx, actor, summary.GameID, action)
	if errors.Is(err, uno.ErrStaleRevision) {
		// Retry once with updated summary
		if freshSummary, freshErr := h.service.FindChatGame(ctx, chatID); freshErr == nil {
			action.Revision = freshSummary.Revision
			outcome, err = h.service.Apply(ctx, actor, freshSummary.GameID, action)
		}
	}

	if err != nil {
		switch {
		case errors.Is(err, uno.ErrAlreadyFinished):
			h.reply(ctx, int64(chatID), "🏁 Você já terminou esta partida e não pode entrar novamente.", nil)
		case errors.Is(err, uno.ErrAlreadyJoined):
			h.reply(ctx, int64(chatID), "⚠️ Você já está inscrito nesta partida!", nil)
		case errors.Is(err, game.ErrRoomLocked):
			h.reply(ctx, int64(chatID), "🔒 Esta partida está trancada e não aceita novos jogadores.", nil)
		case errors.Is(err, uno.ErrPlayerLimit):
			h.reply(ctx, int64(chatID), "⚠️ A partida já atingiu o limite de 10 jogadores.", nil)
		case errors.Is(err, uno.ErrGameFinished), errors.Is(err, game.ErrGameClosed):
			h.reply(ctx, int64(chatID), "⚠️ A partida já foi finalizada.", nil)
		case errors.Is(err, uno.ErrLobbyClosed):
			h.reply(ctx, int64(chatID), "⚠️ Esta partida não permite entrada tardia.", nil)
		default:
			h.reply(ctx, int64(chatID), "❌ Não foi possível entrar na partida.", nil)
		}
		return
	}

	if outcome.View.Phase == uno.Lobby {
		h.reply(ctx, int64(chatID), h.renderer.RenderLobby(outcome.View), h.gameMarkup(outcome.View))
	} else {
		msg := fmt.Sprintf("✅ %s entrou na partida em andamento!", h.renderer.PlayerLink(actorID, outcome.View))
		if turn := h.renderer.RenderTurn(outcome.View); turn != "" {
			msg += "\n\n" + turn
		}
		h.reply(ctx, int64(chatID), msg, h.gameMarkup(outcome.View))
	}
}

// startGame is shared by the text command and the lobby button. It applies
// the start action and posts the initial top-card sticker.
func startGame(ctx context.Context, svc *game.Service, bot BotAPI, actorID uno.PlayerID, chatID game.ChatID, gameID uno.GameID, revision uint64) (game.Outcome, error) {
	actor := game.Actor{PlayerID: actorID, ChatID: chatID}
	outcome, err := svc.Apply(ctx, actor, gameID, uno.Action{
		Type:     uno.StartGame,
		PlayerID: actorID,
		Revision: revision,
	})
	if err != nil {
		return game.Outcome{}, err
	}
	if outcome.View.TopCard != nil {
		stickerID := GetCardStickerID(*outcome.View.TopCard)
		if stickerID != "" {
			if _, err := bot.SendSticker(ctx, &telego.SendStickerParams{
				ChatID:  telego.ChatID{ID: int64(chatID)},
				Sticker: telego.InputFile{FileID: stickerID},
			}); err != nil {
				slog.Default().WarnContext(ctx, "failed to send initial top card sticker", "chat_id", chatID, "error", err.Error())
			}
		}
	}
	return outcome, nil
}

// sendStartFlow posts the normal game-start message: a short announcement and
// whose turn it is. The top-card sticker is sent separately by startGame.
func sendStartFlow(ctx context.Context, bot BotAPI, renderer *Renderer, chatID game.ChatID, view game.PublicGameView) {
	text := "🚀 <b>Partida iniciada!</b>"
	if turn := renderer.RenderTurn(view); turn != "" {
		text += "\n\n" + turn
	}
	params := &telego.SendMessageParams{
		ChatID:    telego.ChatID{ID: int64(chatID)},
		Text:      text,
		ParseMode: "HTML",
	}
	if markup := makeGameButtons(view); markup != nil {
		params.ReplyMarkup = markup
	}
	_, _ = bot.SendMessage(ctx, params)
}

func (h *CommandHandler) handleIniciar(ctx context.Context, actorID uno.PlayerID, chatID game.ChatID) {
	summary, err := h.service.FindChatGame(ctx, chatID)
	if err != nil {
		if errors.Is(err, game.ErrNoActiveGame) || errors.Is(err, game.ErrGameNotFound) {
			h.reply(ctx, int64(chatID), "⚠️ Nenhuma partida aberta neste grupo. Use /new para criar uma.", nil)
			return
		}
		h.reply(ctx, int64(chatID), "❌ Erro ao buscar partida.", nil)
		return
	}

	if summary.Phase != uno.Lobby {
		h.reply(ctx, int64(chatID), "⚠️ A partida já foi iniciada!", nil)
		return
	}

	outcome, err := startGame(ctx, h.service, h.bot, actorID, chatID, summary.GameID, summary.Revision)
	if err != nil {
		switch {
		case errors.Is(err, game.ErrForbidden):
			h.reply(ctx, int64(chatID), "⚠️ Apenas o responsável pela partida pode iniciá-la.", nil)
		case errors.Is(err, uno.ErrNotEnoughPlayers):
			h.reply(ctx, int64(chatID), "⚠️ São necessários pelo menos 2 jogadores inscritos para iniciar.", nil)
		default:
			h.reply(ctx, int64(chatID), "❌ Não foi possível iniciar a partida.", nil)
		}
		return
	}

	sendStartFlow(ctx, h.bot, h.renderer, chatID, outcome.View)
}

func (h *CommandHandler) handleCancelar(ctx context.Context, actorID uno.PlayerID, chatID game.ChatID) {
	summary, err := h.service.FindChatGame(ctx, chatID)
	if err != nil {
		if errors.Is(err, game.ErrNoActiveGame) || errors.Is(err, game.ErrGameNotFound) {
			h.reply(ctx, int64(chatID), "⚠️ Nenhuma partida ativa encontrada neste grupo.", nil)
			return
		}
		h.reply(ctx, int64(chatID), "❌ Erro ao buscar partida.", nil)
		return
	}

	isAdmin := false
	isCreatorOrOwner := actorID == summary.OwnerID || actorID == summary.CreatorID
	if !isCreatorOrOwner {
		membership, err := lookupMembershipAPI(ctx, h.bot, int64(chatID), int64(actorID))
		if err != nil {
			h.logger.WarnContext(ctx, "failed to verify cancel permission", "chat_id", chatID, "user_id", actorID, "error", err.Error())
			h.reply(ctx, int64(chatID), "❌ Não foi possível confirmar sua permissão no grupo. O criador ou responsável ainda pode cancelar.", nil)
			return
		}
		isAdmin = membership.Admin
		if !isAdmin {
			h.reply(ctx, int64(chatID), "⚠️ Apenas o responsável pela partida ou um administrador do grupo pode cancelá-la.", nil)
			return
		}
	}

	actor := game.Actor{PlayerID: actorID, ChatID: chatID, ChatAdmin: isAdmin}
	action := uno.Action{
		Type:     uno.CancelGame,
		PlayerID: actorID,
		Revision: summary.Revision,
	}

	_, err = h.service.Apply(ctx, actor, summary.GameID, action)
	if err != nil {
		if errors.Is(err, game.ErrForbidden) {
			h.reply(ctx, int64(chatID), "⚠️ Apenas o responsável pela partida ou um administrador do grupo pode cancelá-la.", nil)
			return
		}
		h.reply(ctx, int64(chatID), "❌ Não foi possível cancelar a partida.", nil)
		return
	}

	h.tokens.InvalidateGame(summary.GameID)
	if isAdmin && !isCreatorOrOwner {
		h.reply(ctx, int64(chatID), "🛑 <b>Partida cancelada por um administrador.</b>", nil)
	} else {
		h.reply(ctx, int64(chatID), "🛑 <b>Partida cancelada pelo responsável.</b>", nil)
	}
}

func (h *CommandHandler) handleSair(ctx context.Context, actorID uno.PlayerID, chatID game.ChatID) {
	summary, err := h.service.FindChatGame(ctx, chatID)
	if err != nil {
		if errors.Is(err, game.ErrNoActiveGame) || errors.Is(err, game.ErrGameNotFound) {
			h.reply(ctx, int64(chatID), "⚠️ Nenhuma partida ativa encontrada neste grupo.", nil)
			return
		}
		h.reply(ctx, int64(chatID), "❌ Erro ao buscar partida.", nil)
		return
	}

	actor := game.Actor{PlayerID: actorID, ChatID: chatID}
	action := uno.Action{
		Type:     uno.LeaveGame,
		PlayerID: actorID,
		Revision: summary.Revision,
	}

	outcome, err := h.service.Apply(ctx, actor, summary.GameID, action)
	if err != nil {
		if errors.Is(err, uno.ErrUnknownPlayer) || errors.Is(err, game.ErrNotParticipant) {
			h.reply(ctx, int64(chatID), "⚠️ Você não está participando desta partida.", nil)
			return
		}
		h.reply(ctx, int64(chatID), "❌ Não foi possível sair da partida.", nil)
		return
	}

	var notify func()
	if h.finalize != nil {
		notify = h.finalize(ctx, outcome)
	}
	h.tokens.InvalidateUserGame(summary.GameID, actorID)
	if notify != nil {
		h.tokens.InvalidateGame(summary.GameID)
		notify()
		return
	}

	if outcome.View.Closed {
		h.tokens.InvalidateGame(summary.GameID)
		msg := fmt.Sprintf("👋 %s saiu da partida.\n\n%s",
			h.renderer.PlayerLink(actorID, outcome.View),
			h.renderer.RenderPublicState(outcome.View),
		)
		h.reply(ctx, int64(chatID), msg, nil)
	} else {
		msg := fmt.Sprintf("👋 %s saiu da partida.", h.renderer.PlayerLink(actorID, outcome.View))
		if turn := h.renderer.RenderTurn(outcome.View); turn != "" {
			msg += "\n\n" + turn
		}
		h.reply(ctx, int64(chatID), msg, h.gameMarkup(outcome.View))
	}
}

func (h *CommandHandler) handleEstado(ctx context.Context, chatID game.ChatID) {
	summary, err := h.service.FindChatGame(ctx, chatID)
	if err != nil {
		if errors.Is(err, game.ErrNoActiveGame) || errors.Is(err, game.ErrGameNotFound) {
			h.reply(ctx, int64(chatID), "⚠️ Nenhuma partida ativa ou recente encontrada neste grupo. Use /new para criar uma.", nil)
			return
		}
		h.reply(ctx, int64(chatID), "❌ Erro ao buscar partida.", nil)
		return
	}

	view, err := h.service.PublicView(ctx, summary.GameID)
	if err != nil {
		h.reply(ctx, int64(chatID), "❌ Erro ao consultar estado da partida.", nil)
		return
	}

	if view.Phase == uno.Lobby {
		h.reply(ctx, int64(chatID), h.renderer.RenderLobby(view), makeGameButtons(view))
	} else {
		h.reply(ctx, int64(chatID), h.renderer.RenderPublicState(view), makeGameButtons(view))
	}
}

func (h *CommandHandler) handleRoomLock(ctx context.Context, actorID uno.PlayerID, chatID game.ChatID, locked bool) {
	summary, err := h.service.FindChatGame(ctx, chatID)
	if err != nil {
		h.reply(ctx, int64(chatID), "⚠️ Nenhuma partida ativa encontrada neste grupo.", nil)
		return
	}
	_, changed, err := h.service.SetLocked(ctx, game.Actor{PlayerID: actorID, ChatID: chatID}, summary.GameID, locked)
	if err != nil {
		if errors.Is(err, game.ErrForbidden) {
			h.reply(ctx, int64(chatID), "⚠️ Apenas o responsável pela partida pode trancar ou destrancar.", nil)
		} else {
			h.reply(ctx, int64(chatID), "⚠️ Não foi possível alterar as entradas desta partida.", nil)
		}
		return
	}
	text := "🔓 A partida foi destrancada.\nNovos jogadores podem entrar novamente."
	if locked {
		text = "🔒 A partida foi trancada.\nNovos jogadores não poderão entrar."
	}
	if !changed {
		text = "🔓 A partida já está aberta."
		if locked {
			text = "🔒 A partida já está trancada."
		}
	}
	h.reply(ctx, int64(chatID), text, nil)
}

// handlePular advances the turn of a player who is stuck. Only the owner of
// the game or an administrator of the chat may ask for it.
func (h *CommandHandler) handlePular(ctx context.Context, actorID uno.PlayerID, chatID game.ChatID) {
	summary, err := h.service.FindChatGame(ctx, chatID)
	if err != nil {
		h.reply(ctx, int64(chatID), "⚠️ Nenhuma partida ativa encontrada neste grupo.", nil)
		return
	}
	view, err := h.service.PublicView(ctx, summary.GameID)
	if err != nil {
		h.reply(ctx, int64(chatID), "⚠️ Não foi possível consultar a partida.", nil)
		return
	}
	actor := game.Actor{PlayerID: actorID, ChatID: chatID}
	action := uno.Action{Type: uno.PenaltySkip, PlayerID: actorID, Revision: view.Revision}
	outcome, err := h.service.Apply(ctx, actor, summary.GameID, action)
	if err != nil {
		var early *game.ErrSkipTooEarly
		if errors.As(err, &early) {
			seconds := int(math.Ceil(early.Wait.Seconds()))
			if seconds < 1 {
				seconds = 1
			}
			h.reply(ctx, int64(chatID), fmt.Sprintf("⏰ Aguarde %ds antes de pular.", seconds), nil)
			return
		}
		if errors.Is(err, game.ErrForbidden) {
			h.reply(ctx, int64(chatID), "⚠️ Apenas quem está participando da partida pode pular o turno.", nil)
			return
		}
		h.reply(ctx, int64(chatID), "⚠️ Não foi possível pular o turno agora.", nil)
		return
	}
	h.reply(ctx, int64(chatID), h.renderer.RenderSkipNotice(actorID, action, outcome), nil)
}

// handleExpulsar removes another participant from the game. The target is the
// author of the message the command replies to.
func (h *CommandHandler) handleExpulsar(ctx context.Context, msg *telego.Message) {
	if msg == nil || msg.From == nil {
		return
	}
	chatID := game.ChatID(msg.Chat.ID)
	actorID := uno.PlayerID(msg.From.ID)
	summary, err := h.service.FindChatGame(ctx, chatID)
	if err != nil {
		h.reply(ctx, int64(chatID), "⚠️ Nenhuma partida ativa encontrada neste grupo.", nil)
		return
	}
	if msg.ReplyToMessage == nil || msg.ReplyToMessage.From == nil {
		h.reply(ctx, int64(chatID), "⚠️ Responda à mensagem do jogador que deseja expulsar.", nil)
		return
	}
	target := uno.PlayerID(msg.ReplyToMessage.From.ID)
	if target == actorID {
		h.reply(ctx, int64(chatID), "⚠️ Use /leave para sair da partida.", nil)
		return
	}
	view, err := h.service.PublicView(ctx, summary.GameID)
	if err != nil {
		h.reply(ctx, int64(chatID), "⚠️ Não foi possível consultar a partida.", nil)
		return
	}
	actor := game.Actor{PlayerID: actorID, ChatID: chatID}
	if !h.isChatAdmin(ctx, actorID, chatID) && view.OwnerID != actorID {
		h.reply(ctx, int64(chatID), "⚠️ Apenas o responsável pela partida ou um administrador pode expulsar jogadores.", nil)
		return
	}
	outcome, err := h.service.Apply(ctx, actor, summary.GameID, uno.Action{Type: uno.KickPlayer, PlayerID: actorID, TargetID: target, Revision: view.Revision})
	if err != nil {
		switch {
		case errors.Is(err, game.ErrForbidden):
			h.reply(ctx, int64(chatID), "⚠️ Apenas o responsável pela partida ou um administrador pode expulsar jogadores.", nil)
		case errors.Is(err, uno.ErrUnknownPlayer):
			h.reply(ctx, int64(chatID), "⚠️ Esse jogador não está participando da partida.", nil)
		default:
			h.reply(ctx, int64(chatID), "⚠️ Não foi possível expulsar o jogador agora.", nil)
		}
		return
	}
	h.tokens.InvalidateUserGame(summary.GameID, target)
	text := fmt.Sprintf("🚫 %s foi expulso da partida.", h.renderer.PlayerLink(target, outcome.View))
	h.reply(ctx, int64(chatID), text, nil)
}

const (
	// Network work in /stats gets its own budget so an expired chat-task
	// deadline can never turn a failed photo into a failed fallback as well.
	statsSendTimeout     = 10 * time.Second
	statsFallbackTimeout = 5 * time.Second

	// Stages faster than this are not worth a log line.
	statsSlowThreshold = 200 * time.Millisecond
)

// logSlow emits a warning only when a /stats stage exceeded the threshold.
func shortID(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

func (h *CommandHandler) logSlow(ctx context.Context, stage string, started time.Time, attrs ...any) {
	elapsed := time.Since(started)
	if elapsed < statsSlowThreshold {
		return
	}
	fields := append([]any{"stage", stage, "duration_ms", elapsed.Milliseconds()}, attrs...)
	h.logger.WarnContext(ctx, "slow stats stage", fields...)
}

// statsContext drops the parent deadline (which may already be close to
// expiring) while keeping its values, and gives the operation a fresh budget.
func statsContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), timeout)
}

func (h *CommandHandler) replyStats(ctx context.Context, chatID int64, text string) {
	sendCtx, cancel := statsContext(ctx, statsFallbackTimeout)
	defer cancel()
	h.reply(sendCtx, chatID, text, nil)
}

// HandleStats is the read-only entry point used by the dedicated read queue.
// It intentionally skips the side effects of HandleMessage (observing group
// titles, player names and the current game) so it never touches game state.
func (h *CommandHandler) HandleStats(ctx context.Context, msg *telego.Message) {
	if msg == nil || msg.From == nil || msg.From.IsBot || msg.SenderChat != nil || msg.IsTopicMessage {
		return
	}
	ctx = withReplyTo(ctx, msg.MessageID)
	actorID := uno.PlayerID(msg.From.ID)
	if msg.Chat.Type == "group" || msg.Chat.Type == "supergroup" {
		chatID := game.ChatID(msg.Chat.ID)
		h.handleStats(ctx, msg.Chat.ID, chatID, actorID, msg.Chat.Title, msg.From.FirstName)
		return
	}
	h.handleStats(ctx, msg.Chat.ID, 0, actorID, "", msg.From.FirstName)
}

// handleStats sends the player's stats card as a photo. targetChatID is where
// the message goes; statsChatID is the aggregation scope (zero in private).
func (h *CommandHandler) handleStats(ctx context.Context, targetChatID int64, statsChatID game.ChatID, actorID uno.PlayerID, scope, displayName string) {
	if h.rankingService == nil {
		h.replyStats(ctx, targetChatID, "❌ Estatísticas não disponíveis no momento.")
		return
	}

	// Both the loading notice and the card quote the /stats command.
	reply := replyParameters(ctx)

	// The first card can take a few seconds (avatar download + upload). Post
	// immediate feedback and remove it once the real photo or fallback lands.
	loadingCtx, cancelLoading := statsContext(ctx, statsFallbackTimeout)
	loading, err := h.bot.SendMessage(loadingCtx, &telego.SendMessageParams{
		ChatID:          telego.ChatID{ID: targetChatID},
		Text:            "⏳ Gerando seu card...",
		ReplyParameters: reply,
	})
	cancelLoading()
	if err == nil && loading != nil && loading.MessageID != 0 {
		defer func() {
			deleteCtx, cancelDelete := statsContext(ctx, statsFallbackTimeout)
			defer cancelDelete()
			_ = h.bot.DeleteMessage(deleteCtx, &telego.DeleteMessageParams{
				ChatID:    telego.ChatID{ID: targetChatID},
				MessageID: loading.MessageID,
			})
		}()
	}

	totalStart := time.Now()
	defer h.logSlow(ctx, "total", totalStart, "chat_id", targetChatID, "user_id", actorID)

	readCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Start the avatar work before the database reads so the network latency
	// overlaps. The buffered channel keeps the goroutine from leaking even if
	// a database error makes us return early.
	type avatarResult struct {
		data []byte
		err  error
	}
	var avatarCh chan avatarResult
	if h.avatarFetcher != nil {
		avatarCh = make(chan avatarResult, 1)
		fetchCtx, cancelFetch := context.WithTimeout(ctx, avatarDownloadTimeout)
		go func() {
			defer cancelFetch()
			fetchStart := time.Now()
			data, err := h.avatarFetcher.Fetch(fetchCtx, int64(actorID))
			h.logSlow(fetchCtx, "avatar_fetch", fetchStart, "user_id", actorID)
			avatarCh <- avatarResult{data: data, err: err}
		}()
	}

	statsStart := time.Now()
	stats, err := h.rankingService.ReadPlayerStats(readCtx, int64(statsChatID), int64(actorID))
	h.logSlow(ctx, "db.stats", statsStart, "chat_id", statsChatID, "user_id", actorID)
	if err != nil {
		h.logger.WarnContext(ctx, "failed to read player stats", "chat_id", statsChatID, "user_id", actorID, "error", err)
		h.replyStats(ctx, targetChatID, "⚠️ Não foi possível consultar suas estatísticas. Tente novamente.")
		return
	}
	if scope != "" {
		rankStart := time.Now()
		group, rankErr := h.rankingService.ListGroupRanking(readCtx, int64(statsChatID))
		h.logSlow(ctx, "db.ranking", rankStart, "chat_id", statsChatID, "user_id", actorID)
		if rankErr == nil {
			for i, entry := range group.Entries {
				if entry.UserID == int64(actorID) {
					stats.Rank = i + 1
					stats.Total = int(group.Total)
					break
				}
			}
		}
	}

	if displayName == "" {
		displayName = h.renderer.userCache.GetRawName(actorID)
	}
	var avatar []byte
	if avatarCh != nil {
		waitStart := time.Now()
		result := <-avatarCh
		h.logSlow(ctx, "avatar_wait", waitStart, "user_id", actorID)
		if result.err != nil {
			h.logger.WarnContext(ctx, "failed to fetch profile photo", "user_id", actorID, "error", result.err)
		} else {
			avatar = result.data
		}
	}

	data := statscard.Data{
		Name:       displayName,
		Games:      stats.Games,
		Wins:       stats.Wins,
		HasHistory: stats.HasHistory(),
		Avatar:     avatar,
	}
	token := statscard.Token(data)
	caption := h.renderer.RenderStatsCaption(stats, scope)

	// A hit reuses the file_id Telegram returned on the first upload: no
	// rendering, no re-upload. The caption is always rebuilt from fresh data.
	cacheHit := false
	if h.statsCache != nil {
		if fileID, ok := h.statsCache.Get(token); ok {
			cacheHit = true
			h.logger.InfoContext(ctx, "stats cache hit", "token", shortID(token), "chat_id", targetChatID)
			params := &telego.SendPhotoParams{
				ChatID:          telego.ChatID{ID: targetChatID},
				Photo:           telego.InputFile{FileID: fileID},
				Caption:         caption,
				ParseMode:       "HTML",
				ReplyParameters: reply,
			}
			sendCtx, cancelSend := statsContext(ctx, statsSendTimeout)
			sendStart := time.Now()
			_, err := h.bot.SendPhoto(sendCtx, params)
			h.logSlow(ctx, "send_photo", sendStart, "chat_id", targetChatID, "cache_hit", true)
			cancelSend()
			if err == nil {
				return
			} else {
				h.logger.DebugContext(ctx, "cached stats photo failed; re-rendering", "error", err)
				h.statsCache.Delete(token)
			}
		}
	}
	if !cacheHit {
		h.logger.InfoContext(ctx, "stats cache miss", "token", shortID(token), "chat_id", targetChatID)
	}

	renderStart := time.Now()
	// Concurrent /stats for the same token share one render.
	rendered, err, _ := h.renderGroup.Do(token, func() (any, error) {
		return h.renderCard(data)
	})
	h.logSlow(ctx, "render", renderStart, "user_id", actorID)
	card, _ := rendered.([]byte)
	if err != nil {
		h.logger.WarnContext(ctx, "failed to render stats card", "chat_id", targetChatID, "error", err)
		h.replyStats(ctx, targetChatID, h.renderer.RenderPlayerStats(stats, scope))
		return
	}
	params := &telego.SendPhotoParams{
		ChatID:          telego.ChatID{ID: targetChatID},
		Photo:           telego.InputFile{File: namedBytes{Reader: bytes.NewReader(card), name: "stats.jpg"}},
		Caption:         caption,
		ParseMode:       "HTML",
		ReplyParameters: reply,
	}
	sendCtx, cancelSend := statsContext(ctx, statsSendTimeout)
	sendStart := time.Now()
	message, err := h.bot.SendPhoto(sendCtx, params)
	h.logSlow(ctx, "send_photo", sendStart, "chat_id", targetChatID, "cache_hit", false)
	cancelSend()
	if err != nil {
		h.logger.WarnContext(ctx, "failed to send stats photo", "chat_id", targetChatID, "error", err)
		h.replyStats(ctx, targetChatID, h.renderer.RenderPlayerStats(stats, scope))
		return
	}
	if h.statsCache != nil {
		if fileID := largestPhotoID(message); fileID != "" {
			h.logger.InfoContext(ctx, "stats cache stored", "token", shortID(token), "file_id", shortID(fileID))
			h.statsCache.Put(token, fileID)
		} else {
			h.logger.WarnContext(ctx, "sendPhoto response had no file_id; next /stats will re-render", "token", shortID(token))
		}
	}
}

// largestPhotoID returns the highest-resolution photo of a sendPhoto reply.
func largestPhotoID(message *telego.Message) string {
	if message == nil || len(message.Photo) == 0 {
		return ""
	}
	return message.Photo[len(message.Photo)-1].FileID
}

// isChatAdmin asks Telegram whether the user administers the chat.
func (h *CommandHandler) isChatAdmin(ctx context.Context, userID uno.PlayerID, chatID game.ChatID) bool {
	if h.bot == nil {
		return false
	}
	member, err := h.bot.GetChatMember(ctx, &telego.GetChatMemberParams{
		ChatID: telego.ChatID{ID: int64(chatID)},
		UserID: int64(userID),
	})
	if err != nil || member == nil {
		return false
	}
	switch member.MemberStatus() {
	case telego.MemberStatusCreator, telego.MemberStatusAdministrator:
		return true
	}
	return false
}

func (h *CommandHandler) handleConfig(ctx context.Context, msg *telego.Message) {
	if msg == nil || msg.From == nil {
		return
	}
	chatID := msg.Chat.ID
	actorID := msg.From.ID

	svc := h.getGroupsService()
	if svc == nil {
		h.reply(ctx, chatID, "❌ Configuração não disponível no momento.", nil)
		return
	}

	if svc.Repository != nil {
		if _, err := svc.Repository.GetOrCreateGroupConfig(ctx, chatID); err != nil {
			h.logger.WarnContext(ctx, "failed to ensure group config in /settings", "chat_id", chatID, "error", err)
		} else if h.knownUsers != nil {
			if err := h.knownUsers.ObserveGroupUser(ctx, groups.KnownUser{
				ChatID:      chatID,
				UserID:      actorID,
				DisplayName: observedName(*msg.From),
				Username:    msg.From.Username,
				LastSeenAt:  time.Now().UTC(),
			}); err != nil {
				h.logger.WarnContext(ctx, "failed to observe user in /settings", "chat_id", chatID, "error", err)
			}
		}
	}

	cfg, allowed, err := svc.CanConfigureUser(ctx, chatID, actorID)
	if err != nil && !errors.Is(err, groups.ErrForbidden) {
		h.logger.WarnContext(ctx, "failed to check config permissions", "chat_id", chatID, "user_id", actorID, "error", err)
	}

	if !allowed {
		h.reply(ctx, chatID, "⚠️ Somente administradores ou quem adicionou o bot pode alterar esta configuração.", nil)
		return
	}

	text := h.renderer.RenderGroupConfig(cfg)
	buttons := makeGroupConfigButtons(cfg)
	h.reply(ctx, chatID, text, buttons)
}

func (h *CommandHandler) HandleMyChatMember(ctx context.Context, update *telego.ChatMemberUpdated) {
	if update == nil {
		return
	}
	chatID := update.Chat.ID
	isGroup := update.Chat.Type == "group" || update.Chat.Type == "supergroup"
	if !isGroup {
		return
	}

	if update.OldChatMember == nil || update.NewChatMember == nil {
		return
	}

	oldStatus := update.OldChatMember.MemberStatus()
	newStatus := update.NewChatMember.MemberStatus()

	wasAbsent := oldStatus == telego.MemberStatusLeft || oldStatus == telego.MemberStatusBanned
	isPresent := newStatus == telego.MemberStatusMember || newStatus == telego.MemberStatusAdministrator

	if !wasAbsent || !isPresent {
		return
	}

	svc := h.getGroupsService()
	if update.Chat.Title != "" && svc != nil {
		_ = svc.ObserveGroupTitle(ctx, chatID, update.Chat.Title)
	}
	if update.From.ID > 0 && !update.From.IsBot {
		if svc != nil {
			if _, err := svc.RecordInstallation(ctx, chatID, update.From.ID); err != nil {
				h.logger.WarnContext(ctx, "failed to record bot installation", "chat_id", chatID, "user_id", update.From.ID, "error", err)
			}
		}
		if h.knownUsers != nil {
			if err := h.knownUsers.ObserveGroupUser(ctx, groups.KnownUser{
				ChatID:      chatID,
				UserID:      update.From.ID,
				DisplayName: observedName(update.From),
				Username:    update.From.Username,
				LastSeenAt:  time.Now().UTC(),
			}); err != nil {
				h.logger.WarnContext(ctx, "failed to observe installer user", "chat_id", chatID, "error", err)
			}
		}
	}

	config := groups.Defaults(chatID)
	if svc != nil && svc.Repository != nil {
		if c, err := svc.Repository.GetOrCreateGroupConfig(ctx, chatID); err == nil {
			config = c
		}
	}

	text := h.renderer.RenderGroupWelcome(config)
	buttons := makeGroupWelcomeButtons(chatID)
	h.reply(ctx, chatID, text, buttons)
}
