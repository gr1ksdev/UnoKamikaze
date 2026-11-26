package telegram

import (
	"strings"

	"github.com/mymmrac/telego"
)

// commandID is a transport-independent name for an action. Every group and
// private chat accepts the same English spelling; bot texts stay Portuguese.
type commandID string

const (
	cmdNew     commandID = "new"
	cmdJoin    commandID = "join"
	cmdStart   commandID = "start"
	cmdLeave   commandID = "leave"
	cmdClose   commandID = "close"
	cmdOpen    commandID = "open"
	cmdKill    commandID = "kill"
	cmdSkip    commandID = "skip"
	cmdKick    commandID = "kick"
	cmdState   commandID = "state"
	cmdRanking commandID = "ranking"
	cmdStats   commandID = "stats"
	cmdConfig  commandID = "settings"
	cmdReset   commandID = "reset"
	cmdHelp    commandID = "help"
)

var commandNames = map[string]commandID{
	"new":      cmdNew,
	"join":     cmdJoin,
	"start":    cmdStart,
	"leave":    cmdLeave,
	"close":    cmdClose,
	"open":     cmdOpen,
	"kill":     cmdKill,
	"skip":     cmdSkip,
	"kick":     cmdKick,
	"state":    cmdState,
	"ranking":  cmdRanking,
	"stats":    cmdStats,
	"settings": cmdConfig,
	"reset":    cmdReset,
	"help":     cmdHelp,
}

var commandSpelling = map[commandID]string{
	cmdNew:     "/new",
	cmdJoin:    "/join",
	cmdStart:   "/start",
	cmdLeave:   "/leave",
	cmdClose:   "/close",
	cmdOpen:    "/open",
	cmdKill:    "/kill",
	cmdSkip:    "/skip",
	cmdKick:    "/kick",
	cmdState:   "/state",
	cmdRanking: "/ranking",
	cmdStats:   "/stats",
	cmdConfig:  "/settings",
	cmdReset:   "/reset",
	cmdHelp:    "/help",
}

// Menu descriptions are Portuguese: only the command names are English.
var commandDescriptions = map[commandID]string{
	cmdNew:     "Criar uma nova partida de UNO",
	cmdJoin:    "Entrar na partida de UNO",
	cmdStart:   "Iniciar a partida",
	cmdLeave:   "Sair da partida em andamento",
	cmdClose:   "Impede novos jogadores de entrar",
	cmdOpen:    "Permite novas entradas",
	cmdKill:    "Cancelar a partida",
	cmdSkip:    "Pular o turno do jogador atual",
	cmdKick:    "Expulsar um jogador da partida",
	cmdState:   "Ver o estado atual da partida",
	cmdRanking: "Ver o ranking acumulado do grupo",
	cmdStats:   "Ver suas estatísticas no grupo",
	cmdConfig:  "Configurar o ranking do grupo",
	cmdReset:   "Limpa partidas perdidas ou inacabadas",
	cmdHelp:    "Ver comandos e instruções",
}

var groupMenuOrder = []commandID{
	cmdNew, cmdJoin, cmdStart, cmdLeave, cmdClose, cmdOpen,
	cmdKill, cmdSkip, cmdKick, cmdState, cmdRanking, cmdStats,
	cmdConfig, cmdReset, cmdHelp,
}

func resolveCommand(raw string) (commandID, bool) {
	id, ok := commandNames[strings.ToLower(strings.TrimSpace(raw))]
	return id, ok
}

func commandName(id commandID) string {
	if name, ok := commandSpelling[id]; ok {
		return name
	}
	return string(id)
}

func commandList() []telego.BotCommand {
	commands := make([]telego.BotCommand, 0, len(groupMenuOrder))
	for _, id := range groupMenuOrder {
		commands = append(commands, telego.BotCommand{
			Command:     strings.TrimPrefix(commandName(id), "/"),
			Description: commandDescriptions[id],
		})
	}
	return commands
}

func groupCommandNames() []string {
	names := make([]string, 0, len(groupMenuOrder))
	for _, id := range groupMenuOrder {
		names = append(names, strings.TrimPrefix(commandName(id), "/"))
	}
	return names
}

func privateCommandList() []telego.BotCommand {
	return []telego.BotCommand{
		{Command: "start", Description: "Conhecer o UnoKamikaze"},
		{Command: "help", Description: "Ver comandos e instruções"},
		{Command: "ranking", Description: "Ver seus rankings mensais"},
		{Command: "stats", Description: "Ver suas estatísticas"},
	}
}
