package telegram

import (
	"fmt"
	"html"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/gr1ksdev/UnoKamikaze/internal/game"
	"github.com/gr1ksdev/UnoKamikaze/internal/groups"
	"github.com/gr1ksdev/UnoKamikaze/internal/ranking"
	"github.com/gr1ksdev/UnoKamikaze/internal/uno"
)

type UserCache struct {
	mu    sync.RWMutex
	users map[uno.PlayerID]string
	order []uno.PlayerID
	limit int
}

func NewUserCache(limit int) *UserCache {
	if limit <= 0 {
		limit = 10000
	}
	return &UserCache{
		users: make(map[uno.PlayerID]string),
		order: make([]uno.PlayerID, 0),
		limit: limit,
	}
}

func (c *UserCache) Put(id uno.PlayerID, firstName, username string) {
	if id <= 0 {
		return
	}
	name := strings.TrimSpace(firstName)
	if name == "" {
		name = fmt.Sprintf("Jogador %d", id)
	}
	if username != "" {
		name = fmt.Sprintf("%s (@%s)", name, username)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.users[id]; !exists {
		if len(c.users) >= c.limit && len(c.order) > 0 {
			oldest := c.order[0]
			c.order = c.order[1:]
			delete(c.users, oldest)
		}
		c.order = append(c.order, id)
	}
	c.users[id] = name
}

func (c *UserCache) GetRawName(id uno.PlayerID) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if name, ok := c.users[id]; ok {
		return name
	}
	return fmt.Sprintf("Jogador %d", id)
}

// CardRepr returns a human-friendly string for a card, e.g. "❤️ 7" or "🌈+4 Coringa".
func CardRepr(c uno.Card) string {
	colorIcon := ColorIcon(c.Color)
	rankName := RankName(c.Rank)

	if c.Rank >= uno.Wild {
		return rankName
	}
	return fmt.Sprintf("%s %s", colorIcon, rankName)
}

func ColorIcon(color uno.Color) string {
	switch color {
	case uno.Red:
		return "❤️"
	case uno.Blue:
		return "💙"
	case uno.Green:
		return "💚"
	case uno.Yellow:
		return "💛"
	default:
		return "⬛️"
	}
}

func ColorNamePT(color uno.Color) string {
	switch color {
	case uno.Red:
		return "Vermelho"
	case uno.Blue:
		return "Azul"
	case uno.Green:
		return "Verde"
	case uno.Yellow:
		return "Amarelo"
	default:
		return "Nenhuma"
	}
}

func RankName(rank uno.Rank) string {
	switch rank {
	case uno.Zero:
		return "0"
	case uno.One:
		return "1"
	case uno.Two:
		return "2"
	case uno.Three:
		return "3"
	case uno.Four:
		return "4"
	case uno.Five:
		return "5"
	case uno.Six:
		return "6"
	case uno.Seven:
		return "7"
	case uno.Eight:
		return "8"
	case uno.Nine:
		return "9"
	case uno.DrawTwo:
		return "+2 (Comprar 2)"
	case uno.Reverse:
		return "🔄 Inverter"
	case uno.Skip:
		return "🚫 Pular"
	case uno.SwapHands:
		return "🔀 Trocar cartas"
	case uno.Wild:
		return "🌈 Coringa"
	case uno.WildDrawFour:
		return "🌈+4 Coringa Comprar 4"
	default:
		return "?"
	}
}

type Renderer struct {
	botID     atomic.Int64
	userCache *UserCache
}

func NewRenderer(cache *UserCache) *Renderer {
	if cache == nil {
		cache = NewUserCache(10000)
	}
	return &Renderer{userCache: cache}
}

// SetBotID configures presentation identity after GetMe, before update ingress.
func (r *Renderer) SetBotID(id int64) { r.botID.Store(id) }

