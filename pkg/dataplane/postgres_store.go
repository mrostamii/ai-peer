package dataplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mrostamii/tooti/pkg/gateway"
)

type PostgresStore struct {
	pool *pgxpool.Pool
}

func OpenPostgresStore(dsn string, maxOpen, maxIdle, connMaxLifetimeSec int) (*PostgresStore, error) {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return nil, fmt.Errorf("postgres dsn is required")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse postgres dsn: %w", err)
	}
	if maxOpen > 0 {
		cfg.MaxConns = int32(maxOpen)
	}
	if maxIdle > 0 {
		cfg.MinConns = int32(maxIdle)
	}
	if connMaxLifetimeSec > 0 {
		cfg.MaxConnLifetime = time.Duration(connMaxLifetimeSec) * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open postgres pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &PostgresStore{pool: pool}, nil
}

func (s *PostgresStore) Close() error {
	if s == nil || s.pool == nil {
		return nil
	}
	s.pool.Close()
	return nil
}

func (s *PostgresStore) UpsertProvider(ctx context.Context, req gateway.ProviderRegisterRequest) error {
	metaRaw, err := json.Marshal(req.Metadata)
	if err != nil {
		return fmt.Errorf("marshal provider metadata: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO providers (provider_id, peer_id, status, metadata_json)
		VALUES ($1, $2, $3, $4::jsonb)
		ON CONFLICT (provider_id) DO UPDATE
		SET peer_id = EXCLUDED.peer_id,
			status = EXCLUDED.status,
			last_seen_at = NOW(),
			metadata_json = EXCLUDED.metadata_json
	`, req.ProviderID, req.PeerID, req.Status, string(metaRaw))
	if err != nil {
		return fmt.Errorf("upsert provider: %w", err)
	}
	if strings.TrimSpace(req.WalletAddress) == "" {
		return nil
	}
	_, err = s.pool.Exec(ctx, `
		UPDATE provider_wallet_history
		SET active_to = NOW()
		WHERE provider_id = $1 AND active_to IS NULL AND wallet_address <> $2
	`, req.ProviderID, req.WalletAddress)
	if err != nil {
		return fmt.Errorf("close previous active wallet: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO provider_wallet_history (provider_id, wallet_address, rotation_proof)
		SELECT $1, $2, ''
		WHERE NOT EXISTS (
			SELECT 1 FROM provider_wallet_history
			WHERE provider_id = $1 AND wallet_address = $2 AND active_to IS NULL
		)
	`, req.ProviderID, req.WalletAddress)
	if err != nil {
		return fmt.Errorf("insert provider wallet history: %w", err)
	}
	return nil
}

func (s *PostgresStore) HeartbeatProvider(ctx context.Context, req gateway.ProviderHeartbeatRequest) error {
	res, err := s.pool.Exec(ctx, `
		UPDATE providers
		SET peer_id = $2, status = $3, last_seen_at = NOW()
		WHERE provider_id = $1
	`, req.ProviderID, req.PeerID, req.Status)
	if err != nil {
		return fmt.Errorf("update provider heartbeat: %w", err)
	}
	n := res.RowsAffected()
	if n > 0 {
		return nil
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO providers (provider_id, peer_id, status)
		VALUES ($1, $2, $3)
	`, req.ProviderID, req.PeerID, req.Status)
	if err != nil {
		return fmt.Errorf("insert provider from heartbeat: %w", err)
	}
	return nil
}

func (s *PostgresStore) RotateProviderWallet(ctx context.Context, req gateway.ProviderWalletRotateRequest) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin wallet rotation tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `
		UPDATE provider_wallet_history
		SET active_to = NOW()
		WHERE provider_id = $1 AND active_to IS NULL
	`, req.ProviderID)
	if err != nil {
		return fmt.Errorf("close active wallet: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO provider_wallet_history (provider_id, wallet_address, rotation_proof)
		VALUES ($1, $2, $3)
	`, req.ProviderID, req.NewWallet, req.RotationProof)
	if err != nil {
		return fmt.Errorf("insert rotated wallet: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit wallet rotation tx: %w", err)
	}
	return nil
}

func (s *PostgresStore) RecordTelemetryBatch(ctx context.Context, gatewayID, batchID, signature string, eventCount int) (bool, error) {
	res, err := s.pool.Exec(ctx, `
		INSERT INTO telemetry_batches (gateway_id, batch_id, signature, event_count)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (gateway_id, batch_id) DO NOTHING
	`, gatewayID, batchID, signature, eventCount)
	if err != nil {
		return false, fmt.Errorf("insert telemetry batch: %w", err)
	}
	return res.RowsAffected() > 0, nil
}

func (s *PostgresStore) InsertUsageEvents(ctx context.Context, events []gateway.UsageEvent) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin usage insert tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	inserted := 0
	for _, e := range events {
		if strings.TrimSpace(e.RequestID) == "" {
			continue
		}
		if e.CreatedAt.IsZero() {
			e.CreatedAt = time.Now().UTC()
		}
		res, err := tx.Exec(ctx, `
			INSERT INTO usage_events (
				request_id, gateway_id, gateway_type, consumer_id, provider_id,
				model, tokens_in, tokens_out, cost_usdc, latency_ms, status,
				payment_method, created_at
			) VALUES (
				$1, $2, $3, NULLIF($4,''), NULLIF($5,''),
				$6, $7, $8, $9, $10, $11,
				$12, $13
			)
			ON CONFLICT (request_id) DO NOTHING
		`,
			e.RequestID, e.GatewayID, e.GatewayType, e.ConsumerID, e.ProviderID,
			e.Model, e.TokensIn, e.TokensOut, e.CostUSDC, e.LatencyMS, e.Status,
			e.PaymentMethod, e.CreatedAt)
		if err != nil {
			return 0, fmt.Errorf("insert usage event %s: %w", e.RequestID, err)
		}
		n := res.RowsAffected()
		inserted += int(n)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit usage insert tx: %w", err)
	}
	return inserted, nil
}

