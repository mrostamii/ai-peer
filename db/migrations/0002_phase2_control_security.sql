-- Phase 2 control-plane security and idempotency additions.

BEGIN;

CREATE TABLE IF NOT EXISTS telemetry_batches (
    gateway_id TEXT NOT NULL,
    batch_id TEXT NOT NULL,
    signature TEXT NOT NULL,
    event_count INTEGER NOT NULL DEFAULT 0,
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (gateway_id, batch_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_consumers_api_key_hash_unique
    ON consumers (api_key_hash)
    WHERE api_key_hash IS NOT NULL;

COMMIT;
