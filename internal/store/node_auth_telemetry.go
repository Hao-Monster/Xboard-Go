package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"
)

func (s *Store) EnsureNodeAuthTelemetry(ctx context.Context, now time.Time) error {
	if now.IsZero() || now.Unix() < 0 {
		return ErrInvalidInput
	}
	defer s.lockWrite()()
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO node_auth_telemetry_state (id, observed_since)
		VALUES (1, ?) ON CONFLICT(id) DO NOTHING
	`, now.Unix()); err != nil {
		return fmt.Errorf("ensure node authentication telemetry: %w", err)
	}
	return nil
}

func (s *Store) AddNodeAuthUsage(ctx context.Context, increments []NodeAuthUsageIncrement) error {
	if len(increments) == 0 {
		return nil
	}
	if len(increments) > 4 {
		return ErrInvalidInput
	}
	seen := make(map[string]struct{}, len(increments))
	for _, increment := range increments {
		if !validNodeAuthKind(increment.AuthKind) || !validNodeAuthTransport(increment.Transport) ||
			increment.SuccessCount == 0 || increment.SuccessCount > math.MaxInt64 || increment.LastUsedAt.IsZero() || increment.LastUsedAt.Unix() < 0 {
			return ErrInvalidInput
		}
		key := increment.AuthKind + "\x00" + increment.Transport
		if _, duplicate := seen[key]; duplicate {
			return ErrInvalidInput
		}
		seen[key] = struct{}{}
	}

	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin node authentication telemetry update: %w", err)
	}
	defer tx.Rollback()
	for _, increment := range increments {
		result, execErr := tx.ExecContext(ctx, `
			INSERT INTO node_auth_telemetry (auth_kind, transport, success_count, last_used_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(auth_kind, transport) DO UPDATE SET
				success_count = node_auth_telemetry.success_count + excluded.success_count,
				last_used_at = MAX(node_auth_telemetry.last_used_at, excluded.last_used_at)
			WHERE node_auth_telemetry.success_count <= ? - excluded.success_count
		`, increment.AuthKind, increment.Transport, int64(increment.SuccessCount), increment.LastUsedAt.Unix(), int64(math.MaxInt64))
		if execErr != nil {
			return fmt.Errorf("add node authentication telemetry: %w", execErr)
		}
		updated, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return fmt.Errorf("read node authentication telemetry update: %w", rowsErr)
		}
		if updated != 1 {
			return fmt.Errorf("%w: node authentication telemetry counter overflow", ErrInvalidInput)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit node authentication telemetry: %w", err)
	}
	return nil
}

func (s *Store) GetNodeAuthTelemetry(ctx context.Context) (NodeAuthTelemetry, error) {
	var result NodeAuthTelemetry
	var observedSince int64
	if err := s.db.QueryRowContext(ctx, `
		SELECT observed_since FROM node_auth_telemetry_state WHERE id = 1
	`).Scan(&observedSince); errors.Is(err, sql.ErrNoRows) {
		return NodeAuthTelemetry{}, ErrNotFound
	} else if err != nil {
		return NodeAuthTelemetry{}, fmt.Errorf("read node authentication telemetry state: %w", err)
	}
	result.ObservedSince = time.Unix(observedSince, 0).UTC()

	rows, err := s.db.QueryContext(ctx, `
		SELECT auth_kind, transport, success_count, last_used_at
		FROM node_auth_telemetry ORDER BY auth_kind, transport LIMIT 5
	`)
	if err != nil {
		return NodeAuthTelemetry{}, fmt.Errorf("list node authentication telemetry: %w", err)
	}
	defer rows.Close()
	rowCount := 0
	for rows.Next() {
		rowCount++
		var authKind, transport string
		var successCount uint64
		var lastUsedUnix int64
		if err := rows.Scan(&authKind, &transport, &successCount, &lastUsedUnix); err != nil {
			return NodeAuthTelemetry{}, fmt.Errorf("scan node authentication telemetry: %w", err)
		}
		if !validNodeAuthKind(authKind) || !validNodeAuthTransport(transport) || lastUsedUnix < 0 {
			return NodeAuthTelemetry{}, errors.New("node authentication telemetry contains invalid labels or timestamp")
		}
		usage := &result.LegacyGlobalToken
		if authKind == NodeAuthKindMachineCredential {
			usage = &result.MachineCredential
		}
		switch transport {
		case NodeAuthTransportHTTP:
			usage.HTTPAuthSuccess = successCount
		case NodeAuthTransportWebSocket:
			usage.WebSocketAuthSuccess = successCount
		}
		lastUsedAt := time.Unix(lastUsedUnix, 0).UTC()
		if usage.LastUsedAt == nil || lastUsedAt.After(*usage.LastUsedAt) {
			usage.LastUsedAt = &lastUsedAt
		}
	}
	if err := rows.Err(); err != nil {
		return NodeAuthTelemetry{}, fmt.Errorf("iterate node authentication telemetry: %w", err)
	}
	if rowCount > 4 {
		return NodeAuthTelemetry{}, errors.New("node authentication telemetry exceeds its bounded aggregate cardinality")
	}
	return result, nil
}

func validNodeAuthKind(value string) bool {
	return value == NodeAuthKindLegacyGlobalToken || value == NodeAuthKindMachineCredential
}

func validNodeAuthTransport(value string) bool {
	return value == NodeAuthTransportHTTP || value == NodeAuthTransportWebSocket
}
