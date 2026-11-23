package game

import (
	"fmt"
	"testing"
	"time"

	"github.com/gr1ksdev/UnoKamikaze/internal/uno"
)

// blockDeck deals the swap card to player 1 and the red Skip to player 2.
func blockService(t *testing.T) (*Service, PublicGameView) {
	t.Helper()
	s := testService(t)
	s.manager.newGame = func(id uno.GameID, rules uno.Rules) (*uno.Game, error) {
		cards := []uno.Card{
			{ID: "swap", Rank: uno.SwapHands},
			{ID: "p2red", Color: uno.Red, Rank: uno.Zero},
			{ID: "p1blue", Color: uno.Blue, Rank: uno.One},
			{ID: "p2skip", Color: uno.Red, Rank: uno.Skip},
		}
		for i := len(cards); i < 14; i++ {
			cards = append(cards, uno.Card{ID: uno.CardID(fmt.Sprintf("fill%d", i)), Color: uno.Blue, Rank: uno.Nine})
		}
		cards = append(cards, uno.Card{ID: "top", Color: uno.Red, Rank: uno.Nine})
		return uno.NewGame(id, rules, uno.WithDeck(cards), uno.WithShuffler(func([]uno.CardID) {}))
	}
	v := create(t, s, -100, 1, uno.CaseiroRules())
	join(t, s, v, 1)
	join(t, s, v, 2)
	act(t, s, v.GameID, Actor{PlayerID: 1, ChatID: -100}, uno.Action{Type: uno.StartGame, DealerID: 2})
	act(t, s, v.GameID, Actor{PlayerID: 1}, uno.Action{Type: uno.PlayCard, CardID: "swap"})
	act(t, s, v.GameID, Actor{PlayerID: 1}, uno.Action{Type: uno.ChoosePlayer, TargetID: 2})
	return s, v
}

func TestServiceSwapBlockManualDecision(t *testing.T) {
	s, v := blockService(t)
	colored := act(t, s, v.GameID, Actor{PlayerID: 1}, uno.Action{Type: uno.ChooseColor, Color: uno.Red})
	if colored.View.Phase != uno.BlockingSwap || colored.View.BlockTargetID != 2 || colored.View.BlockTurnID != 2 || colored.View.BlockColor != uno.Red {
		t.Fatalf("block window not published: %+v", colored.View)
	}
	rev := colored.View.Revision
	denied(t, s, v.GameID, Actor{PlayerID: 1}, uno.Action{Type: uno.BlockSwap, PlayerID: 1, Revision: rev}, uno.ErrNotYourTurn)

	// The target's own view still shows only its own hand.
	targetView, err := s.PlayerView(t.Context(), Actor{PlayerID: 2}, v.GameID)
	if err != nil {
		t.Fatal(err)
	}
	if len(targetView.Hand) != 7 {
		t.Fatalf("target hand changed before the decision: %d", len(targetView.Hand))
	}

	blocked := act(t, s, v.GameID, Actor{PlayerID: 2}, uno.Action{Type: uno.BlockSwap})
	if blocked.View.Phase != uno.TakingTurn || blocked.View.BlockTargetID != 0 {
		t.Fatalf("block not resolved: %+v", blocked.View)
	}
	var swapped, announced bool
	for _, ev := range blocked.Events {
		swapped = swapped || ev.Type == uno.HandsSwapped
		announced = announced || ev.Type == uno.SwapBlocked
	}
	if swapped || !announced {
		t.Fatalf("unexpected events: %+v", blocked.Events)
	}
}

func TestServiceSwapBlockPassHandTransfers(t *testing.T) {
	s, v := blockService(t)
	colored := act(t, s, v.GameID, Actor{PlayerID: 1}, uno.Action{Type: uno.ChooseColor, Color: uno.Red})
	if colored.View.Phase != uno.BlockingSwap {
		t.Fatalf("block window not published: %+v", colored.View)
	}
	rev := colored.View.Revision
	denied(t, s, v.GameID, Actor{PlayerID: 1}, uno.Action{Type: uno.PassHand, PlayerID: 1, Revision: rev}, uno.ErrNotYourTurn)

	passed := act(t, s, v.GameID, Actor{PlayerID: 2}, uno.Action{Type: uno.PassHand})
	if passed.View.Phase != uno.TakingTurn || passed.View.BlockTargetID != 0 {
		t.Fatalf("pass hand not resolved: %+v", passed.View)
	}
	var transferred, announced bool
	for _, ev := range passed.Events {
		transferred = transferred || ev.Type == uno.HandsSwapped
		announced = announced || ev.Type == uno.HandsPassed
	}
	if !transferred || !announced {
		t.Fatalf("unexpected events: %+v", passed.Events)
	}
}

func TestServiceSwapBlockTimeoutAllowsSwap(t *testing.T) {
	s, v := blockService(t)
	act(t, s, v.GameID, Actor{PlayerID: 1}, uno.Action{Type: uno.ChooseColor, Color: uno.Red})
	entry := s.manager.byID[v.GameID].entry
	entry.turnStarted = time.Now().Add(-time.Hour)

	var candidate *ExpiredTurn
	for _, c := range s.ExpiredTurns(t.Context(), time.Second) {
		if c.Block {
			copy := c
			candidate = &copy
		}
	}
	if candidate == nil || candidate.PlayerID != 2 {
		t.Fatalf("missing block candidate: %+v", candidate)
	}
	outcome, applied := s.AutoSkipTurn(t.Context(), *candidate, time.Second)
	if !applied || outcome.View.Phase != uno.TakingTurn {
		t.Fatalf("timeout did not allow the swap: %+v, %v", outcome, applied)
	}
	var swapped bool
	for _, ev := range outcome.Events {
		swapped = swapped || ev.Type == uno.HandsSwapped
	}
	if !swapped {
		t.Fatalf("timeout did not transfer hands: %+v", outcome.Events)
	}
}
