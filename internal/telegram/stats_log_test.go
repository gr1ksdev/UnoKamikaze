package telegram

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestLogSlowOnlyAboveThreshold(t *testing.T) {
	var buf bytes.Buffer
	h := NewCommandHandler(nil, nil, nil, nil, "", slog.New(slog.NewTextHandler(&buf, nil)))

	h.logSlow(context.Background(), "render", time.Now().Add(-time.Second), "chat_id", -1)
	if !strings.Contains(buf.String(), "slow stats stage") || !strings.Contains(buf.String(), "stage=render") {
		t.Fatalf("slow stage not logged: %s", buf.String())
	}

	buf.Reset()
	h.logSlow(context.Background(), "render", time.Now(), "chat_id", -1)
	if buf.Len() != 0 {
		t.Fatalf("fast stage should not log: %s", buf.String())
	}
}
