package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"
)

const LegacyOperationalLogsSlice = "operational-logs-v1"
const MaxLegacyOperationalLogs = 1_000_000

const schemaV63OperationalLogs = `
CREATE TABLE IF NOT EXISTS legacy_operational_logs (
 category TEXT NOT NULL CHECK(category IN ('admin_audit','request','mail','server')),
 source_id INTEGER NOT NULL CHECK(source_id > 0),
 method TEXT NOT NULL CHECK(method IN ('','GET','HEAD','POST','PUT','PATCH','DELETE','OPTIONS')),
 created_at INTEGER NOT NULL CHECK(created_at >= 0),
 PRIMARY KEY(category,source_id)
);
CREATE INDEX IF NOT EXISTS idx_legacy_operational_logs_created ON legacy_operational_logs(created_at, category, source_id);
CREATE TABLE IF NOT EXISTS operational_daily_statistics (
 record_at INTEGER NOT NULL CHECK(record_at >= -28800 AND (record_at + 32400) % 86400 IN (0,3600)),
 metric TEXT NOT NULL CHECK(metric IN ('order_count','order_total','paid_count','paid_total','commission_count','commission_total','register_count','invite_count','transfer_used_total')),
 value INTEGER NOT NULL CHECK(typeof(value) = 'integer' AND value >= 0),
 PRIMARY KEY(record_at, metric)
);
`

// This projection cannot hold raw legacy payloads or identities.
type LegacyOperationalLog struct {
	Category  string `json:"category"`
	ID        int64  `json:"id"`
	Method    string `json:"method"`
	CreatedAt int64  `json:"created_at"`
}

// Hourly counts reconcile the complete legacy user domain without loading any
// identity or invitation relationship into the operational migration process.
type LegacyOperationalUserFact struct {
	HourAt     int64 `json:"hour_at"`
	Registered int64 `json:"registered"`
	Invited    int64 `json:"invited"`
}

func LegacyOperationalUserFactsChecksum(facts []LegacyOperationalUserFact) string {
	ordered := append([]LegacyOperationalUserFact{}, facts...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].HourAt < ordered[j].HourAt })
	return legacyCanonicalChecksum(ordered)
}

type LegacyOperationalLogsImport struct {
	Slice                string
	SourceSHA256         string
	SourceSize           int64
	Logs                 []LegacyOperationalLog
	Checksum             string
	UserFactsChecksum    string
	AsOf                 int64
	CutoffAt             int64
	ExcludedRows         int64
	ExcludedFailedJobs   int64
	RollbackBackupPath   string
	RollbackBackupSHA256 string
}

type LegacyOperationalLogsImportReport struct {
	Slice                string             `json:"slice"`
	SourceSHA256         string             `json:"source_sha256"`
	SourceSize           int64              `json:"source_size"`
	RollbackBackupPath   string             `json:"rollback_backup_path"`
	RollbackBackupSHA256 string             `json:"rollback_backup_sha256"`
	Logs                 LegacyDomainResult `json:"logs"`
	StatisticsRows       int64              `json:"statistics_rows"`
	StatisticsChecksum   string             `json:"statistics_checksum"`
	UserFactsChecksum    string             `json:"user_facts_checksum"`
	StatisticsTimezone   string             `json:"statistics_timezone"`
	AsOf                 int64              `json:"as_of"`
	CutoffAt             int64              `json:"cutoff_at"`
	ExcludedRows         int64              `json:"excluded_rows"`
	ExcludedFailedJobs   int64              `json:"excluded_failed_jobs"`
	AppliedAt            time.Time          `json:"applied_at"`
	AlreadyApplied       bool               `json:"already_applied"`
}

func LegacyOperationalLogsChecksum(logs []LegacyOperationalLog) string {
	ordered := append([]LegacyOperationalLog{}, logs...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Category == ordered[j].Category {
			return ordered[i].ID < ordered[j].ID
		}
		return ordered[i].Category < ordered[j].Category
	})
	return legacyCanonicalChecksum(ordered)
}

