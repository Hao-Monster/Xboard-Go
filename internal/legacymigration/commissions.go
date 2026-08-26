package legacymigration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const (
	maxLegacyCommissionRows      = 1_000_000
	maxLegacyCommissionDataBytes = int64(64 << 20)
)

type CommissionsSnapshot struct {
	Path     string
	Size     int64
	SHA256   string
	Logs     []store.LegacyCommissionLog
	Checksum string
}

func ReadCommissionsSnapshot(ctx context.Context, sourcePath string) (CommissionsSnapshot, error) {
	logs := []store.LegacyCommissionLog{}
	identity, err := readLegacySnapshot(ctx, sourcePath, func(database *sql.DB) error {
		if err := requireRealTable(ctx, database, "v2_commission_log", []string{
			"id", "invite_user_id", "user_id", "trade_no", "order_amount", "get_amount", "created_at", "updated_at",
		}); err != nil {
			return err
		}
		if err := validateLegacyQueryBudget(ctx, database, `
			SELECT COUNT(*), COALESCE(SUM(length(CAST(trade_no AS BLOB))), 0)
			FROM v2_commission_log
		`, maxLegacyCommissionRows, maxLegacyCommissionDataBytes, "legacy commissions"); err != nil {
			return err
		}
		var bytesRead int64
		rows, err := database.QueryContext(ctx, `
			SELECT id, invite_user_id, user_id, trade_no, order_amount, get_amount,
			       `+legacyUnixExpression("created_at")+`, `+legacyUnixExpression("updated_at")+`
			FROM v2_commission_log ORDER BY id
		`)
		if err != nil {
			return fmt.Errorf("read legacy commissions: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			if len(logs) >= maxLegacyCommissionRows {
				return fmt.Errorf("legacy commissions exceed the %d-row migration limit", maxLegacyCommissionRows)
			}
			var item store.LegacyCommissionLog
			if err := rows.Scan(&item.ID, &item.InviteUserID, &item.UserID, &item.TradeNo, &item.OrderAmount,
				&item.GetAmount, &item.CreatedAt, &item.UpdatedAt); err != nil {
				return fmt.Errorf("scan legacy commission: %w", err)
			}
			bytesRead += int64(len(item.TradeNo))
			if bytesRead > maxLegacyCommissionDataBytes {
				return errors.New("legacy commissions exceed the migration data limit")
			}
			logs = append(logs, item)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate legacy commissions: %w", err)
		}
		return store.ValidateLegacyCommissionsData(logs)
	})
	if err != nil {
		return CommissionsSnapshot{}, err
	}
	return CommissionsSnapshot{
		Path: identity.Path, Size: identity.Size, SHA256: identity.SHA256,
		Logs: logs, Checksum: store.LegacyCommissionsChecksum(logs),
	}, nil
}
