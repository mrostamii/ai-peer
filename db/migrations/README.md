# Database Migrations

Phase 2 introduces an official gateway data plane and durable accounting schema.

## Current migration set

- `0001_phase2_data_plane.sql`: initial system-of-record schema for providers,
  consumers, usage events, ledgers, settlements, and reconciliation reports.
- `0002_phase2_control_security.sql`: telemetry batch idempotency and API key
  uniqueness index.
- `0003_phase2_api_key_lifecycle.sql`: API key lifecycle table for active and
  revoked key history.
- `0004_phase2_prepaid_balance_state.sql`: prepaid balance snapshots and
  request reservation state for reserve/finalize flow.

## Apply manually

```bash
psql "$DATABASE_URL" -f db/migrations/0001_phase2_data_plane.sql
psql "$DATABASE_URL" -f db/migrations/0002_phase2_control_security.sql
psql "$DATABASE_URL" -f db/migrations/0003_phase2_api_key_lifecycle.sql
psql "$DATABASE_URL" -f db/migrations/0004_phase2_prepaid_balance_state.sql
```

## Notes

- Community/local gateways should not receive any database credentials.
- Official gateways write financial/business truth into PostgreSQL.
- Redis is intended for hot-path auth/balance checks and is not the durable
  accounting source of truth.
