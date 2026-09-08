package legacymigration

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

type OperationalLogsSnapshot struct {
	Path               string
	Size               int64
	SHA256             string
	AsOf               int64
	CutoffAt           int64
	Logs               []store.LegacyOperationalLog
	ExcludedRows       int64
	ExcludedFailedJobs int64
	Checksum           string
	UserFactsChecksum  string
}

// D-013 deliberately projects fixed metadata at the SQL boundary. Request data,
// URI/query strings, recipients, IPs, exceptions and PHP payloads are never read.
func ReadOperationalLogsSnapshot(ctx context.Context, path string, asOf time.Time) (OperationalLogsSnapshot, error) {
	if asOf.IsZero() || asOf.Unix() < 90*86400 {
		return OperationalLogsSnapshot{}, fmt.Errorf("operational log migration requires a valid as-of time")
	}
	snapshot := OperationalLogsSnapshot{AsOf: asOf.Unix(), CutoffAt: asOf.Add(-90 * 24 * time.Hour).Unix(), Logs: []store.LegacyOperationalLog{}}
	identity, err := readLegacySnapshot(ctx, path, func(db *sql.DB) error {
		if err := requireRealTable(ctx, db, "v2_user", []string{"created_at", "invite_user_id"}); err != nil {
			return err
		}
		stamp := legacyUnixExpression("created_at")
		var invalidUsers int64
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM v2_user WHERE (`+stamp+`) IS NULL OR (`+stamp+`) < 0`).Scan(&invalidUsers); err != nil {
			return fmt.Errorf("inspect legacy user statistics timestamps: %w", err)
		}
		if invalidUsers != 0 {
			return fmt.Errorf("legacy users contain invalid statistics timestamps")
		}
		userRows, err := db.QueryContext(ctx, `SELECT ((`+stamp+`)/3600)*3600,COUNT(*),COUNT(invite_user_id) FROM v2_user WHERE (`+stamp+`) <= ? GROUP BY ((`+stamp+`)/3600)*3600 ORDER BY ((`+stamp+`)/3600)*3600`, snapshot.AsOf)
		if err != nil {
			return fmt.Errorf("read legacy user statistics facts: %w", err)
		}
		facts := []store.LegacyOperationalUserFact{}
		for userRows.Next() {
			var fact store.LegacyOperationalUserFact
			if err := userRows.Scan(&fact.HourAt, &fact.Registered, &fact.Invited); err != nil {
				userRows.Close()
				return fmt.Errorf("scan legacy user statistics facts: %w", err)
			}
			facts = append(facts, fact)
		}
		if err := userRows.Err(); err != nil {
			userRows.Close()
			return err
		}
		if err := userRows.Close(); err != nil {
			return err
		}
		snapshot.UserFactsChecksum = store.LegacyOperationalUserFactsChecksum(facts)
		queued, err := inspectOperationalQueuedJobs(ctx, db)
		if err != nil {
			return err
		}
		if queued.TotalRows != 0 {
			return fmt.Errorf("legacy jobs queue must be drained before offline migration")
		}
		failed, err := optionalRealTable(ctx, db, "failed_jobs")
		if err != nil {
			return err
		}
		if failed {
			if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM failed_jobs`).Scan(&snapshot.ExcludedFailedJobs); err != nil {
				return fmt.Errorf("count excluded failed jobs: %w", err)
			}
		}
		for _, source := range []struct {
			table, category string
			method          bool
		}{
			{"v2_admin_audit_log", "admin_audit", true}, {"v2_log", "request", true}, {"v2_mail_log", "mail", false}, {"v2_server_log", "server", false},
		} {
			present, err := optionalRealTable(ctx, db, source.table)
			if err != nil {
				return err
			}
			if !present {
				continue
			}
			columns := []string{"id", "created_at"}
			if source.method {
				columns = append(columns, "method")
			}
			if err := requireRealTable(ctx, db, source.table, columns); err != nil {
				return err
			}
			var invalid, excluded, retained int64
			if err := db.QueryRowContext(ctx, `SELECT
			 COALESCE(SUM(CASE WHEN typeof(id) <> 'integer' OR id < 1 OR typeof(created_at) <> 'integer' OR created_at < 0 OR created_at > ? THEN 1 ELSE 0 END),0),
			 COALESCE(SUM(CASE WHEN created_at < ? THEN 1 ELSE 0 END),0),
			 COALESCE(SUM(CASE WHEN created_at >= ? THEN 1 ELSE 0 END),0) FROM `+source.table,
				snapshot.AsOf, snapshot.CutoffAt, snapshot.CutoffAt).Scan(&invalid, &excluded, &retained); err != nil {
				return fmt.Errorf("inspect %s log timestamps: %w", source.table, err)
			}
			if invalid != 0 {
				return fmt.Errorf("legacy %s contains invalid or future-dated log metadata", source.table)
			}
			if retained > int64(store.MaxLegacyOperationalLogs-len(snapshot.Logs)) {
				return fmt.Errorf("operational log migration exceeds row budget")
			}
			snapshot.ExcludedRows += excluded
			methodProjection := `''`
			if source.method {
				methodProjection = `CASE WHEN UPPER(method) IN ('GET','HEAD','POST','PUT','PATCH','DELETE','OPTIONS') THEN UPPER(method) ELSE '' END`
			}
			rows, err := db.QueryContext(ctx, `SELECT id, created_at, `+methodProjection+` FROM `+source.table+` WHERE created_at >= ? AND created_at <= ? ORDER BY id`, snapshot.CutoffAt, snapshot.AsOf)
			if err != nil {
				return fmt.Errorf("read %s log metadata: %w", source.table, err)
			}
			for rows.Next() {
				entry := store.LegacyOperationalLog{Category: source.category}
				if err := rows.Scan(&entry.ID, &entry.CreatedAt, &entry.Method); err != nil {
					rows.Close()
					return fmt.Errorf("scan log metadata: %w", err)
				}
				snapshot.Logs = append(snapshot.Logs, entry)
			}
			readErr := rows.Err()
			closeErr := rows.Close()
			if readErr != nil {
				return readErr
			}
			if closeErr != nil {
				return closeErr
			}
		}
		return nil
	})
	if err != nil {
		return OperationalLogsSnapshot{}, err
	}
	snapshot.Path, snapshot.Size, snapshot.SHA256 = identity.Path, identity.Size, identity.SHA256
	snapshot.Checksum = store.LegacyOperationalLogsChecksum(snapshot.Logs)
	return snapshot, nil
}
