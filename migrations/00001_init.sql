-- migrate:up

-- Customers (tenants). The balance is a projection of the transactions table:
-- every change to it is made in the same transaction as a transactions row.
CREATE TABLE customers (
    id              UUID PRIMARY KEY,
    name            TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    api_key_hash    BYTEA NOT NULL UNIQUE,
    api_key_prefix  TEXT NOT NULL,
    balance         BIGINT NOT NULL DEFAULT 0 CHECK (balance >= 0),
    rate_limit_rps  INTEGER NOT NULL DEFAULT 100 CHECK (rate_limit_rps BETWEEN 1 AND 100000),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE messages (
    id              UUID PRIMARY KEY,
    customer_id     UUID NOT NULL REFERENCES customers (id) ON DELETE CASCADE,
    type            TEXT NOT NULL CHECK (type IN ('normal', 'express')),
    recipient       TEXT NOT NULL,
    body            TEXT NOT NULL,
    encoding        TEXT NOT NULL CHECK (encoding IN ('gsm7', 'ucs2')),
    segments        SMALLINT NOT NULL CHECK (segments BETWEEN 1 AND 10),
    cost            BIGINT NOT NULL CHECK (cost > 0),
    status          TEXT NOT NULL DEFAULT 'accepted'
                    CHECK (status IN ('accepted', 'sent', 'delivered', 'undelivered', 'failed', 'expired')),
    attempts        INTEGER NOT NULL DEFAULT 0,
    provider        TEXT,
    provider_ref    TEXT,
    last_error      TEXT,
    failure_reason  TEXT CHECK (failure_reason IN ('rejected', 'attempts_exhausted', 'expired')),
    sla_breached    BOOLEAN NOT NULL DEFAULT false,
    client_ref      TEXT,
    accepted_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ NOT NULL,
    sent_at         TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT messages_customer_client_ref_key UNIQUE (customer_id, client_ref),
    CONSTRAINT messages_failure_reason_check_status
        CHECK ((status IN ('failed', 'expired')) = (failure_reason IS NOT NULL))
);

-- Serves every customer-scoped query: lookups, keyset listings, time ranges
-- (through UUIDv7 bounds), and summary reports.
CREATE INDEX messages_customer_idx ON messages (customer_id, id DESC);

-- Append-only credit history. Constraints make the accounting rules structural.
CREATE TABLE transactions (
    id           UUID PRIMARY KEY,
    customer_id  UUID NOT NULL REFERENCES customers (id) ON DELETE CASCADE,
    kind         TEXT NOT NULL CHECK (kind IN ('charge', 'debit', 'refund')),
    amount       BIGINT NOT NULL,
    message_id   UUID REFERENCES messages (id) ON DELETE CASCADE,
    client_ref   TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT transactions_amount_sign_check
        CHECK ((kind = 'debit' AND amount < 0) OR (kind IN ('charge', 'refund') AND amount > 0)),
    CONSTRAINT transactions_message_check
        CHECK ((kind = 'charge') = (message_id IS NULL)),
    CONSTRAINT transactions_client_ref_check
        CHECK ((kind = 'charge') = (client_ref IS NOT NULL))
);

-- At most one debit and one refund per message.
CREATE UNIQUE INDEX transactions_message_kind_uidx ON transactions (message_id, kind)
    WHERE message_id IS NOT NULL;
-- Charges are idempotent per customer.
CREATE UNIQUE INDEX transactions_charge_ref_uidx ON transactions (customer_id, client_ref)
    WHERE kind = 'charge';
CREATE INDEX transactions_customer_idx ON transactions (customer_id, id DESC);

-- Dispatch queue: one row per message that still needs dispatch work.
-- Claiming sets lease_owner and moves next_attempt_at to the lease deadline,
-- so "next_attempt_at <= now()" selects both ready rows and expired leases.
CREATE TABLE queue (
    message_id       UUID PRIMARY KEY REFERENCES messages (id) ON DELETE CASCADE,
    lane             TEXT NOT NULL,
    next_attempt_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_owner      TEXT,
    expires_at       TIMESTAMPTZ NOT NULL
) WITH (
    fillfactor = 70,
    autovacuum_vacuum_scale_factor = 0.01,
    autovacuum_vacuum_cost_delay = 0
);

CREATE INDEX queue_lane_ready_idx ON queue (lane, next_attempt_at);
CREATE INDEX queue_expires_idx ON queue (expires_at);

-- Worker heartbeats, for the dashboard and stale-worker detection.
CREATE TABLE workers (
    id          TEXT PRIMARY KEY,
    role        TEXT NOT NULL,
    started_at  TIMESTAMPTZ NOT NULL,
    last_seen   TIMESTAMPTZ NOT NULL,
    stats       JSONB NOT NULL DEFAULT '{}'
);

-- migrate:down

DROP TABLE workers;
DROP TABLE queue;
DROP TABLE transactions;
DROP TABLE messages;
DROP TABLE customers;
