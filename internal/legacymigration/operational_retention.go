package legacymigration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const (
	DefaultOperationalRetentionDays = 90
	MaxOperationalRetentionDays     = 3650
)

type OperationalRetentionSnapshot struct {
	Path             string                             `json:"path"`
	Size             int64                              `json:"size"`
	SHA256           string                             `json:"sha256"`
	AsOf             time.Time                          `json:"as_of"`
	RetentionDays    int                                `json:"retention_days"`
	CutoffAt         int64                              `json:"cutoff_at"`
	MigrationSafe    bool                               `json:"migration_safe"`
	DecisionRequired bool                               `json:"decision_required"`
	NodeTraffic      OperationalRetentionWindow         `json:"node_traffic"`
	FailedJobs       OperationalFailedJobsRetention     `json:"failed_jobs"`
	QueuedJobs       OperationalQueueRetention          `json:"queued_jobs"`
	StatsDaily       OperationalAuxiliaryTableRetention `json:"stats_daily"`
	Reasons          []string                           `json:"reasons"`
}

type OperationalRetentionWindow struct {
	Present         bool   `json:"present"`
	TotalRows       int64  `json:"total_rows"`
	RetainedRows    int64  `json:"retained_rows"`
	OutOfWindowRows int64  `json:"out_of_window_rows"`
	FutureRows      int64  `json:"future_rows"`
	InvalidRows     int64  `json:"invalid_rows"`
	MinRecordAt     *int64 `json:"min_record_at,omitempty"`
	MaxRecordAt     *int64 `json:"max_record_at,omitempty"`
}

type OperationalFailedJobsRetention struct {
	Present                   bool   `json:"present"`
	TotalRows                 int64  `json:"total_rows"`
	RetainedRows              int64  `json:"retained_rows"`
	OutOfWindowRows           int64  `json:"out_of_window_rows"`
	FutureRows                int64  `json:"future_rows"`
	InvalidRows               int64  `json:"invalid_rows"`
	TimestampColumnAvailable  bool   `json:"timestamp_column_available"`
	PayloadColumnsPresent     bool   `json:"payload_columns_present"`
	ExecutableImportSupported bool   `json:"executable_import_supported"`
	MinFailedAt               *int64 `json:"min_failed_at,omitempty"`
	MaxFailedAt               *int64 `json:"max_failed_at,omitempty"`
}

type OperationalQueueRetention struct {
	Present   bool  `json:"present"`
	TotalRows int64 `json:"total_rows"`
}

type OperationalAuxiliaryTableRetention struct {
	Present   bool  `json:"present"`
	TotalRows int64 `json:"total_rows"`
}

func ReadOperationalRetentionSnapshot(ctx context.Context, sourcePath string, now time.Time, retentionDays int) (OperationalRetentionSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return OperationalRetentionSnapshot{}, err
	}
	if now.IsZero() || now.Unix() < 0 || retentionDays < 1 || retentionDays > MaxOperationalRetentionDays {
		return OperationalRetentionSnapshot{}, fmt.Errorf("operational retention scan requires a current time and retention window between 1 and %d days", MaxOperationalRetentionDays)
	}
	asOf := now.UTC()
	cutoff := asOf.Add(-time.Duration(retentionDays) * 24 * time.Hour).Unix()
	report := OperationalRetentionSnapshot{
		AsOf:          asOf,
		RetentionDays: retentionDays,
		CutoffAt:      cutoff,
	}
	identity, err := readLegacySnapshot(ctx, sourcePath, func(database *sql.DB) error {
		var readErr error
		report.NodeTraffic, readErr = inspectOperationalNodeTraffic(ctx, database, cutoff, asOf.Unix())
		if readErr != nil {
			return readErr
		}
		report.FailedJobs, readErr = inspectOperationalFailedJobs(ctx, database, cutoff, asOf.Unix())
		if readErr != nil {
			return readErr
		}
		report.QueuedJobs, readErr = inspectOperationalQueuedJobs(ctx, database)
		if readErr != nil {
			return readErr
		}
		report.StatsDaily, readErr = inspectOperationalAuxiliaryTable(ctx, database, "stats_daily")
		return readErr
	})
	if err != nil {
		return OperationalRetentionSnapshot{}, err
	}
	report.Path, report.Size, report.SHA256 = identity.Path, identity.Size, identity.SHA256
	report.Reasons = operationalRetentionReasons(report)
	report.MigrationSafe = report.NodeTraffic.InvalidRows == 0 && report.NodeTraffic.FutureRows == 0 && report.FailedJobs.InvalidRows == 0 && report.FailedJobs.FutureRows == 0 && report.QueuedJobs.TotalRows == 0
	report.DecisionRequired = report.NodeTraffic.OutOfWindowRows != 0 || report.FailedJobs.TotalRows != 0 || report.StatsDaily.TotalRows != 0
	if len(report.Reasons) == 0 {
		report.Reasons = []string{"legacy operational data fits the selected retention window and has no pending executable queue rows"}
	}
	return report, nil
}