func (s *Store) LookupLegacyOperationalLogsImport(ctx context.Context, digest string) (LegacyOperationalLogsImportReport, bool, error) {
	if !validLowerSHA256(digest) {
		return LegacyOperationalLogsImportReport{}, false, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return LegacyOperationalLogsImportReport{}, false, err
	}
	defer tx.Rollback()
	report, found, err := lookupLegacyOperationalLogsImport(ctx, tx, digest)
	if err != nil || !found {
		return report, found, err
	}
	if err := verifyOperationalImportTarget(ctx, tx, report); err != nil {
		return LegacyOperationalLogsImportReport{}, false, err
	}
	return report, true, tx.Commit()
}

func lookupLegacyOperationalLogsImport(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, digest string) (LegacyOperationalLogsImportReport, bool, error) {
	var encoded string
	err := db.QueryRowContext(ctx, `SELECT report_json FROM legacy_migration_runs WHERE slice = ? AND source_sha256 = ?`, LegacyOperationalLogsSlice, digest).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return LegacyOperationalLogsImportReport{}, false, nil
	}
	if err != nil {
		return LegacyOperationalLogsImportReport{}, false, err
	}
	var report LegacyOperationalLogsImportReport
	if err := json.Unmarshal([]byte(encoded), &report); err != nil {
		return report, false, err
	}
	report.AlreadyApplied = true
	return report, true, nil
}