// PlayerLink keeps the displayed identity separate from the mention target.
// Events and confirmations must use the resulting view, not the previous turn.
func (r *Renderer) PlayerLink(id uno.PlayerID, view game.PublicGameView) string {
	name := html.EscapeString(r.userCache.GetRawName(id))
	target := r.botID.Load()
	if target <= 0 {
		return name
	}
	if !view.Closed {
		responsible := uno.PlayerID(0)
		switch view.Phase {
		case uno.TakingTurn:
			responsible = view.CurrentTurn
		case uno.ChoosingPlayer:
			responsible = view.PlayerChooserID
		case uno.ChoosingColor:
			responsible = view.ColorChooserID
		case uno.BlockingSwap:
			// Name the player whose turn resumes after the decision, never the
			// target holding the Skip card.
			responsible = view.BlockTurnID
		}
		if id > 0 && id == responsible {
			target = int64(id)
		}
	}
	return fmt.Sprintf(`<a href="tg://user?id=%d">%s</a>`, target, name)
}

// RenderLobby returns formatted text for a newly created or joined lobby.
func (r *Renderer) RenderLobby(view game.PublicGameView) string {
	var sb strings.Builder
	sb.WriteString("🎮 <b>Partida de UNO</b>\n\n")
	sb.WriteString(fmt.Sprintf("Responsável: %s\n\n", r.PlayerLink(view.OwnerID, view)))

	sb.WriteString(fmt.Sprintf("Jogadores inscritos (%d/10):\n", len(view.Players)))
	if len(view.Players) == 0 {
		sb.WriteString("<i>Nenhum jogador inscrito ainda.</i>\n")
	} else {
		for i, p := range view.Players {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, r.PlayerLink(p.ID, view)))
		}
	}

	if view.Locked {
		sb.WriteString("\n🔒 Esta partida está trancada e não aceita novos jogadores.")
	} else {
		sb.WriteString(fmt.Sprintf("\nUse %s para participar.", commandName(cmdJoin)))
	}
	if len(view.Players) >= 2 {
		sb.WriteString(fmt.Sprintf("\nUse %s para começar!", commandName(cmdStart)))
	} else {
		sb.WriteString("\n(Mínimo de 2 jogadores para iniciar)")
	}

	return sb.String()
}

func placementLabel(position int) string {
	switch position {
	case 1:
		return "🥇"
	case 2:
		return "🥈"
	case 3:
		return "🥉"
	default:
		return fmt.Sprintf("%d.", position)
	}
}

func (r *Renderer) renderPlacements(view game.PublicGameView) string {
	lines := make([]string, 0, len(view.Placements))
	for _, pl := range view.Placements {
		lines = append(lines, fmt.Sprintf("%s %s", placementLabel(pl.Position), r.PlayerLink(pl.PlayerID, view)))
	}
	return strings.Join(lines, "\n")
}

// RenderPublicState displays the next decision and the actual turn sequence,
// starting at the current player and walking in the engine's direction.
// renderClosedState renders the final summary of a finished game.
func (r *Renderer) renderClosedState(view game.PublicGameView) string {
	var sb strings.Builder
	sb.WriteString("🏆 <b>Partida encerrada</b>")
	switch view.CloseReason {
	case game.Cancelled:
		sb.WriteString("\nCancelada pelo responsável.")
	case game.Departure:
		sb.WriteString("\nEncerrada após desistência de jogadores.")
	}
	if len(view.Placements) > 0 {
		sb.WriteString("\n\n" + r.renderPlacements(view))
	}
	return sb.String()
}