func inspectOperationalNodeTraffic(ctx context.Context, database *sql.DB, cutoff, asOf int64) (OperationalRetentionWindow, error) {
	present, err := optionalRealTable(ctx, database, "v2_stat_server")
	if err != nil || !present {
		return OperationalRetentionWindow{Present: present}, err
	}
	if err := requireRealTable(ctx, database, "v2_stat_server", []string{"server_id", "server_type", "u", "d", "record_type", "record_at", "created_at", "updated_at"}); err != nil {
		return OperationalRetentionWindow{}, err
	}
	result := OperationalRetentionWindow{Present: true}
	minRecordAt, maxRecordAt, err := scanRetentionWindow(database.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       COALESCE(SUM(CASE WHEN record_at >= ? AND record_at <= ? THEN 1 ELSE 0 END),0),
		       COALESCE(SUM(CASE WHEN record_at < ? THEN 1 ELSE 0 END),0),
		       COALESCE(SUM(CASE WHEN record_at > ? THEN 1 ELSE 0 END),0),
		       COALESCE(SUM(CASE WHEN server_id < 1 OR u < 0 OR d < 0 OR record_at < 0 OR created_at < 0 OR updated_at < created_at OR record_type NOT IN ('d','m') THEN 1 ELSE 0 END),0),
		       MIN(record_at), MAX(record_at)
		FROM v2_stat_server
	`, cutoff, asOf, cutoff, asOf), &result.TotalRows, &result.RetainedRows, &result.OutOfWindowRows, &result.FutureRows, &result.InvalidRows)
	if err != nil {
		return OperationalRetentionWindow{}, fmt.Errorf("inspect legacy node traffic retention: %w", err)
	}
	result.MinRecordAt, result.MaxRecordAt = minRecordAt, maxRecordAt
	return result, nil
}

func inspectOperationalFailedJobs(ctx context.Context, database *sql.DB, cutoff, asOf int64) (OperationalFailedJobsRetention, error) {
	present, err := optionalRealTable(ctx, database, "failed_jobs")
	if err != nil || !present {
		return OperationalFailedJobsRetention{Present: present, ExecutableImportSupported: false}, err
	}
	columns, err := readTableColumns(ctx, database, "failed_jobs")
	if err != nil {
		return OperationalFailedJobsRetention{}, err
	}
	result := OperationalFailedJobsRetention{
		Present: columns != nil, PayloadColumnsPresent: columns["payload"] || columns["exception"],
		TimestampColumnAvailable: columns["failed_at"], ExecutableImportSupported: false,
	}
	if !result.TimestampColumnAvailable {
		if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM failed_jobs`).Scan(&result.TotalRows); err != nil {
			return OperationalFailedJobsRetention{}, fmt.Errorf("count legacy failed jobs: %w", err)
		}
		return result, nil
	}
	minFailedAt, maxFailedAt, err := scanRetentionWindow(database.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       COALESCE(SUM(CASE WHEN failed_at_unix >= ? AND failed_at_unix <= ? THEN 1 ELSE 0 END),0),
		       COALESCE(SUM(CASE WHEN failed_at_unix < ? THEN 1 ELSE 0 END),0),
		       COALESCE(SUM(CASE WHEN failed_at_unix > ? THEN 1 ELSE 0 END),0),
		       COALESCE(SUM(CASE WHEN failed_at_unix IS NULL OR failed_at_unix < 0 THEN 1 ELSE 0 END),0),
		       MIN(failed_at_unix), MAX(failed_at_unix)
		FROM (SELECT `+legacyUnixExpression("failed_at")+` AS failed_at_unix FROM failed_jobs)
	`, cutoff, asOf, cutoff, asOf), &result.TotalRows, &result.RetainedRows, &result.OutOfWindowRows, &result.FutureRows, &result.InvalidRows)
	if err != nil {
		return OperationalFailedJobsRetention{}, fmt.Errorf("inspect legacy failed jobs retention: %w", err)
	}
	result.MinFailedAt, result.MaxFailedAt = minFailedAt, maxFailedAt
	return result, nil
}

func inspectOperationalQueuedJobs(ctx context.Context, database *sql.DB) (OperationalQueueRetention, error) {
	present, err := optionalRealTable(ctx, database, "jobs")
	if err != nil || !present {
		return OperationalQueueRetention{Present: present}, err
	}
	result := OperationalQueueRetention{Present: true}
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs`).Scan(&result.TotalRows); err != nil {
		return OperationalQueueRetention{}, fmt.Errorf("count legacy queued jobs: %w", err)
	}
	return result, nil
}