func (s *Store) ImportLegacyOperationalLogs(ctx context.Context, input LegacyOperationalLogsImport, now time.Time) (LegacyOperationalLogsImportReport, error) {
	if input.Slice != LegacyOperationalLogsSlice || !validLowerSHA256(input.SourceSHA256) || input.SourceSize < 512 ||
		!validLowerSHA256(input.RollbackBackupSHA256) || input.RollbackBackupPath == "" || now.IsZero() ||
		input.AsOf < 90*86400 || input.AsOf > now.Unix() || input.CutoffAt != input.AsOf-90*86400 ||
		input.ExcludedRows < 0 || input.ExcludedFailedJobs < 0 || len(input.Logs) > MaxLegacyOperationalLogs ||
		input.Checksum != LegacyOperationalLogsChecksum(input.Logs) || !validLowerSHA256(input.UserFactsChecksum) {
		return LegacyOperationalLogsImportReport{}, ErrInvalidInput
	}
	for _, row := range input.Logs {
		if row.ID < 1 || row.CreatedAt < input.CutoffAt || row.CreatedAt > input.AsOf {
			return LegacyOperationalLogsImportReport{}, ErrInvalidInput
		}
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LegacyOperationalLogsImportReport{}, err
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return LegacyOperationalLogsImportReport{}, err
	}
	if version != CurrentSchemaVersion() {
		return LegacyOperationalLogsImportReport{}, fmt.Errorf("operational import requires current schema")
	}
	if err := ValidateSchema(ctx, tx, version); err != nil {
		return LegacyOperationalLogsImportReport{}, err
	}
	if report, found, err := lookupLegacyOperationalLogsImport(ctx, tx, input.SourceSHA256); err != nil {
		return report, err
	} else if found {
		if report.AsOf != input.AsOf || report.CutoffAt != input.CutoffAt || report.Logs.SourceChecksum != input.Checksum || report.UserFactsChecksum != input.UserFactsChecksum {
			return report, ErrConflict
		}
		if err := verifyOperationalImportTarget(ctx, tx, report); err != nil {
			return LegacyOperationalLogsImportReport{}, err
		}
		return report, tx.Commit()
	}
	var existing, prerequisites int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_migration_runs WHERE slice = ?`, input.Slice).Scan(&existing); err != nil {
		return LegacyOperationalLogsImportReport{}, err
	}
	if existing != 0 {
		return LegacyOperationalLogsImportReport{}, fmt.Errorf("%w: operational logs already imported from another snapshot", ErrConflict)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_migration_runs WHERE source_sha256 = ? AND slice IN (?,?,?,?,?)`, input.SourceSHA256, LegacyHumanUsersSlice, LegacyNodesSlice, LegacyOrdersSlice, LegacyCommissionsSlice, LegacyDistributorsSlice).Scan(&prerequisites); err != nil {
		return LegacyOperationalLogsImportReport{}, err
	}
	if prerequisites != 5 {
		return LegacyOperationalLogsImportReport{}, fmt.Errorf("%w: import human users, distributors, nodes, orders and commissions from the same snapshot before operational logs", ErrConflict)
	}
	userFactsChecksum, err := readOperationalUserFactsChecksum(ctx, tx, input.AsOf)
	if err != nil {
		return LegacyOperationalLogsImportReport{}, err
	}
	if userFactsChecksum != input.UserFactsChecksum {
		return LegacyOperationalLogsImportReport{}, errors.New("operational user facts verification does not match source; reconcile the complete user and distributor domains before importing statistics")
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_operational_logs`).Scan(&existing); err != nil {
		return LegacyOperationalLogsImportReport{}, err
	}
	if existing != 0 {
		return LegacyOperationalLogsImportReport{}, fmt.Errorf("%w: operational log target must be empty", ErrConflict)
	}
	statement, err := tx.PrepareContext(ctx, `INSERT INTO legacy_operational_logs(category,source_id,method,created_at) VALUES (?,?,?,?)`)
	if err != nil {
		return LegacyOperationalLogsImportReport{}, err
	}
	defer statement.Close()
	for _, row := range input.Logs {
		if _, err := statement.ExecContext(ctx, row.Category, row.ID, row.Method, row.CreatedAt); err != nil {
			return LegacyOperationalLogsImportReport{}, fmt.Errorf("import operational metadata: %w", err)
		}
	}
	targetRows, targetChecksum, err := readOperationalLogsResult(ctx, tx)
	if err != nil {
		return LegacyOperationalLogsImportReport{}, err
	}
	if targetRows != len(input.Logs) || targetChecksum != input.Checksum {
		return LegacyOperationalLogsImportReport{}, errors.New("operational metadata target verification does not match source")
	}
	statsRows, statsChecksum, err := rebuildOperationalStatistics(ctx, tx, input.AsOf)
	if err != nil {
		return LegacyOperationalLogsImportReport{}, err
	}
	report := LegacyOperationalLogsImportReport{
		Slice: input.Slice, SourceSHA256: input.SourceSHA256, SourceSize: input.SourceSize,
		RollbackBackupPath: input.RollbackBackupPath, RollbackBackupSHA256: input.RollbackBackupSHA256,
		Logs:           LegacyDomainResult{SourceRows: len(input.Logs), TargetRows: targetRows, SourceChecksum: input.Checksum, TargetChecksum: targetChecksum},
		StatisticsRows: statsRows, StatisticsChecksum: statsChecksum, UserFactsChecksum: userFactsChecksum, StatisticsTimezone: "Asia/Shanghai", AsOf: input.AsOf, CutoffAt: input.CutoffAt,
		ExcludedRows: input.ExcludedRows, ExcludedFailedJobs: input.ExcludedFailedJobs, AppliedAt: now.UTC(),
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return report, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO legacy_migration_runs(slice,source_sha256,source_size,rollback_backup_path,rollback_backup_sha256,report_json,applied_at) VALUES (?,?,?,?,?,?,?)`, input.Slice, input.SourceSHA256, input.SourceSize, input.RollbackBackupPath, input.RollbackBackupSHA256, string(encoded), now.Unix()); err != nil {
		return report, err
	}
	return report, tx.Commit()
}

