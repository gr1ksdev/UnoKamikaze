package uno

import (
	"slices"
	"testing"
)

func blockScenario(t *testing.T) *Game {
	t.Helper()
	return scenario(t, CaseiroRules(), [][]Card{
		{card(NoColor, SwapHands), card(Blue, One)},
		{card(Red, Five)},
		{card(Red, Skip), card(Yellow, Three)},
	}, card(Red, Nine), nil)
}

func reachBlockWindow(t *testing.T, g *Game) {
	t.Helper()
	apply(t, g, Action{Type: PlayCard, PlayerID: 1, CardID: g.Snapshot().Players[0].Hand[0]})
	apply(t, g, Action{Type: ChoosePlayer, PlayerID: 1, TargetID: 3})
	r := apply(t, g, Action{Type: ChooseColor, PlayerID: 1, Color: Red})
	if s := g.Snapshot(); s.Phase != BlockingSwap || s.Pending == nil || s.Pending.ChosenColor != Red {
		t.Fatalf("block window not open: %+v", s)
	}
	if !hasEvent(r, SwapBlockRequired, 3) {
		t.Fatalf("missing block offer: %+v", r)
	}
}

func TestSwapBlockCancelsTransferAndSkipsVictimLater(t *testing.T) {
	g := blockScenario(t)
	before := g.Snapshot()
	reachBlockWindow(t, g)

	// Only the target may decide, and only with the Skip of the chosen colour.
	rev := g.Snapshot().Revision
	rejected(t, g, Action{Type: BlockSwap, PlayerID: 2, Revision: rev}, ErrNotYourTurn)
	rejected(t, g, Action{Type: DrawCard, PlayerID: 3, Revision: rev}, ErrSwapBlockRequired)

	r := apply(t, g, Action{Type: BlockSwap, PlayerID: 3})
	after := g.Snapshot()
	if after.Phase != TakingTurn || after.Pending != nil || after.ActiveColor != Red {
		t.Fatalf("bad resolved state: %+v", after)
	}
	if !slices.Equal(after.Players[0].Hand, before.Players[0].Hand[1:]) || !slices.Equal(after.Players[2].Hand, before.Players[2].Hand[1:]) {
		t.Fatalf("hands should stay with their owners: %+v", after.Players)
	}
	if after.CurrentPlayerID != 2 || after.SkipNextFor != 3 {
		t.Fatalf("wrong turn or banked skip: %+v", after)
	}
	catalog := make(map[CardID]Card, len(after.Cards))
	for _, c := range after.Cards {
		catalog[c.ID] = c
	}
	top := catalog[after.DiscardPile[len(after.DiscardPile)-1]]
	if top.Rank != Skip || top.Color != Red {
		t.Fatalf("Skip was not discarded on top: %+v", top)
	}
	if !hasEvent(r, SwapBlocked, 3) || !hasEvent(r, TurnChanged, 2) {
		t.Fatalf("missing block events: %+v", r)
	}

	// The turn returns to the swapper's successor; when it would reach the
	// victim again, the banked skip consumes that turn.
	apply(t, g, Action{Type: PlayCard, PlayerID: 2, CardID: g.Snapshot().Players[1].Hand[0]})
	final := g.Snapshot()
	if final.CurrentPlayerID != 1 || final.SkipNextFor != 0 || !slices.ContainsFunc(g.Snapshot().Players, func(p Player) bool { return p.ID == 3 && p.Status == Playing }) {
		t.Fatalf("banked self-skip not consumed: %+v", final)
	}
}

func TestSwapPassHandTransfersHands(t *testing.T) {
	g := blockScenario(t)
	before := g.Snapshot()
	reachBlockWindow(t, g)

	rev := g.Snapshot().Revision
	rejected(t, g, Action{Type: PassHand, PlayerID: 2, Revision: rev}, ErrNotYourTurn)

	r := apply(t, g, Action{Type: PassHand, PlayerID: 3})
	after := g.Snapshot()
	if !slices.Equal(after.Players[0].Hand, before.Players[2].Hand) || !slices.Equal(after.Players[2].Hand, before.Players[0].Hand[1:]) {
		t.Fatalf("pass hand did not transfer: %+v", after.Players)
	}
	if after.SkipNextFor != 0 || after.CurrentPlayerID != 2 || !hasEvent(r, HandsSwapped, 1) || !hasEvent(r, HandsPassed, 3) {
		t.Fatalf("bad pass outcome: %+v, %+v", after, r)
	}
}

func TestSwapAllowOnTimeoutStillTransfersHands(t *testing.T) {
	g := blockScenario(t)
	before := g.Snapshot()
	reachBlockWindow(t, g)

	r := apply(t, g, Action{Type: AllowSwap, PlayerID: 3})
	after := g.Snapshot()
	if !slices.Equal(after.Players[0].Hand, before.Players[2].Hand) || !slices.Equal(after.Players[2].Hand, before.Players[0].Hand[1:]) {
		t.Fatalf("allow did not transfer: %+v", after.Players)
	}
	if after.SkipNextFor != 0 || after.CurrentPlayerID != 2 || !hasEvent(r, HandsSwapped, 1) {
		t.Fatalf("bad allow outcome: %+v, %+v", after, r)
	}
}

func TestSwapWithoutBlockCardStillWaitsForTheTarget(t *testing.T) {
	g := scenario(t, CaseiroRules(), [][]Card{
		{card(NoColor, SwapHands), card(Blue, One)},
		{card(Red, Five)},
		{card(Yellow, Skip), card(Yellow, Three)},
	}, card(Red, Nine), nil)
	before := g.Snapshot()
	apply(t, g, Action{Type: PlayCard, PlayerID: 1, CardID: before.Players[0].Hand[0]})
	apply(t, g, Action{Type: ChoosePlayer, PlayerID: 1, TargetID: 3})
	r := apply(t, g, Action{Type: ChooseColor, PlayerID: 1, Color: Red})
	if s := g.Snapshot(); s.Phase != BlockingSwap || !hasEvent(r, SwapBlockRequired, 3) {
		t.Fatalf("target should decide even without the Skip: %+v, %+v", s, r)
	}

	r = apply(t, g, Action{Type: PassHand, PlayerID: 3})
	after := g.Snapshot()
	if after.Phase != TakingTurn || after.Pending != nil || !hasEvent(r, HandsSwapped, 1) {
		t.Fatalf("hand-off did not resolve: %+v, %+v", after, r)
	}
}

func TestSwapBlockAsLastCardWins(t *testing.T) {
	g := scenario(t, CaseiroRules(), [][]Card{
		{card(NoColor, SwapHands), card(Blue, One)},
		{card(Red, Five)},
		{card(Red, Skip)},
	}, card(Red, Nine), nil)
	reachBlockWindow(t, g)
	r := apply(t, g, Action{Type: BlockSwap, PlayerID: 3})
	if !hasEvent(r, PlayerWon, 3) || !hasEvent(r, CardPlayed, 3) {
		t.Fatalf("last Skip should win: %+v", r)
	}
	if s := g.Snapshot(); s.SkipNextFor != 0 || s.CurrentPlayerID != 2 {
		t.Fatalf("winner should not be banked for a skip: %+v", s)
	}
}
