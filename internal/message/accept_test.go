package message_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mmrzaf/sms-gatway/internal/billing"
	"github.com/mmrzaf/sms-gatway/internal/message"
	"github.com/mmrzaf/sms-gatway/internal/testutil"
)

func newService(pool *pgxpool.Pool) *message.Service {
	return message.NewService(pool, message.Config{
		Prices:      message.Prices{Normal: 1, Express: 3},
		MaxSegments: 10,
		NormalLanes: 16,
		NormalTTL:   24 * time.Hour,
		ExpressTTL:  5 * time.Minute,
	})
}

func ref(s string) *string { return &s }

func balance(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) int64 {
	t.Helper()
	b, err := billing.GetBalance(context.Background(), pool, id)
	if err != nil {
		t.Fatal(err)
	}
	return b.Amount
}

func count(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAcceptRecordsMessageDebitAndQueueRow(t *testing.T) {
	pool := testutil.DB(t)
	svc := newService(pool)
	c, _ := testutil.Customer(t, pool, 10)
	ctx := context.Background()

	m, replayed, err := svc.Accept(ctx, c.ID, message.Request{
		To: "+989121234567", Text: strings.Repeat("س", 71), Type: message.Express, ClientRef: ref("otp-1"),
	})
	if err != nil || replayed {
		t.Fatalf("accept: %v, replayed %v", err, replayed)
	}
	if m.Status != message.StatusAccepted || m.Encoding != "ucs2" || m.Segments != 2 || m.Cost != 6 {
		t.Errorf("unexpected message: %+v", m)
	}
	if got := m.ExpiresAt.Sub(m.AcceptedAt); got != 5*time.Minute {
		t.Errorf("express TTL = %v", got)
	}
	if got := balance(t, pool, c.ID); got != 4 {
		t.Errorf("balance = %d, want 4", got)
	}
	var lane string
	if err := pool.QueryRow(ctx, `SELECT lane FROM queue WHERE message_id = $1`, m.ID).Scan(&lane); err != nil || lane != message.ExpressLane {
		t.Errorf("queue lane = %q, %v", lane, err)
	}
	testutil.AssertLedgerConsistent(t, pool)
}

// IT01: overspend under concurrency.
func TestConcurrentAcceptsNeverOverspend(t *testing.T) {
	pool := testutil.DB(t)
	svc := newService(pool)
	c, _ := testutil.Customer(t, pool, 100)

	var accepted, refused, other int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for range 1000 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := svc.Accept(context.Background(), c.ID, message.Request{To: "+989121234567", Text: "hi"})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				accepted++
			case errors.Is(err, billing.ErrInsufficientCredits):
				refused++
			default:
				other++
				t.Log(err)
			}
		}()
	}
	wg.Wait()

	if accepted != 100 || refused != 900 || other != 0 {
		t.Errorf("accepted %d, refused %d, other errors %d; want 100, 900, 0", accepted, refused, other)
	}
	if got := balance(t, pool, c.ID); got != 0 {
		t.Errorf("balance = %d, want 0", got)
	}
	if n := count(t, pool, `SELECT count(*) FROM queue`); n != 100 {
		t.Errorf("%d queue rows, want 100", n)
	}
	testutil.AssertLedgerConsistent(t, pool)
}

// IT02: charges and sends interleaved.
func TestChargesAndSendsInterleaved(t *testing.T) {
	pool := testutil.DB(t)
	svc := newService(pool)
	c, _ := testutil.Customer(t, pool, 0)
	ctx := context.Background()

	var wg sync.WaitGroup
	var mu sync.Mutex
	var accepted int
	for range 500 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, err := billing.ChargeCustomer(ctx, pool, c.ID, 1, "topup-"+uuid.NewString())
			if err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			_, _, err := svc.Accept(ctx, c.ID, message.Request{To: "+989121234567", Text: "hi"})
			if err == nil {
				mu.Lock()
				accepted++
				mu.Unlock()
			} else if !errors.Is(err, billing.ErrInsufficientCredits) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	if got, want := balance(t, pool, c.ID), int64(500-accepted); got != want {
		t.Errorf("balance = %d, want %d (500 charged, %d accepted)", got, want, accepted)
	}
	testutil.AssertLedgerConsistent(t, pool)
}