// RenderPublicState renders the detailed public state. It is requested on
// demand; the running flow only announces the action and the next turn.
func (r *Renderer) RenderPublicState(view game.PublicGameView) string {
	var sb strings.Builder
	if view.Closed || view.Phase == uno.Finished {
		return r.renderClosedState(view)
	}
	if view.TopCard != nil {
		sb.WriteString(fmt.Sprintf("🃏 Topo: <b>%s</b>\n", CardRepr(*view.TopCard)))
		if view.TopCard.Color == uno.NoColor && view.ActiveColor != uno.NoColor {
			sb.WriteString(fmt.Sprintf("🎨 Cor: %s <b>%s</b>\n", ColorIcon(view.ActiveColor), ColorNamePT(view.ActiveColor)))
		}
	}
	if view.DrawCounter > 0 {
		sb.WriteString(fmt.Sprintf("⚠️ Compra acumulada: %d cartas\n", view.DrawCounter))
	}
	if len(view.Placements) == 1 {
		sb.WriteString("🏅 Classificação: " + r.renderPlacements(view) + "\n")
	} else if len(view.Placements) > 1 {
		sb.WriteString("🏅 <b>Classificação</b>\n" + r.renderPlacements(view) + "\n")
	}
	sb.WriteString("\n")
	switch view.Phase {
	case uno.ChoosingPlayer:
		sb.WriteString(fmt.Sprintf("🔀 <b>Aguardando %s escolher com quem trocar ou manter a mão!</b>\n", r.PlayerLink(view.PlayerChooserID, view)))
	case uno.ChoosingColor:
		sb.WriteString(fmt.Sprintf("🎨 <b>Aguardando %s escolher a cor!</b>\n", r.PlayerLink(view.ColorChooserID, view)))
	case uno.BlockingSwap:
		// The block itself is private; only the public turn is announced.
		if view.BlockTurnID > 0 {
			sb.WriteString(fmt.Sprintf("Vez de: %s\n", r.PlayerLink(view.BlockTurnID, view)))
		}
	default:
		if view.CurrentTurn > 0 {
			sb.WriteString(fmt.Sprintf("Vez de: %s\n", r.PlayerLink(view.CurrentTurn, view)))
		}
	}
	if order := r.renderOrder(view, view.CurrentTurn); order != "" {
		sb.WriteString(order)
	}
	return strings.TrimSpace(sb.String())
}

