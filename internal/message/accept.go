package message

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mmrzaf/sms-gatway/internal/billing"
	"github.com/mmrzaf/sms-gatway/internal/store"
)

// Config configures message acceptance.
type Config struct {
	Prices      Prices
	MaxSegments int
	NormalLanes int
	NormalTTL   time.Duration
	ExpressTTL  time.Duration
}

// Service accepts and queries messages.
type Service struct {
	pool *pgxpool.Pool
	cfg  Config
}

// NewService returns a Service using pool.
func NewService(pool *pgxpool.Pool, cfg Config) *Service {
	return &Service{pool: pool, cfg: cfg}
}

// Accepted is the result of an accepted submission.
type Accepted struct {
	// Messages are in request order.
	Messages  []Message
	TotalCost int64
	// Replayed is true when the submission repeated an earlier one by
	// client_ref; nothing new was recorded or charged.
	Replayed bool
}

// Accept accepts one message. It returns *ValidationError for invalid input,
// billing.ErrInsufficientCredits when the balance does not cover the cost,
// and *ConflictError when the client_ref was used for a different message.
func (s *Service) Accept(ctx context.Context, customerID uuid.UUID, req Request) (Message, bool, error) {
	res, err := s.accept(ctx, customerID, []Request{req}, false)
	if err != nil {
		return Message{}, false, err
	}
	return res.Messages[0], res.Replayed, nil
}

// AcceptBatch accepts 1 to MaxBatchSize messages as a unit: either all are
// accepted and the total cost is debited, or nothing is recorded.
//
// A batch is a replay only if every item has a client_ref and every one
// matches an earlier message exactly. Any other overlap with earlier
// client_refs is a *ConflictError.
func (s *Service) AcceptBatch(ctx context.Context, customerID uuid.UUID, reqs []Request) (Accepted, error) {
	return s.accept(ctx, customerID, reqs, true)
}

// errClientRefTaken aborts the accept transaction when an insert skipped a
// row because its client_ref already exists.
var errClientRefTaken = errors.New("client_ref already exists")

func (s *Service) accept(ctx context.Context, customerID uuid.UUID, reqs []Request, batch bool) (Accepted, error) {
	items, err := s.prepare(reqs, batch)
	if err != nil {
		return Accepted{}, err
	}

	// A conflicting row is normally found by the second step. The loop only
	// repeats if that row disappeared in between, for example because its
	// transaction rolled back.
	for range 3 {
		res, err := s.insert(ctx, customerID, items)
		if !errors.Is(err, errClientRefTaken) {
			return res, err
		}
		res, found, err := s.resolveExisting(ctx, customerID, items, batch)
		if found || err != nil {
			return res, err
		}
	}
	return Accepted{}, errors.New("accept: client_ref conflict did not settle")
}

