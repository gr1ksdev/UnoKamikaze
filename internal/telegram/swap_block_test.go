package telegram

import (
	"strings"
	"testing"
	"time"

	"github.com/gr1ksdev/UnoKamikaze/internal/game"
	"github.com/gr1ksdev/UnoKamikaze/internal/uno"
	"github.com/mymmrac/telego"
)

func TestSwapBlockMenuShowsCardsAndProtectsTokens(t *testing.T) {
	renderer := NewRenderer(nil)
	renderer.SetBotID(999)
	renderer.userCache.Put(1, "Attacker", "")
	renderer.userCache.Put(2, "Victim", "")
	renderer.userCache.Put(3, "Next", "")
	tokens := NewTokenStore(100, 100, time.Now, nil)
	h := NewInlineHandler(newMockBotAPI(), nil, renderer, tokens, time.Minute, nil, nil)
	view := game.PlayerGameView{
		PlayerID: 2,
		Hand: []game.CardView{
			{Card: uno.Card{ID: "blue", Color: uno.Blue, Rank: uno.Nine}},
			{Card: uno.Card{ID: "skip", Color: uno.Red, Rank: uno.Skip}},
		},
		Public: game.PublicGameView{
			GameID: "g", ChatID: -100, Revision: 7, Phase: uno.BlockingSwap,
			CurrentTurn: 1, ColorChooserID: 1, BlockTargetID: 2, BlockTurnID: 3, BlockColor: uno.Red,
		},
	}

	// The target sees its hand as stickers: the pass sticker hands the cards
	// over, the matching Skip blocks and the other card is grey.
	results, next := h.swapBlockResults(2, view, "")
	if next != "" || len(results) != 3 {
		t.Fatalf("expected two cards and the permit sticker: %d, next=%q", len(results), next)
	}
	var block, allow *telego.InlineQueryResultCachedSticker
	greys := 0
	for _, result := range results {
		sticker, ok := result.(*telego.InlineQueryResultCachedSticker)
		if !ok {
			t.Fatalf("block menu should use stickers only: %+v", result)
		}
		switch sticker.StickerFileID {
		case GetCardStickerID(uno.Card{Color: uno.Red, Rank: uno.Skip}):
			block = sticker
		case Stickers["option_pass"]:
			allow = sticker
		case GetCardStickerGreyID(uno.Card{Color: uno.Blue, Rank: uno.Nine}):
			greys++
			if !strings.HasPrefix(sticker.ID, "grey_") {
				t.Fatalf("non-Skip card is actionable: %+v", sticker)
			}
		default:
			t.Fatalf("unexpected sticker: %+v", sticker)
		}
	}
	if block == nil || allow == nil || greys != 1 {
		t.Fatalf("expected Skip, permit and one grey card: %+v", results)
	}
	first := results[0].(*telego.InlineQueryResultCachedSticker)
	if first.StickerFileID != Stickers["option_pass"] {
		t.Fatalf("hand-off sticker must come first: %+v", first)
	}

	if _, status := tokens.ConsumeAction(block.ID, 1); status != ConsumeUserMismatch {
		t.Fatalf("attacker consumed the target's block token: %v", status)
	}
	for _, tc := range []struct {
		id   string
		kind uno.ActionType
	}{
		{block.ID, uno.BlockSwap},
		{allow.ID, uno.PassHand},
	} {
		token, status := tokens.ConsumeAction(tc.id, 2)
		if status != ConsumeOK || token.Action.Type != tc.kind || token.Action.Revision != 7 || token.ChatID != -100 {
			t.Fatalf("bad block token: %+v, %v", token, status)
		}
	}

	waiting, _ := h.swapBlockResults(1, view, "")
	if len(waiting) != 2 || !strings.HasPrefix(waiting[0].(*telego.InlineQueryResultArticle).ID, "wait_") {
		t.Fatalf("attacker should see a waiting notice and their own hand: %+v", waiting)
	}
	if strings.Contains(waiting[0].(*telego.InlineQueryResultArticle).Description, "skip") {
		t.Fatal("target's card leaked to the attacker")
	}

	// The victim is never named as the decision holder: only the player whose
	// turn resumes gets a real mention, and no message names the Skip card.
	for _, id := range []uno.PlayerID{1, 2} {
		if !strings.Contains(renderer.PlayerLink(id, view.Public), "id=999") {
			t.Fatalf("block window exposed a real mention for %d", id)
		}
	}
	if !strings.Contains(renderer.PlayerLink(3, view.Public), "id=3") {
		t.Fatal("the next player should be mentioned as the turn holder")
	}
	offer := renderer.RenderActionConfirmation(1, uno.Action{Type: uno.ChooseColor, Color: uno.Red}, game.Outcome{
		View:   view.Public,
		Events: []uno.Event{{Type: uno.SwapBlockRequired, PlayerID: 2, Color: uno.Red}},
	})
	if !strings.Contains(offer, "Vez de:") || !strings.Contains(offer, "Next") || strings.Contains(offer, "bloquear") {
		t.Fatalf("wrong turn message: %s", offer)
	}
	if state := renderer.RenderPublicState(view.Public); strings.Contains(state, "bloquear") || !strings.Contains(state, "Vez de:") || !strings.Contains(state, "Next") {
		t.Fatalf("wrong public state: %s", state)
	}
	blocked := renderer.RenderActionConfirmation(2, uno.Action{Type: uno.BlockSwap}, game.Outcome{
		View:   view.Public,
		Events: []uno.Event{{Type: uno.SwapBlocked, PlayerID: 2, TargetID: 1}},
	})
	if !strings.Contains(blocked, "bloqueou a troca") {
		t.Fatalf("wrong block result message: %s", blocked)
	}
	passed := renderer.RenderActionConfirmation(2, uno.Action{Type: uno.PassHand}, game.Outcome{
		View:   view.Public,
		Events: []uno.Event{{Type: uno.HandsPassed, PlayerID: 2, TargetID: 1}, {Type: uno.HandsSwapped, PlayerID: 1, TargetID: 2}},
	})
	if !strings.Contains(passed, "passou a mão") {
		t.Fatalf("wrong hand-off message: %s", passed)
	}
}