// renderOrder walks the active seats from startAt in the current direction and
// includes each player's public card count. Counts are public information; the
// cards themselves remain private to the inline hand menu.
func (r *Renderer) renderOrder(view game.PublicGameView, startAt uno.PlayerID) string {
	if len(view.Order) == 0 {
		return ""
	}
	parts := make([]string, 0, len(view.Order))
	start, direction := 0, 1
	if view.Direction < 0 {
		direction = -1
	}
	for i, id := range view.Order {
		if id == startAt {
			start = i
			break
		}
	}
	for step := range view.Order {
		pid := view.Order[(start+step*direction+len(view.Order))%len(view.Order)]
		for _, player := range view.Players {
			if player.ID != pid || !player.Active {
				continue
			}
			text := fmt.Sprintf("%s (%d)", r.PlayerLink(pid, view), player.CardCount)
			if player.CardCount == 1 {
				text += " ⚠️ <b>UNO!</b>"
			}
			parts = append(parts, text)
			break
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "👥 " + strings.Join(parts, " → ")
}

// RenderTurn names the player whose decision is pending. A closed game has no
// pending decision and therefore no turn line.
func (r *Renderer) RenderTurn(view game.PublicGameView) string {
	if view.Closed {
		return ""
	}
	var responsible uno.PlayerID
	switch view.Phase {
	case uno.ChoosingColor:
		responsible = view.ColorChooserID
	case uno.ChoosingPlayer:
		responsible = view.PlayerChooserID
	case uno.BlockingSwap:
		responsible = view.BlockTurnID
	default:
		responsible = view.CurrentTurn
	}
	if responsible <= 0 {
		return ""
	}
	return fmt.Sprintf("Vez de: %s", r.PlayerLink(responsible, view))
}

// RenderActionConfirmation renders the only message posted for an accepted
// action. The sticker posted by inline mode already shows the card, so the
// confirmation carries just whose decision is next. A bluff challenge has no
// sticker of its own, so its result is announced here instead. A finished game
// shows the final placements.
// RenderSkipNotice announces a penalty skip: who was skipped, how much waiting
// time they have left and whose turn it is. It never carries the top card or
// the order with card counts, which belong exclusively to /state.
func (r *Renderer) RenderSkipNotice(actorID uno.PlayerID, action uno.Action, outcome game.Outcome) string {
	if outcome.View.Closed || outcome.View.Phase == uno.Finished || outcome.Skip == nil {
		return r.RenderActionConfirmation(actorID, action, outcome)
	}
	seconds := int(outcome.Skip.Remaining.Seconds())
	if outcome.Skip.Remaining > 0 && seconds == 0 {
		seconds = 1
	}
	text := fmt.Sprintf("⏰ %s foi pulado. Tempo restante: %ds.", r.PlayerLink(outcome.Skip.PlayerID, outcome.View), seconds)
	if turn := r.RenderTurn(outcome.View); turn != "" {
		text += "\n\n" + turn
	}
	return text
}

func (r *Renderer) RenderActionConfirmation(actorID uno.PlayerID, action uno.Action, outcome game.Outcome) string {
	var prefix string
	if action.Type == uno.CallBluff {
		for _, ev := range outcome.Events {
			if ev.Type != uno.BluffCalled {
				continue
			}
			target := r.PlayerLink(ev.TargetID, outcome.View)
			if ev.Success {
				prefix = fmt.Sprintf("Blefe pego! %s recebeu %d cartas!", target, ev.Count)
			} else {
				prefix = fmt.Sprintf("%s não blefou! %s recebeu %d cartas!", target, r.PlayerLink(actorID, outcome.View), ev.Count)
			}
			break
		}
	}
	if prefix == "" {
		for _, ev := range outcome.Events {
			switch ev.Type {
			case uno.HandsPassed:
				prefix = fmt.Sprintf("🔄 %s passou a mão para %s!", r.PlayerLink(ev.PlayerID, outcome.View), r.PlayerLink(ev.TargetID, outcome.View))
			case uno.ColorChoiceRequired:
				// Choosing a swap target is only the first half of the move:
				// tell the group the colour decision is still pending.
				if outcome.View.SwapPending {
					prefix = "🎨 Escolhendo a cor..."
				}
			case uno.SwapBlocked:
				prefix = fmt.Sprintf("🛡️ %s bloqueou a troca com 🚫 Pular!", r.PlayerLink(ev.PlayerID, outcome.View))
			case uno.PlayerSkipped:
				if ev.Reason == "block" {
					prefix = fmt.Sprintf("⏭️ %s perdeu a vez por ter bloqueado a troca.", r.PlayerLink(ev.PlayerID, outcome.View))
				}
			}
			if prefix != "" {
				break
			}
		}
	}
	if outcome.View.Closed || outcome.View.Phase == uno.Finished {
		final := r.renderClosedState(outcome.View)
		if prefix == "" {
			return final
		}
		return prefix + "\n\n" + final
	}
	turn := r.RenderTurn(outcome.View)
	if prefix == "" {
		return turn
	}
	return prefix + "\n\n" + turn
}

func (r *Renderer) RenderWelcome() string {
	return "👋 <b>Bem-vindo ao UnoKamikaze!</b>\n\n" +
		"Jogue UNO com seus amigos diretamente nos grupos do Telegram. Crie partidas e use sua mão pelo menu privado.\n\n" +
		"Use /help para conhecer todos os comandos."
}

func (r *Renderer) RenderHelp(botUsername string) string {
	var sb strings.Builder
	sb.WriteString("📖 <b>UnoKamikaze — Comandos</b>\n\n<blockquote>")
	sb.WriteString("<b>/start</b> — Mostra a apresentação do bot no privado.\n")
	orders := []commandID{cmdNew, cmdJoin, cmdClose, cmdOpen, cmdStart, cmdState, cmdRanking, cmdStats, cmdLeave, cmdSkip, cmdKick, cmdKill, cmdReset, cmdConfig, cmdHelp}
	for _, id := range orders {
		sb.WriteString(fmt.Sprintf("<b>%s</b> — %s\n", commandName(id), commandDescriptions[id]))
	}
	sb.WriteString("</blockquote>\n\n")

	sb.WriteString("Em grupos, mencionar o bot: ")
	if botUsername != "" {
		sb.WriteString(fmt.Sprintf("<code>/join@%s</code>. ", html.EscapeString(strings.TrimPrefix(botUsername, "@"))))
	} else {
		sb.WriteString("<code>/join@seubot</code>. ")
	}
	sb.WriteString("No privado, o sufixo é opcional.\n\n")

	sb.WriteString("<b>Como jogar:</b> toque em 🃏 <b>Suas cartas</b> ou digite ")
	if botUsername != "" {
		sb.WriteString(fmt.Sprintf("<code>@%s</code>. ", html.EscapeString(strings.TrimPrefix(botUsername, "@"))))
	} else {
		sb.WriteString("<code>@seubot</code>. ")
	}
	sb.WriteString("Sua mão aparece no menu inline — cartas jogáveis em cor, as demais em cinza. ")
	sb.WriteString("O grupo vê a jogada e <code>Vez de: Fulano</code>; use <code>/state</code> para o estado completo.\n\n")

	sb.WriteString("<blockquote>")
	sb.WriteString("📜 <b>Regras</b>\n")
	sb.WriteString("• <b>Objetivo:</b> zerar a mão. A partida segue até restar um jogador; a ordem de saída define a colocação.\n")
	sb.WriteString("• <b>Cartas:</b> 7 por jogador. Jogue uma carta da cor ativa ou com o mesmo número/símbolo. Coringa e +4 escolhem a cor.\n")
	sb.WriteString("• <b>Comprar:</b> sem jogada, compre 1 carta. Se ela servir, jogue-a ou passe; se não, a vez passa. Uma compra por turno.\n")
	sb.WriteString("• <b>Efeitos:</b> 🚫 pula o próximo; 🔄 inverte a direção (com 2 jogadores, funciona como 🚫); +2 e +4 fazem o próximo comprar e perder a vez.\n")
	sb.WriteString("• <b>Empilhamento:</b> +2 sobre +2, +4 sobre +4, +4 sobre +2 e +2 da cor escolhida sobre +4. Quem não empilhar compra o total acumulado.\n")
	sb.WriteString("• <b>Blefe do +4:</b> o alvo pode contestar no menu. Se o autor tinha a cor ativa, ele compra a penalidade; se não tinha, quem contestou compra a penalidade + 2 cartas.\n")
	sb.WriteString("• <b>🔀 Trocar cartas:</b> escolha um alvo (ou manter a mão) e uma cor. O alvo decide no menu: entregar a mão, bloquear com o 🚫 da cor escolhida (perde a próxima vez) ou deixar o prazo acabar — a troca acontece.\n")
	sb.WriteString("• <b>UNO:</b> anunciado automaticamente quando alguém fica com 1 carta.\n")
	sb.WriteString("• <b>Tempo:</b> sem ação no prazo, o turno é pulado; cada pulo reduz o cronômetro daquele jogador (piso de 15s). <code>/skip</code> do próprio jogador é livre; de terceiros, só após o prazo.\n")
	sb.WriteString("• <b>Limites:</b> até 10 jogadores por partida e 1 partida ativa por grupo.\n")
	sb.WriteString("• <b>Ranking:</b> mensal por grupo; <code>/settings</code> alterna entre Vitória e Colocação.")
	sb.WriteString("</blockquote>\n\n")

	sb.WriteString("🇧🇷 Esta é uma versão brasileira desenvolvida em Go (Golang), baseada no @unopybot.")
	return sb.String()
}

func groupRankingSummary(system groups.RankingSystem) (string, string) {
	switch system {
	case groups.Updated:
		return "🏆 Colocação", "Os pontos seguem a posição final: quanto melhor, mais pontos."
	case groups.Legacy:
		fallthrough
	default:
		return "🏆 Vitória", "Quem não ficou em último recebe +1 ponto."
	}
}

// RenderGroupConfig formats the current group configuration for the /settings command.
func (r *Renderer) RenderGroupConfig(config groups.Config) string {
	rankTitle, rankDesc := groupRankingSummary(config.RankingSystem)
	return fmt.Sprintf("⚙️ <b>Configuração do Grupo</b>\n\n"+
		"<blockquote><b>%s</b>\n<i>%s</i></blockquote>\n\n"+
		"Selecione abaixo para alterar.\n"+
		"As mudanças afetarão apenas as próximas partidas criadas.",
		rankTitle, rankDesc)
}

// renderGlobalStats formats the aggregate across every group. The photo
// already carries games and wins, so the caption keeps only the remaining
// history; the text fallback has no photo and shows all of it.
func renderGlobalStats(stats ranking.PlayerStats, includeGames bool) string {
	var sb strings.Builder
	sb.WriteString("📊 <b>Suas estatísticas globais</b>\n\n")
	if !stats.HasHistory() {
		sb.WriteString("Você ainda não tem partidas pontuadas em nenhum grupo.")
		return sb.String()
	}
	sb.WriteString("<blockquote>")
	if includeGames {
		sb.WriteString(fmt.Sprintf("🎮 <b>Partidas pontuadas:</b> %d\n", stats.Games))
		sb.WriteString(fmt.Sprintf("🏆 <b>Vitórias:</b> %d\n", stats.Wins))
	}
	sb.WriteString(fmt.Sprintf("💠 <b>Pontos:</b> %s\n", ranking.FormatScore(stats.System, stats.Score)))
	if stats.BestPosition > 0 {
		sb.WriteString(fmt.Sprintf("🥇 <b>Melhor colocação:</b> %dº\n", stats.BestPosition))
	}
	if stats.AvgPosition != "" {
		sb.WriteString(fmt.Sprintf("📉 <b>Colocação média:</b> %s\n", stats.AvgPosition))
	}
	sb.WriteString("</blockquote>\n\n")
	sb.WriteString("Soma das partidas pontuadas em todos os grupos.")
	if !includeGames {
		sb.WriteString(" Não há posição global: o ranking é mensal por grupo — use <code>/ranking</code> para ver os seus.")
	}
	return sb.String()
}

// RenderPlayerStats formats the accumulated history of one player.
func (r *Renderer) RenderPlayerStats(stats ranking.PlayerStats, scope string) string {
	if scope == "" {
		return renderGlobalStats(stats, true)
	}
	if !stats.HasHistory() {
		return fmt.Sprintf("📊 <b>Suas estatísticas em %s</b>\n\nVocê ainda não tem partidas pontuadas aqui.", scope)
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📊 <b>Suas estatísticas em %s</b>\n\n", scope))
	sb.WriteString(fmt.Sprintf("Partidas pontuadas: %d\n", stats.Games))
	sb.WriteString(fmt.Sprintf("Vitórias: %d\n", stats.Wins))
	sb.WriteString(fmt.Sprintf("Pontos: %s\n", ranking.FormatScore(stats.System, stats.Score)))
	if stats.BestPosition > 0 {
		sb.WriteString(fmt.Sprintf("Melhor colocação: %dº\n", stats.BestPosition))
	}
	if stats.AvgPosition != "" {
		sb.WriteString(fmt.Sprintf("Colocação média: %s\n", stats.AvgPosition))
	}
	if stats.Rank > 0 && stats.Total > 0 {
		sb.WriteString(fmt.Sprintf("Posição no ranking: %d de %d\n", stats.Rank, stats.Total))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// RenderStatsCaption carries the stats that do not fit on the photo. The card
// itself shows games and wins; the caption keeps the remaining history visible.
func (r *Renderer) RenderStatsCaption(stats ranking.PlayerStats, scope string) string {
	if scope == "" {
		return renderGlobalStats(stats, false)
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📊 <b>Suas estatísticas em %s</b>\n\n", html.EscapeString(scope)))
	if !stats.HasHistory() {
		sb.WriteString("Você ainda não tem partidas pontuadas aqui.")
		return sb.String()
	}
	sb.WriteString(fmt.Sprintf("Pontos: %s\n", ranking.FormatScore(stats.System, stats.Score)))
	if stats.BestPosition > 0 {
		sb.WriteString(fmt.Sprintf("Melhor colocação: %dº\n", stats.BestPosition))
	}
	if stats.AvgPosition != "" {
		sb.WriteString(fmt.Sprintf("Colocação média: %s\n", stats.AvgPosition))
	}
	if stats.Rank > 0 && stats.Total > 0 {
		sb.WriteString(fmt.Sprintf("Posição no ranking: %d de %d", stats.Rank, stats.Total))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// RenderGroupWelcome formats the introductory message when the bot joins a group.
func (r *Renderer) RenderGroupWelcome(config groups.Config) string {
	rankLabel := "Vitória"
	if config.RankingSystem == groups.Updated {
		rankLabel = "Colocação"
	}
	return fmt.Sprintf("👋 <b>Olá! Obrigado por adicionar o UnoKamikaze ao grupo!</b>\n\n"+
		"O bot já está pronto para jogar. As partidas usam o modo <b>Caseiro</b> e o ranking <b>%s</b>.\n\n"+
		"Use /new para iniciar uma partida ou clique no botão abaixo para personalizar as opções do grupo.", rankLabel)
}
