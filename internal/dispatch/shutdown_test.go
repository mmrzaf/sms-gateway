package dispatch

import (
	"context"
	"testing"
	"time"

	"github.com/mmrzaf/sms-gatway/internal/message"
	"github.com/mmrzaf/sms-gatway/internal/testutil"
)

func TestShutdownCancelsBlockedCompletion(t *testing.T) {
	db := testutil.DB(t)
	svc := newMessages(db, 4)
	customer, _ := testutil.Customer(t, db, 100)
	m := accept(t, svc, customer.ID, message.Normal, "+989121234567")
	blocker, err := db.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	if _, err := blocker.Exec(context.Background(), `SELECT id FROM messages WHERE id = $1 FOR UPDATE`, m.ID); err != nil {
		t.Fatal(err)
	}
	provider := newScripted(t, ok)
	cfg := testConfig(provider.srv.URL)
	cfg.ShutdownTimeout = 100 * time.Millisecond
	w := New(cfg, db, discard)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	waitFor(t, "provider acceptance", func() bool { return len(provider.received()) > 0 })
	// The message stays locked until after Run returns.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown blocked on the completion transaction")
	}
	if err := blocker.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := reload(t, svc, m); got.Status != message.StatusAccepted {
		t.Fatalf("blocked completion persisted: %+v", got)
	}
	// Uncommitted work stays queued for lease recovery.
	var queued bool
	if err := db.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM queue WHERE message_id = $1)`, m.ID).Scan(&queued); err != nil || !queued {
		t.Fatalf("recovery queue: %t, %v", queued, err)
	}
}
