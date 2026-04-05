-- Phase 2: x402 prepaid topup idempotency table.

BEGIN;

CREATE TABLE IF NOT EXISTS x402_prepaid_topups (
    payment_fingerprint TEXT PRIMARY KEY,
    consumer_id TEXT NOT NULL REFERENCES consumers(consumer_id) ON DELETE CASCADE,
    amount_usdc NUMERIC(20, 6) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_x402_prepaid_topups_consumer_created_at
    ON x402_prepaid_topups (consumer_id, created_at DESC);

COMMIT;