func inspectOperationalAuxiliaryTable(ctx context.Context, database *sql.DB, table string) (OperationalAuxiliaryTableRetention, error) {
	present, err := optionalRealTable(ctx, database, table)
	if err != nil || !present {
		return OperationalAuxiliaryTableRetention{Present: present}, err
	}
	result := OperationalAuxiliaryTableRetention{Present: true}
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&result.TotalRows); err != nil {
		return OperationalAuxiliaryTableRetention{}, fmt.Errorf("count legacy auxiliary table %q: %w", table, err)
	}
	return result, nil
}

func operationalRetentionReasons(report OperationalRetentionSnapshot) []string {
	reasons := []string{}
	if report.NodeTraffic.InvalidRows != 0 {
		reasons = append(reasons, "legacy node traffic contains invalid rows")
	}
	if report.NodeTraffic.FutureRows != 0 {
		reasons = append(reasons, "legacy node traffic contains future-dated rows")
	}
	if report.NodeTraffic.OutOfWindowRows != 0 {
		reasons = append(reasons, "legacy node traffic outside the selected retention window requires archive-or-skip approval")
	}
	if report.FailedJobs.InvalidRows != 0 {
		reasons = append(reasons, "legacy failed_jobs contains rows without a valid failed_at timestamp")
	}
	if report.FailedJobs.FutureRows != 0 {
		reasons = append(reasons, "legacy failed_jobs contains future-dated rows")
	}
	if report.FailedJobs.TotalRows != 0 {
		reasons = append(reasons, "legacy failed_jobs payloads are not executable Go worker input; retain only non-sensitive aggregate evidence if CE-003 is accepted")
	}
	if report.QueuedJobs.TotalRows != 0 {
		reasons = append(reasons, "legacy jobs queue must be drained before offline migration")
	}
	if report.StatsDaily.TotalRows != 0 {
		reasons = append(reasons, "legacy stats_daily exists but is not a canonical Go target table; rebuild aggregates from facts after D-013")
	}
	return reasons
}

func scanRetentionWindow(row *sql.Row, values ...*int64) (*int64, *int64, error) {
	var minValue, maxValue sql.NullInt64
	destinations := make([]any, 0, len(values)+2)
	for _, value := range values {
		destinations = append(destinations, value)
	}
	destinations = append(destinations, &minValue, &maxValue)
	if err := row.Scan(destinations...); err != nil {
		return nil, nil, err
	}
	return nullableInt64(minValue), nullableInt64(maxValue), nil
}

func nullableInt64(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	copied := value.Int64
	return &copied
}

func optionalRealTable(ctx context.Context, database *sql.DB, table string) (bool, error) {
	var objectType string
	err := database.QueryRowContext(ctx, `SELECT type FROM sqlite_schema WHERE name = ?`, table).Scan(&objectType)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect legacy table %q: %w", table, err)
	}
	if objectType != "table" {
		return false, fmt.Errorf("legacy snapshot object %q must be a real table", table)
	}
	return true, nil
}

func readTableColumns(ctx context.Context, database *sql.DB, table string) (map[string]bool, error) {
	rows, err := database.QueryContext(ctx, `PRAGMA table_info("`+table+`")`)
	if err != nil {
		return nil, fmt.Errorf("inspect legacy table %q columns: %w", table, err)
	}
	defer rows.Close()
	columns := map[string]bool{}
	for rows.Next() {
		var sequence, notNull, primaryKey int
		var name, dataType string
		var defaultValue any
		if err := rows.Scan(&sequence, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, fmt.Errorf("inspect legacy table %q columns: %w", table, err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("inspect legacy table %q columns: %w", table, err)
	}
	return columns, nil
}