// Aggregate imported facts, never old v2_stat/stats_daily values. Each fact
// metric uses bounded SQL aggregation; integer SUM aborts on overflow.
func rebuildOperationalStatistics(ctx context.Context, tx *sql.Tx, asOf int64) (int64, string, error) {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return 0, "", fmt.Errorf("load legacy statistics timezone: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM operational_daily_statistics`); err != nil {
		return 0, "", err
	}
	expected := []operationalStatistic{}
	queries := []struct{ metric, table, stamp, expression, where string }{
		{"order_count", "orders", "created_at", "COUNT(*)", ""}, {"order_total", "orders", "created_at", "SUM(total_amount)", ""},
		{"paid_count", "orders", "paid_at", "COUNT(*)", " AND status NOT IN (0,2)"}, {"paid_total", "orders", "paid_at", "SUM(total_amount)", " AND status NOT IN (0,2)"},
		{"commission_count", "commission_logs", "created_at", "COUNT(*)", ""}, {"commission_total", "commission_logs", "created_at", "SUM(get_amount)", ""},
		{"register_count", "users", "created_at", "COUNT(*)", ""}, {"invite_count", "users", "created_at", "COUNT(*)", " AND invite_user_id IS NOT NULL"},
	}
	for _, q := range queries {
		rows, err := tx.QueryContext(ctx, `SELECT (`+q.stamp+`/3600)*3600, ?, `+q.expression+` FROM `+q.table+` WHERE `+q.stamp+` >= 0 AND `+q.stamp+` <= ?`+q.where+` GROUP BY (`+q.stamp+`/3600)*3600`, q.metric, asOf)
		if err != nil {
			return 0, "", fmt.Errorf("rebuild %s from facts: %w", q.metric, err)
		}
		values, err := scanOperationalFactStatistics(rows, location)
		if err != nil {
			return 0, "", err
		}
		expected = append(expected, values...)
	}
	rows, err := tx.QueryContext(ctx, `
	 SELECT (created_at/3600)*3600,'transfer_used_total',SUM(amount) FROM (
	 SELECT created_at,upload AS amount FROM node_traffic_stats WHERE created_at >= 0 AND created_at <= ?
	 UNION ALL SELECT created_at,download AS amount FROM node_traffic_stats WHERE created_at >= 0 AND created_at <= ?
	 ) GROUP BY (created_at/3600)*3600`, asOf, asOf)
	if err != nil {
		return 0, "", fmt.Errorf("rebuild traffic from facts: %w", err)
	}
	traffic, err := scanOperationalFactStatistics(rows, location)
	if err != nil {
		return 0, "", err
	}
	expected = append(expected, traffic...)
	sort.Slice(expected, func(i, j int) bool {
		if expected[i].RecordAt == expected[j].RecordAt {
			return expected[i].Metric < expected[j].Metric
		}
		return expected[i].RecordAt < expected[j].RecordAt
	})
	statement, err := tx.PrepareContext(ctx, `INSERT INTO operational_daily_statistics(record_at,metric,value) VALUES (?,?,?)`)
	if err != nil {
		return 0, "", err
	}
	defer statement.Close()
	for _, metric := range expected {
		if _, err := statement.ExecContext(ctx, metric.RecordAt, metric.Metric, metric.Value); err != nil {
			return 0, "", fmt.Errorf("store rebuilt statistics: %w", err)
		}
	}
	actual, err := readOperationalStatistics(ctx, tx)
	if err != nil {
		return 0, "", err
	}
	checksum := legacyCanonicalChecksum(actual)
	if len(actual) != len(expected) || checksum != legacyCanonicalChecksum(expected) {
		return 0, "", errors.New("operational statistics target verification does not match business facts")
	}
	return int64(len(actual)), checksum, nil
}

type operationalStatistic struct {
	RecordAt int64  `json:"record_at"`
	Metric   string `json:"metric"`
	Value    int64  `json:"value"`
}

func scanOperationalFactStatistics(rows *sql.Rows, location *time.Location) ([]operationalStatistic, error) {
	defer rows.Close()
	// SQL reduces facts to hourly totals before Go applies the legacy timezone.
	// Shanghai's post-1970 offsets are whole hours, including historic DST;
	// fixed UTC+8 arithmetic would misclassify older legitimate business facts.
	days := map[int64]operationalStatistic{}
	for rows.Next() {
		var value operationalStatistic
		if err := rows.Scan(&value.RecordAt, &value.Metric, &value.Value); err != nil {
			return nil, fmt.Errorf("read operational fact aggregate: %w", err)
		}
		local := time.Unix(value.RecordAt, 0).In(location)
		stamp := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location).Unix()
		day := days[stamp]
		if value.Value < 0 || day.Value > math.MaxInt64-value.Value {
			return nil, errors.New("operational daily statistic integer overflow")
		}
		day.RecordAt, day.Metric, day.Value = stamp, value.Metric, day.Value+value.Value
		days[stamp] = day
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate operational fact aggregates: %w", err)
	}
	values := make([]operationalStatistic, 0, len(days))
	for _, value := range days {
		values = append(values, value)
	}
	return values, nil
}

