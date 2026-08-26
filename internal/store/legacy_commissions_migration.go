package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	LegacyCommissionsSlice = "commissions-v1"
	maxLegacyCommissions   = 1_000_000
)

type LegacyCommissionLog struct {
	ID           int64  `json:"id"`
	InviteUserID int64  `json:"invite_user_id"`
	UserID       int64  `json:"user_id"`
	TradeNo      string `json:"trade_no"`
	OrderAmount  int64  `json:"order_amount"`
	GetAmount    int64  `json:"get_amount"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
}

type LegacyCommissionsImport struct {
	Slice                string
	SourceSHA256         string
	SourceSize           int64
	Logs                 []LegacyCommissionLog
	Checksum             string
	RollbackBackupPath   string
	RollbackBackupSHA256 string
}

type LegacyCommissionsImportReport struct {
	Slice                string             `json:"slice"`
	SourceSHA256         string             `json:"source_sha256"`
	SourceSize           int64              `json:"source_size"`
	RollbackBackupPath   string             `json:"rollback_backup_path"`
	RollbackBackupSHA256 string             `json:"rollback_backup_sha256"`
	Logs                 LegacyDomainResult `json:"logs"`
	AppliedAt            time.Time          `json:"applied_at"`
	AlreadyApplied       bool               `json:"already_applied"`
}

func LegacyCommissionsChecksum(logs []LegacyCommissionLog) string {
	ordered := append([]LegacyCommissionLog(nil), logs...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].ID < ordered[right].ID })
	if ordered == nil {
		ordered = []LegacyCommissionLog{}
	}
	return legacyCanonicalChecksum(ordered)
}

func ValidateLegacyCommissionsData(logs []LegacyCommissionLog) error {
	if len(logs) > maxLegacyCommissions {
		return fmt.Errorf("%w: legacy commissions exceed the %d-row migration limit", ErrInvalidInput, maxLegacyCommissions)
	}
	ids := make(map[int64]struct{}, len(logs))
	orderInviters := make(map[string]struct{}, len(logs))
	for _, item := range logs {
		if item.ID < 1 || item.InviteUserID < 1 || item.UserID < 1 || !validTradeNo(item.TradeNo) ||
			item.OrderAmount < 0 || item.OrderAmount > maxOrderMoneyCents || item.GetAmount < 1 || item.GetAmount > maxOrderMoneyCents ||
			!validLegacyUnixTimestamp(item.CreatedAt) || !validLegacyUnixTimestamp(item.UpdatedAt) || item.UpdatedAt < item.CreatedAt {
			return fmt.Errorf("%w: invalid legacy commission id %d", ErrInvalidInput, item.ID)
		}
		if _, duplicate := ids[item.ID]; duplicate {
			return fmt.Errorf("%w: duplicate legacy commission id %d", ErrConflict, item.ID)
		}
		key := fmt.Sprintf("%s:%d", item.TradeNo, item.InviteUserID)
		if _, duplicate := orderInviters[key]; duplicate {
			return fmt.Errorf("%w: duplicate legacy commission order and inviter", ErrConflict)
		}
		ids[item.ID] = struct{}{}
		orderInviters[key] = struct{}{}
	}
	return nil
}

func (s *Store) LookupLegacyCommissionsImport(ctx context.Context, sourceSHA256 string) (LegacyCommissionsImportReport, bool, error) {
	if !validLowerSHA256(sourceSHA256) {
		return LegacyCommissionsImportReport{}, false, ErrInvalidInput
	}
	return lookupLegacyCommissionsImport(ctx, s.db, sourceSHA256)
}

func lookupLegacyCommissionsImport(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, sourceSHA256 string) (LegacyCommissionsImportReport, bool, error) {
	var encoded string
	err := database.QueryRowContext(ctx, `SELECT report_json FROM legacy_migration_runs WHERE slice = ? AND source_sha256 = ?`, LegacyCommissionsSlice, sourceSHA256).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return LegacyCommissionsImportReport{}, false, nil
	}
	if err != nil {
		return LegacyCommissionsImportReport{}, false, fmt.Errorf("lookup legacy commission migration: %w", err)
	}
	var report LegacyCommissionsImportReport
	if err := json.Unmarshal([]byte(encoded), &report); err != nil {
		return LegacyCommissionsImportReport{}, false, fmt.Errorf("decode legacy commission migration report: %w", err)
	}
	report.AlreadyApplied = true
	return report, true, nil
}

func (s *Store) ImportLegacyCommissions(ctx context.Context, input LegacyCommissionsImport, now time.Time) (LegacyCommissionsImportReport, error) {
	if err := validateLegacyCommissionsImport(input); err != nil {
		return LegacyCommissionsImportReport{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LegacyCommissionsImportReport{}, fmt.Errorf("begin legacy commission import: %w", err)
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return LegacyCommissionsImportReport{}, fmt.Errorf("read legacy commission target schema: %w", err)
	}
	if version != CurrentSchemaVersion() {
		return LegacyCommissionsImportReport{}, fmt.Errorf("legacy commission import requires current schema %d, found %d", CurrentSchemaVersion(), version)
	}
	if err := ValidateSchema(ctx, tx, version); err != nil {
		return LegacyCommissionsImportReport{}, fmt.Errorf("validate legacy commission target schema: %w", err)
	}
	if existing, found, err := lookupLegacyCommissionsImport(ctx, tx, input.SourceSHA256); err != nil {
		return LegacyCommissionsImportReport{}, err
	} else if found {
		if err := tx.Commit(); err != nil {
			return LegacyCommissionsImportReport{}, fmt.Errorf("commit idempotent legacy commission import: %w", err)
		}
		return existing, nil
	}
	var otherRuns, prerequisiteRuns, targetRows int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_migration_runs WHERE slice = ?`, LegacyCommissionsSlice).Scan(&otherRuns); err != nil {
		return LegacyCommissionsImportReport{}, fmt.Errorf("count legacy commission migrations: %w", err)
	}
	if otherRuns != 0 {
		return LegacyCommissionsImportReport{}, fmt.Errorf("%w: legacy commission slice was already imported from another snapshot", ErrConflict)
	}
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM legacy_migration_runs
		WHERE source_sha256 = ? AND slice IN (?, ?)
	`, input.SourceSHA256, LegacyHumanUsersSlice, LegacyOrdersSlice).Scan(&prerequisiteRuns); err != nil {
		return LegacyCommissionsImportReport{}, fmt.Errorf("validate legacy commission prerequisites: %w", err)
	}
	if prerequisiteRuns != 2 {
		return LegacyCommissionsImportReport{}, fmt.Errorf("%w: import human users and orders from the same snapshot before commissions", ErrConflict)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM commission_logs`).Scan(&targetRows); err != nil {
		return LegacyCommissionsImportReport{}, fmt.Errorf("count target commission logs: %w", err)
	}
	if targetRows != 0 {
		return LegacyCommissionsImportReport{}, fmt.Errorf("%w: legacy commission import requires an empty commission log target", ErrConflict)
	}
	resolved, err := resolveLegacyCommissionReferences(ctx, tx, input.Logs)
	if err != nil {
		return LegacyCommissionsImportReport{}, err
	}
	statement, err := tx.PrepareContext(ctx, `
		INSERT INTO commission_logs
		(id, order_id, invite_user_id, user_id, trade_no, order_amount, get_amount, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return LegacyCommissionsImportReport{}, fmt.Errorf("prepare legacy commission import: %w", err)
	}
	defer statement.Close()
	for index, item := range input.Logs {
		if _, err := statement.ExecContext(ctx, item.ID, resolved[index], item.InviteUserID, item.UserID, item.TradeNo,
			item.OrderAmount, item.GetAmount, item.CreatedAt, item.UpdatedAt); err != nil {
			return LegacyCommissionsImportReport{}, fmt.Errorf("import legacy commission id %d: %w", item.ID, err)
		}
	}
	target, err := readLegacyTargetCommissions(ctx, tx)
	if err != nil {
		return LegacyCommissionsImportReport{}, err
	}
	report := LegacyCommissionsImportReport{
		Slice: input.Slice, SourceSHA256: input.SourceSHA256, SourceSize: input.SourceSize,
		RollbackBackupPath: input.RollbackBackupPath, RollbackBackupSHA256: input.RollbackBackupSHA256,
		Logs:      LegacyDomainResult{SourceRows: len(input.Logs), TargetRows: len(target), SourceChecksum: input.Checksum, TargetChecksum: LegacyCommissionsChecksum(target)},
		AppliedAt: now.UTC(),
	}
	if report.Logs.SourceRows != report.Logs.TargetRows || report.Logs.SourceChecksum != report.Logs.TargetChecksum {
		return LegacyCommissionsImportReport{}, errors.New("legacy commission target verification does not match source")
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return LegacyCommissionsImportReport{}, fmt.Errorf("encode legacy commission migration report: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO legacy_migration_runs
		(slice, source_sha256, source_size, rollback_backup_path, rollback_backup_sha256, report_json, applied_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, report.Slice, report.SourceSHA256, report.SourceSize, report.RollbackBackupPath, report.RollbackBackupSHA256, string(encoded), report.AppliedAt.Unix()); err != nil {
		return LegacyCommissionsImportReport{}, fmt.Errorf("record legacy commission migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LegacyCommissionsImportReport{}, fmt.Errorf("commit legacy commission import: %w", err)
	}
	return report, nil
}

func validateLegacyCommissionsImport(input LegacyCommissionsImport) error {
	if input.Slice != LegacyCommissionsSlice || !validLowerSHA256(input.SourceSHA256) || input.SourceSize < 1 ||
		input.RollbackBackupPath == "" || len(input.RollbackBackupPath) > 4096 || !utf8.ValidString(input.RollbackBackupPath) ||
		strings.IndexFunc(input.RollbackBackupPath, unicode.IsControl) >= 0 || !validLowerSHA256(input.RollbackBackupSHA256) ||
		input.Checksum != LegacyCommissionsChecksum(input.Logs) {
		return fmt.Errorf("%w: invalid legacy commission import", ErrInvalidInput)
	}
	return ValidateLegacyCommissionsData(input.Logs)
}

func resolveLegacyCommissionReferences(ctx context.Context, tx *sql.Tx, logs []LegacyCommissionLog) ([]int64, error) {
	orderIDs := make([]int64, len(logs))
	for index, item := range logs {
		var orderID, orderUserID, orderAmount int64
		if err := tx.QueryRowContext(ctx, `SELECT id, user_id, total_amount FROM orders WHERE trade_no = ?`, item.TradeNo).Scan(&orderID, &orderUserID, &orderAmount); errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: legacy commission id %d references missing order", ErrConflict, item.ID)
		} else if err != nil {
			return nil, fmt.Errorf("resolve legacy commission order: %w", err)
		}
		if orderUserID != item.UserID || orderAmount != item.OrderAmount {
			return nil, fmt.Errorf("%w: legacy commission id %d does not match its order", ErrConflict, item.ID)
		}
		var users int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM users WHERE id IN (?, ?) AND account_kind = 'human'
		`, item.InviteUserID, item.UserID).Scan(&users); err != nil {
			return nil, fmt.Errorf("validate legacy commission users: %w", err)
		}
		wantUsers := 2
		if item.InviteUserID == item.UserID {
			wantUsers = 1
		}
		if users != wantUsers {
			return nil, fmt.Errorf("%w: legacy commission id %d references missing human user", ErrConflict, item.ID)
		}
		orderIDs[index] = orderID
	}
	return orderIDs, nil
}

func readLegacyTargetCommissions(ctx context.Context, database queryer) ([]LegacyCommissionLog, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id, invite_user_id, user_id, trade_no, order_amount, get_amount, created_at, updated_at
		FROM commission_logs ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("read imported legacy commissions: %w", err)
	}
	defer rows.Close()
	logs := make([]LegacyCommissionLog, 0)
	for rows.Next() {
		var item LegacyCommissionLog
		if err := rows.Scan(&item.ID, &item.InviteUserID, &item.UserID, &item.TradeNo, &item.OrderAmount,
			&item.GetAmount, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan imported legacy commission: %w", err)
		}
		logs = append(logs, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate imported legacy commissions: %w", err)
	}
	return logs, nil
}