func TestSwapHandOffAvailableWithoutTheBlockCard(t *testing.T) {
	renderer := NewRenderer(nil)
	renderer.SetBotID(999)
	renderer.userCache.Put(2, "Victim", "")
	tokens := NewTokenStore(100, 100, time.Now, nil)
	h := NewInlineHandler(newMockBotAPI(), nil, renderer, tokens, time.Minute, nil, nil)
	view := game.PlayerGameView{
		PlayerID: 2,
		Hand: []game.CardView{
			{Card: uno.Card{ID: "blue", Color: uno.Blue, Rank: uno.Nine}},
			{Card: uno.Card{ID: "yellow", Color: uno.Yellow, Rank: uno.Skip}},
		},
		Public: game.PublicGameView{
			GameID: "g", ChatID: -100, Revision: 7, Phase: uno.BlockingSwap,
			BlockTargetID: 2, BlockColor: uno.Red,
		},
	}
	results, _ := h.swapBlockResults(2, view, "")
	if len(results) != 3 {
		t.Fatalf("expected hand-off plus two grey cards: %d", len(results))
	}
	first := results[0].(*telego.InlineQueryResultCachedSticker)
	if first.StickerFileID != Stickers["option_pass"] {
		t.Fatalf("hand-off sticker must come first: %+v", first)
	}
	token, status := tokens.ConsumeAction(first.ID, 2)
	if status != ConsumeOK || token.Action.Type != uno.PassHand {
		t.Fatalf("bad hand-off token: %+v, %v", token, status)
	}
	for _, result := range results[1:] {
		card := result.(*telego.InlineQueryResultCachedSticker)
		if !strings.HasPrefix(card.ID, "grey_") {
			t.Fatalf("card without the Skip should be grey: %+v", card)
		}
	}
}

func TestSwapTargetSelectionAnnouncesTheColorStep(t *testing.T) {
	r := NewRenderer(nil)
	r.SetBotID(999)
	r.userCache.Put(1, "Alice", "")
	r.userCache.Put(2, "Bob", "")
	view := game.PublicGameView{
		GameID: "g", ChatID: -100, Revision: 7, Phase: uno.ChoosingColor,
		CurrentTurn: 1, ColorChooserID: 1, SwapPending: true,
	}
	out := game.Outcome{
		View:   view,
		Events: []uno.Event{{Type: uno.ColorChoiceRequired, PlayerID: 1}},
	}
	// The target-selection action itself only announces the pending colour.
	text := r.RenderActionConfirmation(1, uno.Action{Type: uno.ChoosePlayer, TargetID: 2}, out)
	if !strings.Contains(text, "🎨 Escolhendo a cor...") || !strings.Contains(text, "Vez de:") {
		t.Fatalf("missing colour-step message: %s", text)
	}
	if !strings.Contains(r.PlayerLink(1, view), "id=1") {
		t.Fatal("wrong mention for the colour chooser")
	}
}