func scanOperationalStatistics(rows *sql.Rows) ([]operationalStatistic, error) {
	defer rows.Close()
	values := []operationalStatistic{}
	for rows.Next() {
		var value operationalStatistic
		if err := rows.Scan(&value.RecordAt, &value.Metric, &value.Value); err != nil {
			return nil, fmt.Errorf("read operational statistics: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate operational statistics: %w", err)
	}
	return values, nil
}

func readOperationalStatistics(ctx context.Context, database queryer) ([]operationalStatistic, error) {
	rows, err := database.QueryContext(ctx, `SELECT record_at,metric,value FROM operational_daily_statistics ORDER BY record_at,metric`)
	if err != nil {
		return nil, err
	}
	return scanOperationalStatistics(rows)
}

func readOperationalLogsResult(ctx context.Context, database queryer) (int, string, error) {
	rows, err := database.QueryContext(ctx, `SELECT category,source_id,method,created_at FROM legacy_operational_logs ORDER BY category,source_id`)
	if err != nil {
		return 0, "", err
	}
	defer rows.Close()
	// Stream the same canonical JSON as LegacyOperationalLogsChecksum without
	// allocating a second million-row collection solely for verification.
	digest := sha256.New()
	_, _ = digest.Write([]byte("["))
	count := 0
	for rows.Next() {
		var row LegacyOperationalLog
		if err := rows.Scan(&row.Category, &row.ID, &row.Method, &row.CreatedAt); err != nil {
			return 0, "", err
		}
		encoded, err := json.Marshal(row)
		if err != nil {
			return 0, "", err
		}
		if count > 0 {
			_, _ = digest.Write([]byte(","))
		}
		_, _ = digest.Write(encoded)
		count++
	}
	if err := rows.Err(); err != nil {
		return 0, "", err
	}
	_, _ = digest.Write([]byte("]"))
	return count, hex.EncodeToString(digest.Sum(nil)), nil
}

func verifyOperationalImportTarget(ctx context.Context, database queryer, report LegacyOperationalLogsImportReport) error {
	userFactsChecksum, err := readOperationalUserFactsChecksum(ctx, database, report.AsOf)
	if err != nil {
		return err
	}
	if userFactsChecksum != report.UserFactsChecksum {
		return errors.New("operational user facts verification failed for recorded migration")
	}
	count, checksum, err := readOperationalLogsResult(ctx, database)
	if err != nil {
		return err
	}
	if count != report.Logs.TargetRows || checksum != report.Logs.TargetChecksum || count != report.Logs.SourceRows || checksum != report.Logs.SourceChecksum {
		return errors.New("operational metadata target verification failed for recorded migration")
	}
	statistics, err := readOperationalStatistics(ctx, database)
	if err != nil {
		return err
	}
	if int64(len(statistics)) != report.StatisticsRows || legacyCanonicalChecksum(statistics) != report.StatisticsChecksum {
		return errors.New("operational statistics target verification failed for recorded migration")
	}
	return nil
}

func readOperationalUserFactsChecksum(ctx context.Context, database queryer, asOf int64) (string, error) {
	rows, err := database.QueryContext(ctx, `SELECT (created_at/3600)*3600,COUNT(*),COUNT(invite_user_id) FROM users WHERE created_at >= 0 AND created_at <= ? GROUP BY (created_at/3600)*3600 ORDER BY (created_at/3600)*3600`, asOf)
	if err != nil {
		return "", fmt.Errorf("read target user statistics facts: %w", err)
	}
	defer rows.Close()
	facts := []LegacyOperationalUserFact{}
	for rows.Next() {
		var fact LegacyOperationalUserFact
		if err := rows.Scan(&fact.HourAt, &fact.Registered, &fact.Invited); err != nil {
			return "", fmt.Errorf("scan target user statistics facts: %w", err)
		}
		facts = append(facts, fact)
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterate target user statistics facts: %w", err)
	}
	return LegacyOperationalUserFactsChecksum(facts), nil
}