// IT03: concurrent duplicate client_ref.
func TestConcurrentDuplicateClientRef(t *testing.T) {
	pool := testutil.DB(t)
	svc := newService(pool)
	c, _ := testutil.Customer(t, pool, 100)

	var wg sync.WaitGroup
	var mu sync.Mutex
	ids := make(map[uuid.UUID]bool)
	var replays int
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m, replayed, err := svc.Accept(context.Background(), c.ID,
				message.Request{To: "+989121234567", Text: "hi", ClientRef: ref("same")})
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			ids[m.ID] = true
			if replayed {
				replays++
			}
		}()
	}
	wg.Wait()

	if len(ids) != 1 || replays != 49 {
		t.Errorf("%d distinct messages, %d replays; want 1 and 49", len(ids), replays)
	}
	if got := balance(t, pool, c.ID); got != 99 {
		t.Errorf("balance = %d, want 99", got)
	}
	testutil.AssertLedgerConsistent(t, pool)
}

// IT04: client_ref conflict.
func TestClientRefConflict(t *testing.T) {
	pool := testutil.DB(t)
	svc := newService(pool)
	c, _ := testutil.Customer(t, pool, 100)
	ctx := context.Background()

	if _, _, err := svc.Accept(ctx, c.ID, message.Request{To: "+989121234567", Text: "one", ClientRef: ref("r1")}); err != nil {
		t.Fatal(err)
	}
	_, _, err := svc.Accept(ctx, c.ID, message.Request{To: "+989121234567", Text: "two", ClientRef: ref("r1")})
	var conflict *message.ConflictError
	if !errors.As(err, &conflict) || conflict.Fields[0].Field != "client_ref" {
		t.Fatalf("expected a conflict on client_ref, got %v", err)
	}
	if got := balance(t, pool, c.ID); got != 99 {
		t.Errorf("balance = %d, want 99", got)
	}

	other, _ := testutil.Customer(t, pool, 10)
	if _, replayed, err := svc.Accept(ctx, other.ID, message.Request{To: "+989121234567", Text: "two", ClientRef: ref("r1")}); err != nil || replayed {
		t.Errorf("client_refs must be scoped per customer: %v, replayed %v", err, replayed)
	}
}