// insert runs the accept transaction. The statement order matters: the
// customer row is the only row concurrent requests contend on, so its update
// comes last and its lock is held only until commit.
func (s *Service) insert(ctx context.Context, customerID uuid.UUID, items []item) (Accepted, error) {
	n := len(items)
	var (
		ids        = make([]uuid.UUID, n)
		types      = make([]string, n)
		recipients = make([]string, n)
		bodies     = make([]string, n)
		encodings  = make([]string, n)
		segments   = make([]int32, n)
		costs      = make([]int64, n)
		clientRefs = make([]*string, n)
		ttls       = make([]int64, n)
		lanes      = make([]string, n)
		debits     = make([]billing.Debit, n)
		total      int64
		express    bool
	)
	for i, it := range items {
		ids[i] = store.NewID()
		types[i] = string(it.Type)
		recipients[i] = it.To
		bodies[i] = it.Text
		encodings[i] = string(it.seg.Encoding)
		segments[i] = int32(it.seg.Segments)
		costs[i] = it.cost
		clientRefs[i] = store.NullString(it.ref)
		ttls[i] = s.ttl(it.Type).Milliseconds()
		lanes[i] = Lane(it.Type, customerID, s.cfg.NormalLanes)
		debits[i] = billing.Debit{MessageID: ids[i], Amount: it.cost}
		total += it.cost
		express = express || it.Type == Express
	}

	var inserted []Message
	err := store.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			INSERT INTO messages (id, customer_id, type, recipient, body, encoding, segments, cost,
			                      client_ref, expires_at)
			SELECT m.id, $1, m.type, m.recipient, m.body, m.encoding, m.segments, m.cost,
			       m.client_ref, now() + m.ttl_ms * interval '1 millisecond'
			FROM unnest($2::uuid[], $3::text[], $4::text[], $5::text[], $6::text[], $7::int[],
			            $8::bigint[], $9::text[], $10::bigint[])
			     AS m(id, type, recipient, body, encoding, segments, cost, client_ref, ttl_ms)
			ON CONFLICT ON CONSTRAINT messages_customer_client_ref_key DO NOTHING
			RETURNING `+columns,
			customerID, ids, types, recipients, bodies, encodings, segments, costs, clientRefs, ttls)
		if err != nil {
			return fmt.Errorf("insert messages: %w", err)
		}
		inserted, err = pgx.CollectRows(rows, scan)
		if err != nil {
			return fmt.Errorf("insert messages: %w", err)
		}
		if len(inserted) < n {
			return errClientRefTaken
		}

		if err := billing.RecordDebits(ctx, tx, customerID, debits); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO queue (message_id, lane, expires_at)
			SELECT m.id, q.lane, m.expires_at
			FROM unnest($1::uuid[], $2::text[]) AS q(id, lane)
			JOIN messages m ON m.id = q.id`,
			ids, lanes); err != nil {
			return fmt.Errorf("enqueue messages: %w", err)
		}
		if _, err := billing.DebitBalance(ctx, tx, customerID, total); err != nil {
			return err
		}
		if express {
			// Delivered to listeners only when the transaction commits.
			if _, err := tx.Exec(ctx, `SELECT pg_notify($1, '')`, ExpressChannel); err != nil {
				return fmt.Errorf("notify express workers: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return Accepted{}, err
	}

	byID := make(map[uuid.UUID]Message, n)
	for _, m := range inserted {
		byID[m.ID] = m
	}
	res := Accepted{Messages: make([]Message, n), TotalCost: total}
	for i, id := range ids {
		res.Messages[i] = byID[id]
	}
	return res, nil
}

// resolveExisting decides between replay and conflict after an insert found
// existing client_refs. found is false if none of them exist any more.
func (s *Service) resolveExisting(ctx context.Context, customerID uuid.UUID, items []item, batch bool) (Accepted, bool, error) {
	var refs []string
	for _, it := range items {
		if it.ref != "" {
			refs = append(refs, it.ref)
		}
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+columns+` FROM messages
		WHERE customer_id = $1 AND client_ref = ANY($2::text[])`,
		customerID, refs)
	if err != nil {
		return Accepted{}, false, fmt.Errorf("load existing messages: %w", err)
	}
	existing, err := pgx.CollectRows(rows, scan)
	if err != nil {
		return Accepted{}, false, fmt.Errorf("load existing messages: %w", err)
	}
	if len(existing) == 0 {
		return Accepted{}, false, nil
	}
	byRef := make(map[string]Message, len(existing))
	for _, m := range existing {
		byRef[*m.ClientRef] = m
	}

	replay := Accepted{Messages: make([]Message, len(items)), Replayed: true}
	var conflicts []FieldError
	for i, it := range items {
		m, ok := byRef[it.ref]
		if it.ref == "" || !ok {
			replay.Messages = nil
			continue
		}
		field := "client_ref"
		if batch {
			field = fmt.Sprintf("messages[%d].client_ref", i)
		}
		if !sameRequest(m, it) {
			conflicts = append(conflicts, FieldError{field, CodeConflict,
				"was already used for a message with a different recipient, text, or type"})
			replay.Messages = nil
			continue
		}
		conflicts = append(conflicts, FieldError{field, CodeConflict,
			"was already used by an earlier request that this batch only partly repeats"})
		if replay.Messages != nil {
			replay.Messages[i] = m
			replay.TotalCost += m.Cost
		}
	}
	if replay.Messages != nil {
		return replay, true, nil
	}
	return Accepted{}, true, &ConflictError{Fields: conflicts}
}

func sameRequest(m Message, it item) bool {
	return m.Recipient == it.To && m.Body == it.Text && m.Type == it.Type
}

func (s *Service) ttl(t Type) time.Duration {
	if t == Express {
		return s.cfg.ExpressTTL
	}
	return s.cfg.NormalTTL
}
