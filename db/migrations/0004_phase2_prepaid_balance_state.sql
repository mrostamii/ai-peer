-- Phase 2 prepaid state tables for reserve/finalize flow.

BEGIN;

CREATE TABLE IF NOT EXISTS consumer_balances (
    consumer_id TEXT PRIMARY KEY REFERENCES consumers(consumer_id) ON DELETE CASCADE,
    current_balance NUMERIC(20, 6) NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS prepaid_reservations (
    request_id TEXT PRIMARY KEY,
    consumer_id TEXT NOT NULL REFERENCES consumers(consumer_id) ON DELETE CASCADE,
    reserved_usdc NUMERIC(20, 6) NOT NULL,
    charged_usdc NUMERIC(20, 6) NOT NULL DEFAULT 0,
    status TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finalized_at TIMESTAMPTZ NULL,
    CONSTRAINT prepaid_reservations_status_chk CHECK (status IN ('reserved', 'finalized', 'released'))
);

CREATE INDEX IF NOT EXISTS idx_prepaid_reservations_consumer_status
    ON prepaid_reservations (consumer_id, status, created_at DESC);

COMMIT;