// IT05: batch atomicity.
func TestBatchIsAllOrNothing(t *testing.T) {
	pool := testutil.DB(t)
	svc := newService(pool)
	c, _ := testutil.Customer(t, pool, 250)

	reqs := make([]message.Request, 300)
	for i := range reqs {
		reqs[i] = message.Request{To: "+989121234567", Text: "hi"}
	}
	if _, err := svc.AcceptBatch(context.Background(), c.ID, reqs); !errors.Is(err, billing.ErrInsufficientCredits) {
		t.Fatalf("expected insufficient credits, got %v", err)
	}
	if n := count(t, pool, `SELECT count(*) FROM messages`); n != 0 {
		t.Errorf("%d messages recorded", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM transactions WHERE kind = 'debit'`); n != 0 {
		t.Errorf("%d debits recorded", n)
	}

	res, err := svc.AcceptBatch(context.Background(), c.ID, reqs[:250])
	if err != nil || len(res.Messages) != 250 || res.TotalCost != 250 {
		t.Fatalf("batch of 250: %v, %d messages, cost %d", err, len(res.Messages), res.TotalCost)
	}
	if got := balance(t, pool, c.ID); got != 0 {
		t.Errorf("balance = %d", got)
	}
	testutil.AssertLedgerConsistent(t, pool)
}

// IT06: batch idempotency.
func TestBatchIdempotency(t *testing.T) {
	pool := testutil.DB(t)
	svc := newService(pool)
	c, _ := testutil.Customer(t, pool, 100)
	ctx := context.Background()

	batch := []message.Request{
		{To: "+989121234567", Text: "a", ClientRef: ref("b-1")},
		{To: "+989121234568", Text: "b", ClientRef: ref("b-2"), Type: message.Express},
	}
	first, err := svc.AcceptBatch(ctx, c.ID, batch)
	if err != nil || first.Replayed {
		t.Fatalf("first batch: %v", err)
	}

	again, err := svc.AcceptBatch(ctx, c.ID, batch)
	if err != nil || !again.Replayed || again.TotalCost != first.TotalCost {
		t.Fatalf("replay: %v, %+v", err, again)
	}
	for i := range batch {
		if again.Messages[i].ID != first.Messages[i].ID {
			t.Errorf("replay item %d has a different message", i)
		}
	}

	overlap := []message.Request{batch[0], {To: "+989121234569", Text: "c", ClientRef: ref("b-3")}}
	_, err = svc.AcceptBatch(ctx, c.ID, overlap)
	var conflict *message.ConflictError
	if !errors.As(err, &conflict) || conflict.Fields[0].Field != "messages[0].client_ref" {
		t.Fatalf("partial overlap: expected conflict on item 0, got %v", err)
	}

	withoutRef := []message.Request{batch[0], {To: "+989121234569", Text: "c"}}
	if _, err := svc.AcceptBatch(ctx, c.ID, withoutRef); !errors.As(err, &conflict) {
		t.Fatalf("overlap with an item lacking client_ref: expected conflict, got %v", err)
	}

	if got, want := balance(t, pool, c.ID), 100-first.TotalCost; got != want {
		t.Errorf("balance = %d, want %d", got, want)
	}
	testutil.AssertLedgerConsistent(t, pool)
}

func TestGetAndListAreScopedToCustomer(t *testing.T) {
	pool := testutil.DB(t)
	svc := newService(pool)
	c, _ := testutil.Customer(t, pool, 100)
	other, _ := testutil.Customer(t, pool, 100)
	ctx := context.Background()

	var ids []uuid.UUID
	for i := range 5 {
		typ := message.Normal
		if i%2 == 1 {
			typ = message.Express
		}
		m, _, err := svc.Accept(ctx, c.ID, message.Request{To: "+989121234567", Text: "hi", Type: typ})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, m.ID)
	}

	if _, err := svc.Get(ctx, other.ID, ids[0]); !errors.Is(err, message.ErrNotFound) {
		t.Errorf("another customer's message: %v", err)
	}

	page, more, err := svc.List(ctx, c.ID, message.Filter{Limit: 3})
	if err != nil || len(page) != 3 || !more || page[0].ID != ids[4] {
		t.Fatalf("first page: %v, %d items, more %v", err, len(page), more)
	}
	rest, more, err := svc.List(ctx, c.ID, message.Filter{Limit: 3, After: page[2].ID})
	if err != nil || len(rest) != 2 || more || rest[1].ID != ids[0] {
		t.Fatalf("second page: %v, %d items, more %v", err, len(rest), more)
	}

	express, _, err := svc.List(ctx, c.ID, message.Filter{Type: message.Express, Limit: 50})
	if err != nil || len(express) != 2 {
		t.Errorf("express filter: %v, %d items", err, len(express))
	}
	future, _, err := svc.List(ctx, c.ID, message.Filter{Since: time.Now().Add(time.Hour), Limit: 50})
	if err != nil || len(future) != 0 {
		t.Errorf("since filter: %v, %d items", err, len(future))
	}
	none, _, err := svc.List(ctx, other.ID, message.Filter{Limit: 50})
	if err != nil || len(none) != 0 {
		t.Errorf("other customer sees %d messages (%v)", len(none), err)
	}
}

func TestSummarize(t *testing.T) {
	pool := testutil.DB(t)
	svc := newService(pool)
	c, _ := testutil.Customer(t, pool, 100)
	ctx := context.Background()

	for _, typ := range []message.Type{message.Normal, message.Normal, message.Express} {
		if _, _, err := svc.Accept(ctx, c.ID, message.Request{To: "+989121234567", Text: "hi", Type: typ}); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	sum, err := svc.Summarize(ctx, c.ID, now.Add(-time.Hour), now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if sum.Messages != 3 || sum.Segments != 3 || sum.CreditsSpent != 5 || sum.CreditsRefunded != 0 {
		t.Errorf("totals: %+v", sum)
	}
	if sum.ByStatus[message.StatusAccepted] != 3 || sum.ByStatus[message.StatusDelivered] != 0 || len(sum.ByStatus) != 6 {
		t.Errorf("by status: %v", sum.ByStatus)
	}
	if sum.ByType[message.Normal].Credits != 2 || sum.ByType[message.Express].Credits != 3 {
		t.Errorf("by type: %v", sum.ByType)
	}
}