func (s *PostgresStore) ListUsageEvents(ctx context.Context, filter gateway.UsageListFilter) ([]gateway.UsageEvent, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT request_id, gateway_id, gateway_type, COALESCE(consumer_id, ''),
		       COALESCE(provider_id, ''), model, tokens_in, tokens_out, cost_usdc,
		       latency_ms, status, payment_method, created_at
		FROM usage_events
		WHERE ($1 = '' OR consumer_id = $1)
		  AND ($2 = '' OR provider_id = $2)
		ORDER BY created_at DESC
		LIMIT $3
	`, filter.ConsumerID, filter.ProviderID, limit)
	if err != nil {
		return nil, fmt.Errorf("query usage events: %w", err)
	}
	defer rows.Close()
	out := make([]gateway.UsageEvent, 0, limit)
	for rows.Next() {
		var ev gateway.UsageEvent
		if err := rows.Scan(
			&ev.RequestID, &ev.GatewayID, &ev.GatewayType, &ev.ConsumerID,
			&ev.ProviderID, &ev.Model, &ev.TokensIn, &ev.TokensOut, &ev.CostUSDC,
			&ev.LatencyMS, &ev.Status, &ev.PaymentMethod, &ev.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan usage event: %w", err)
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate usage events: %w", err)
	}
	return out, nil
}

func (s *PostgresStore) UpsertConsumerAPIKey(ctx context.Context, consumerID, walletAddress, consumerType, apiKeyHash string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin upsert consumer api key tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `
		INSERT INTO consumers (consumer_id, api_key_hash, wallet_address, consumer_type)
		VALUES ($1, $2, NULLIF($3,''), $4)
		ON CONFLICT (consumer_id) DO UPDATE
		SET api_key_hash = EXCLUDED.api_key_hash,
			wallet_address = COALESCE(EXCLUDED.wallet_address, consumers.wallet_address),
			consumer_type = EXCLUDED.consumer_type
	`, consumerID, apiKeyHash, walletAddress, consumerType)
	if err != nil {
		return fmt.Errorf("upsert consumer api key: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO consumer_api_keys (consumer_id, api_key_hash, status)
		VALUES ($1, $2, 'active')
		ON CONFLICT (api_key_hash) DO UPDATE
		SET status = 'active', revoked_at = NULL, revoked_reason = ''
	`, consumerID, apiKeyHash)
	if err != nil {
		return fmt.Errorf("insert consumer api key history: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit upsert consumer api key tx: %w", err)
	}
	return nil
}

func (s *PostgresStore) RevokeConsumerAPIKey(ctx context.Context, consumerID, apiKeyHash, reason string) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin revoke api key tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	res, err := tx.Exec(ctx, `
		UPDATE consumer_api_keys
		SET status = 'revoked', revoked_at = NOW(), revoked_reason = $3
		WHERE consumer_id = $1 AND api_key_hash = $2 AND status = 'active'
	`, consumerID, apiKeyHash, reason)
	if err != nil {
		return false, fmt.Errorf("revoke consumer api key: %w", err)
	}
	if res.RowsAffected() == 0 {
		return false, nil
	}
	_, err = tx.Exec(ctx, `
		UPDATE consumers
		SET api_key_hash = NULL
		WHERE consumer_id = $1 AND api_key_hash = $2
	`, consumerID, apiKeyHash)
	if err != nil {
		return false, fmt.Errorf("clear active api key pointer: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit revoke api key tx: %w", err)
	}
	return true, nil
}

func (s *PostgresStore) LookupActiveAPIKey(ctx context.Context, apiKeyHash string) (*gateway.APIKeyPrincipal, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT c.consumer_id, c.consumer_type, COALESCE(c.wallet_address, '')
		FROM consumer_api_keys k
		JOIN consumers c ON c.consumer_id = k.consumer_id
		WHERE k.api_key_hash = $1
		  AND k.status = 'active'
		  AND c.api_key_hash = $1
		LIMIT 1
	`, apiKeyHash)
	var p gateway.APIKeyPrincipal
	if err := row.Scan(&p.ConsumerID, &p.ConsumerType, &p.WalletAddress); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("lookup active api key: %w", err)
	}
	return &p, nil
}

func (s *PostgresStore) LookupConsumerAPIKeyHash(ctx context.Context, consumerID string) (string, error) {
	consumerID = strings.TrimSpace(consumerID)
	if consumerID == "" {
		return "", nil
	}
	var hash *string
	if err := s.pool.QueryRow(ctx, `
		SELECT api_key_hash
		FROM consumers
		WHERE consumer_id = $1
	`, consumerID).Scan(&hash); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("lookup consumer api key hash: %w", err)
	}
	if hash == nil {
		return "", nil
	}
	return strings.TrimSpace(*hash), nil
}

func (s *PostgresStore) ReservePrepaidBalance(ctx context.Context, consumerID, requestID string, reserveUSDC float64) (gateway.PrepaidReserveResult, error) {
	if reserveUSDC <= 0 {
		return gateway.PrepaidReserveResult{}, fmt.Errorf("reserve amount must be > 0")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return gateway.PrepaidReserveResult{}, fmt.Errorf("begin reserve prepaid tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var existingStatus string
	var existingReserved float64
	err = tx.QueryRow(ctx, `
		SELECT status, reserved_usdc
		FROM prepaid_reservations
		WHERE request_id = $1 AND consumer_id = $2
		FOR UPDATE
	`, requestID, consumerID).Scan(&existingStatus, &existingReserved)
	if err == nil {
		balance, balErr := queryCurrentBalanceForUpdate(ctx, tx, consumerID)
		if balErr != nil {
			return gateway.PrepaidReserveResult{}, balErr
		}
		if err := tx.Commit(ctx); err != nil {
			return gateway.PrepaidReserveResult{}, fmt.Errorf("commit reserve prepaid idempotent tx: %w", err)
		}
		return gateway.PrepaidReserveResult{
			Approved:     existingStatus == "reserved" || existingStatus == "finalized" || existingStatus == "released",
			ReservedUSDC: existingReserved,
			BalanceAfter: balance,
		}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return gateway.PrepaidReserveResult{}, fmt.Errorf("lookup prepaid reservation: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO consumer_balances (consumer_id, current_balance)
		VALUES ($1, 0)
		ON CONFLICT (consumer_id) DO NOTHING
	`, consumerID); err != nil {
		return gateway.PrepaidReserveResult{}, fmt.Errorf("ensure consumer balance row: %w", err)
	}
	currentBalance, err := queryCurrentBalanceForUpdate(ctx, tx, consumerID)
	if err != nil {
		return gateway.PrepaidReserveResult{}, err
	}
	if currentBalance < reserveUSDC {
		return gateway.PrepaidReserveResult{
			Approved:     false,
			ReservedUSDC: reserveUSDC,
			BalanceAfter: currentBalance,
		}, nil
	}
	balanceAfter := currentBalance - reserveUSDC
	if _, err := tx.Exec(ctx, `
		UPDATE consumer_balances
		SET current_balance = $2, updated_at = NOW()
		WHERE consumer_id = $1
	`, consumerID, balanceAfter); err != nil {
		return gateway.PrepaidReserveResult{}, fmt.Errorf("update consumer balance after reserve: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO balance_ledger (consumer_id, entry_type, amount_usdc, reference, balance_after)
		VALUES ($1, 'reserve', $2, $3, $4)
	`, consumerID, -reserveUSDC, "reserve:"+requestID, balanceAfter); err != nil {
		return gateway.PrepaidReserveResult{}, fmt.Errorf("insert reserve ledger entry: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO prepaid_reservations (request_id, consumer_id, reserved_usdc, status)
		VALUES ($1, $2, $3, 'reserved')
	`, requestID, consumerID, reserveUSDC); err != nil {
		return gateway.PrepaidReserveResult{}, fmt.Errorf("insert prepaid reservation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return gateway.PrepaidReserveResult{}, fmt.Errorf("commit reserve prepaid tx: %w", err)
	}
	return gateway.PrepaidReserveResult{
		Approved:     true,
		ReservedUSDC: reserveUSDC,
		BalanceAfter: balanceAfter,
	}, nil
}

func (s *PostgresStore) FinalizePrepaidCharge(ctx context.Context, consumerID, requestID string, actualChargeUSDC float64, success bool) (float64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin finalize prepaid tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var status string
	var reservedUSDC float64
	if err := tx.QueryRow(ctx, `
		SELECT status, reserved_usdc
		FROM prepaid_reservations
		WHERE request_id = $1 AND consumer_id = $2
		FOR UPDATE
	`, requestID, consumerID).Scan(&status, &reservedUSDC); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, fmt.Errorf("prepaid reservation not found for request_id=%s", requestID)
		}
		return 0, fmt.Errorf("load prepaid reservation: %w", err)
	}

	currentBalance, err := queryCurrentBalanceForUpdate(ctx, tx, consumerID)
	if err != nil {
		return 0, err
	}
	if status == "finalized" || status == "released" {
		if err := tx.Commit(ctx); err != nil {
			return 0, fmt.Errorf("commit finalize prepaid idempotent tx: %w", err)
		}
		return currentBalance, nil
	}
	if status != "reserved" {
		return 0, fmt.Errorf("invalid reservation status %q", status)
	}
	if actualChargeUSDC < 0 {
		actualChargeUSDC = 0
	}
	chargeUSDC := actualChargeUSDC
	if !success {
		chargeUSDC = 0
	}
	if chargeUSDC > reservedUSDC {
		chargeUSDC = reservedUSDC
	}

	balanceAfterRelease := currentBalance + reservedUSDC
	balanceAfter := balanceAfterRelease - chargeUSDC
	if _, err := tx.Exec(ctx, `
		UPDATE consumer_balances
		SET current_balance = $2, updated_at = NOW()
		WHERE consumer_id = $1
	`, consumerID, balanceAfter); err != nil {
		return 0, fmt.Errorf("update consumer balance after finalize: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO balance_ledger (consumer_id, entry_type, amount_usdc, reference, balance_after)
		VALUES ($1, 'release', $2, $3, $4)
	`, consumerID, reservedUSDC, "release:"+requestID, balanceAfterRelease); err != nil {
		return 0, fmt.Errorf("insert release ledger entry: %w", err)
	}
	finalStatus := "released"
	if chargeUSDC > 0 {
		if _, err := tx.Exec(ctx, `
			INSERT INTO balance_ledger (consumer_id, entry_type, amount_usdc, reference, balance_after)
			VALUES ($1, 'debit', $2, $3, $4)
		`, consumerID, -chargeUSDC, "debit:"+requestID, balanceAfter); err != nil {
			return 0, fmt.Errorf("insert debit ledger entry: %w", err)
		}
		finalStatus = "finalized"
	}
	if _, err := tx.Exec(ctx, `
		UPDATE prepaid_reservations
		SET status = $3, charged_usdc = $4, finalized_at = NOW()
		WHERE request_id = $1 AND consumer_id = $2
	`, requestID, consumerID, finalStatus, chargeUSDC); err != nil {
		return 0, fmt.Errorf("update prepaid reservation finalize state: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit finalize prepaid tx: %w", err)
	}
	return balanceAfter, nil
}

func (s *PostgresStore) CreditConsumerBalance(ctx context.Context, consumerID string, amountUSDC float64, reference string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin credit balance tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		INSERT INTO consumer_balances (consumer_id, current_balance)
		VALUES ($1, 0)
		ON CONFLICT (consumer_id) DO NOTHING
	`, consumerID); err != nil {
		return fmt.Errorf("ensure consumer balance row: %w", err)
	}
	currentBalance, err := queryCurrentBalanceForUpdate(ctx, tx, consumerID)
	if err != nil {
		return err
	}
	balanceAfter := currentBalance + amountUSDC
	if _, err := tx.Exec(ctx, `
		UPDATE consumer_balances
		SET current_balance = $2, updated_at = NOW()
		WHERE consumer_id = $1
	`, consumerID, balanceAfter); err != nil {
		return fmt.Errorf("update consumer balance after credit: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO balance_ledger (consumer_id, entry_type, amount_usdc, reference, balance_after)
		VALUES ($1, 'credit', $2, $3, $4)
	`, consumerID, amountUSDC, reference, balanceAfter); err != nil {
		return fmt.Errorf("insert credit balance ledger: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit credit balance tx: %w", err)
	}
	return nil
}

func (s *PostgresStore) RecordPrepaidDeposit(ctx context.Context, rec gateway.PrepaidDepositRecord) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin record prepaid deposit tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rec.TxHash = strings.TrimSpace(strings.ToLower(rec.TxHash))
	rec.ConsumerID = strings.TrimSpace(rec.ConsumerID)
	rec.WalletAddress = strings.TrimSpace(strings.ToLower(rec.WalletAddress))
	rec.TokenAddress = strings.TrimSpace(strings.ToLower(rec.TokenAddress))
	rec.ToAddress = strings.TrimSpace(strings.ToLower(rec.ToAddress))
	rec.Network = strings.TrimSpace(rec.Network)
	rec.AmountAtomic = strings.TrimSpace(rec.AmountAtomic)
	if rec.TxHash == "" || rec.ConsumerID == "" || rec.AmountUSDC <= 0 {
		return false, fmt.Errorf("tx_hash, consumer_id, and amount_usdc are required")
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO consumers (consumer_id, api_key_hash, wallet_address, consumer_type)
		VALUES ($1, NULL, NULLIF($2,''), 'prepaid')
		ON CONFLICT (consumer_id) DO UPDATE
		SET wallet_address = COALESCE(EXCLUDED.wallet_address, consumers.wallet_address),
			consumer_type = 'prepaid'
	`, rec.ConsumerID, rec.WalletAddress)
	if err != nil {
		return false, fmt.Errorf("ensure consumer row for deposit: %w", err)
	}

	res, err := tx.Exec(ctx, `
		INSERT INTO prepaid_deposits (
			tx_hash, network, consumer_id, wallet_address, token_address,
			to_address, amount_atomic, amount_usdc, block_number
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (tx_hash) DO NOTHING
	`, rec.TxHash, rec.Network, rec.ConsumerID, rec.WalletAddress, rec.TokenAddress, rec.ToAddress, rec.AmountAtomic, rec.AmountUSDC, rec.BlockNumber)
	if err != nil {
		return false, fmt.Errorf("insert prepaid deposit: %w", err)
	}
	if res.RowsAffected() == 0 {
		return false, nil
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO consumer_balances (consumer_id, current_balance)
		VALUES ($1, 0)
		ON CONFLICT (consumer_id) DO NOTHING
	`, rec.ConsumerID)
	if err != nil {
		return false, fmt.Errorf("ensure consumer balance row for deposit: %w", err)
	}

	currentBalance, err := queryCurrentBalanceForUpdate(ctx, tx, rec.ConsumerID)
	if err != nil {
		return false, err
	}
	balanceAfter := currentBalance + rec.AmountUSDC
	if _, err := tx.Exec(ctx, `
		UPDATE consumer_balances
		SET current_balance = $2, updated_at = NOW()
		WHERE consumer_id = $1
	`, rec.ConsumerID, balanceAfter); err != nil {
		return false, fmt.Errorf("update consumer balance after deposit: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO balance_ledger (consumer_id, entry_type, amount_usdc, reference, balance_after)
		VALUES ($1, 'credit', $2, $3, $4)
	`, rec.ConsumerID, rec.AmountUSDC, "deposit:"+rec.TxHash, balanceAfter); err != nil {
		return false, fmt.Errorf("insert deposit balance ledger: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit record prepaid deposit tx: %w", err)
	}
	return true, nil
}

func (s *PostgresStore) CurrentConsumerBalance(ctx context.Context, consumerID string) (float64, error) {
	consumerID = strings.TrimSpace(consumerID)
	if consumerID == "" {
		return 0, fmt.Errorf("consumer_id is required")
	}
	var balance float64
	if err := s.pool.QueryRow(ctx, `
		SELECT current_balance
		FROM consumer_balances
		WHERE consumer_id = $1
	`, consumerID).Scan(&balance); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil
		}
		return 0, fmt.Errorf("query consumer balance: %w", err)
	}
	return balance, nil
}

func queryCurrentBalanceForUpdate(ctx context.Context, tx pgx.Tx, consumerID string) (float64, error) {
	var balance float64
	if err := tx.QueryRow(ctx, `
		SELECT current_balance
		FROM consumer_balances
		WHERE consumer_id = $1
		FOR UPDATE
	`, consumerID).Scan(&balance); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, fmt.Errorf("consumer balance row missing for consumer_id=%s", consumerID)
		}
		return 0, fmt.Errorf("query consumer balance: %w", err)
	}
	return balance, nil
}
