package store

import (
	"bytes"
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
	LegacyPaymentsSlice = "payments-v1"
	maxLegacyPayments   = 10_000
)

type LegacyPayment struct {
	ID                     int64           `json:"id"`
	UUID                   string          `json:"uuid"`
	Provider               PaymentProvider `json:"provider"`
	Name                   string          `json:"name"`
	Icon                   string          `json:"icon"`
	ConfigCiphertext       []byte          `json:"config_ciphertext"`
	NotifyDomain           string          `json:"notify_domain"`
	HandlingFeeFixed       int64           `json:"handling_fee_fixed"`
	HandlingFeeBasisPoints int64           `json:"handling_fee_basis_points"`
	Enabled                bool            `json:"enabled"`
	SortPosition           int             `json:"sort_position"`
	CreatedAt              int64           `json:"created_at"`
	UpdatedAt              int64           `json:"updated_at"`
}

type LegacyPaymentsImport struct {
	Slice                   string
	SourceSHA256            string
	SourceSize              int64
	Payments                []LegacyPayment
	PaymentsChecksum        string
	PlaintextSourceChecksum string
	RollbackBackupPath      string
	RollbackBackupSHA256    string
}

type LegacyPaymentsImportReport struct {
	Slice                   string             `json:"slice"`
	SourceSHA256            string             `json:"source_sha256"`
	SourceSize              int64              `json:"source_size"`
	RollbackBackupPath      string             `json:"rollback_backup_path"`
	RollbackBackupSHA256    string             `json:"rollback_backup_sha256"`
	PlaintextSourceChecksum string             `json:"plaintext_source_checksum"`
	Payments                LegacyDomainResult `json:"payments"`
	AppliedAt               time.Time          `json:"applied_at"`
	AlreadyApplied          bool               `json:"already_applied"`
}

func LegacyPaymentsChecksum(payments []LegacyPayment) string {
	ordered := append([]LegacyPayment(nil), payments...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].ID < ordered[right].ID })
	if ordered == nil {
		ordered = []LegacyPayment{}
	}
	return legacyCanonicalChecksum(ordered)
}

func ValidateLegacyPaymentsData(payments []LegacyPayment) error {
	if len(payments) > maxLegacyPayments {
		return fmt.Errorf("%w: legacy payments exceed the %d-row migration limit", ErrInvalidInput, maxLegacyPayments)
	}
	ids := make(map[int64]struct{}, len(payments))
	uuids := make(map[string]struct{}, len(payments))
	sorts := make(map[int]struct{}, len(payments))
	for _, item := range payments {
		normalized, err := normalizePaymentInput(SavePaymentInput{
			Provider: item.Provider, Name: item.Name, Icon: item.Icon, ConfigCiphertext: item.ConfigCiphertext,
			NotifyDomain: item.NotifyDomain, HandlingFeeFixed: item.HandlingFeeFixed,
			HandlingFeeBasisPoints: item.HandlingFeeBasisPoints, Enabled: item.Enabled,
		}, time.Unix(item.CreatedAt, 0))
		if err != nil || item.ID < 1 || !validPaymentUUID(item.UUID) || item.UUID != strings.TrimSpace(item.UUID) ||
			item.SortPosition < 1 || item.SortPosition > len(payments) || !validLegacyUnixTimestamp(item.CreatedAt) ||
			!validLegacyUnixTimestamp(item.UpdatedAt) || item.UpdatedAt < item.CreatedAt ||
			normalized.Name != item.Name || normalized.Icon != item.Icon || normalized.NotifyDomain != item.NotifyDomain ||
			!bytes.Equal(normalized.ConfigCiphertext, item.ConfigCiphertext) {
			return fmt.Errorf("%w: invalid legacy payment id %d", ErrInvalidInput, item.ID)
		}
		if !utf8.ValidString(item.UUID) || strings.IndexFunc(item.UUID, unicode.IsControl) >= 0 {
			return fmt.Errorf("%w: invalid legacy payment id %d", ErrInvalidInput, item.ID)
		}
		if _, duplicate := ids[item.ID]; duplicate {
			return fmt.Errorf("%w: duplicate legacy payment id %d", ErrConflict, item.ID)
		}
		if _, duplicate := uuids[item.UUID]; duplicate {
			return fmt.Errorf("%w: duplicate legacy payment UUID", ErrConflict)
		}
		if _, duplicate := sorts[item.SortPosition]; duplicate {
			return fmt.Errorf("%w: duplicate legacy payment sort position", ErrConflict)
		}
		ids[item.ID] = struct{}{}
		uuids[item.UUID] = struct{}{}
		sorts[item.SortPosition] = struct{}{}
	}
	return nil
}

func (s *Store) LookupLegacyPaymentsImport(ctx context.Context, sourceSHA256 string) (LegacyPaymentsImportReport, bool, error) {
	if !validLowerSHA256(sourceSHA256) {
		return LegacyPaymentsImportReport{}, false, ErrInvalidInput
	}
	var encoded string
	err := s.db.QueryRowContext(ctx, `SELECT report_json FROM legacy_migration_runs WHERE slice = ? AND source_sha256 = ?`, LegacyPaymentsSlice, sourceSHA256).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return LegacyPaymentsImportReport{}, false, nil
	}
	if err != nil {
		return LegacyPaymentsImportReport{}, false, fmt.Errorf("lookup legacy payment migration: %w", err)
	}
	var report LegacyPaymentsImportReport
	if err := json.Unmarshal([]byte(encoded), &report); err != nil {
		return LegacyPaymentsImportReport{}, false, fmt.Errorf("decode legacy payment migration report: %w", err)
	}
	report.AlreadyApplied = true
	return report, true, nil
}

func (s *Store) ImportLegacyPayments(ctx context.Context, input LegacyPaymentsImport, now time.Time) (LegacyPaymentsImportReport, error) {
	if err := validateLegacyPaymentsImport(input); err != nil {
		return LegacyPaymentsImportReport{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LegacyPaymentsImportReport{}, fmt.Errorf("begin legacy payment import: %w", err)
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return LegacyPaymentsImportReport{}, fmt.Errorf("read legacy payment target schema: %w", err)
	}
	if version != CurrentSchemaVersion() {
		return LegacyPaymentsImportReport{}, fmt.Errorf("legacy payment import requires current schema %d, found %d", CurrentSchemaVersion(), version)
	}
	if err := ValidateSchema(ctx, tx, version); err != nil {
		return LegacyPaymentsImportReport{}, fmt.Errorf("validate legacy payment target schema: %w", err)
	}
	if existing, found, err := lookupLegacyPaymentsImportTx(ctx, tx, input.SourceSHA256); err != nil {
		return LegacyPaymentsImportReport{}, err
	} else if found {
		if err := tx.Commit(); err != nil {
			return LegacyPaymentsImportReport{}, fmt.Errorf("commit idempotent legacy payment import: %w", err)
		}
		existing.AlreadyApplied = true
		return existing, nil
	}
	var otherRuns, targetRows int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_migration_runs WHERE slice = ?`, LegacyPaymentsSlice).Scan(&otherRuns); err != nil {
		return LegacyPaymentsImportReport{}, fmt.Errorf("count legacy payment migrations: %w", err)
	}
	if otherRuns != 0 {
		return LegacyPaymentsImportReport{}, fmt.Errorf("%w: legacy payment slice was already imported from another snapshot", ErrConflict)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM payments`).Scan(&targetRows); err != nil {
		return LegacyPaymentsImportReport{}, fmt.Errorf("count target payments: %w", err)
	}
	if targetRows != 0 {
		return LegacyPaymentsImportReport{}, fmt.Errorf("%w: legacy payment import requires an empty target payment table", ErrConflict)
	}
	statement, err := tx.PrepareContext(ctx, `
		INSERT INTO payments (
			id, uuid, provider, name, icon, config_ciphertext, notify_domain, handling_fee_fixed,
			handling_fee_basis_points, enabled, sort_position, revision, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
	`)
	if err != nil {
		return LegacyPaymentsImportReport{}, fmt.Errorf("prepare legacy payment import: %w", err)
	}
	defer statement.Close()
	for _, item := range input.Payments {
		if _, err := statement.ExecContext(ctx, item.ID, item.UUID, item.Provider, item.Name, nullableText(item.Icon), item.ConfigCiphertext,
			nullableText(item.NotifyDomain), item.HandlingFeeFixed, item.HandlingFeeBasisPoints, item.Enabled,
			item.SortPosition, item.CreatedAt, item.UpdatedAt); err != nil {
			return LegacyPaymentsImportReport{}, fmt.Errorf("import legacy payment id %d: %w", item.ID, err)
		}
	}
	if err := validateExistingOrderPaymentReferences(ctx, tx); err != nil {
		return LegacyPaymentsImportReport{}, err
	}
	target, err := readLegacyTargetPayments(ctx, tx)
	if err != nil {
		return LegacyPaymentsImportReport{}, err
	}
	report := LegacyPaymentsImportReport{
		Slice: input.Slice, SourceSHA256: input.SourceSHA256, SourceSize: input.SourceSize,
		RollbackBackupPath: input.RollbackBackupPath, RollbackBackupSHA256: input.RollbackBackupSHA256,
		PlaintextSourceChecksum: input.PlaintextSourceChecksum,
		Payments:                LegacyDomainResult{SourceRows: len(input.Payments), TargetRows: len(target), SourceChecksum: input.PaymentsChecksum, TargetChecksum: LegacyPaymentsChecksum(target)},
		AppliedAt:               now.UTC(),
	}
	if report.Payments.SourceRows != report.Payments.TargetRows || report.Payments.SourceChecksum != report.Payments.TargetChecksum {
		return LegacyPaymentsImportReport{}, errors.New("legacy payment target verification does not match source")
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return LegacyPaymentsImportReport{}, fmt.Errorf("encode legacy payment migration report: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO legacy_migration_runs (slice, source_sha256, source_size, rollback_backup_path, rollback_backup_sha256, report_json, applied_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		report.Slice, report.SourceSHA256, report.SourceSize, report.RollbackBackupPath, report.RollbackBackupSHA256, string(encoded), report.AppliedAt.Unix()); err != nil {
		return LegacyPaymentsImportReport{}, fmt.Errorf("record legacy payment migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LegacyPaymentsImportReport{}, fmt.Errorf("commit legacy payment import: %w", err)
	}
	return report, nil
}

func validateExistingOrderPaymentReferences(ctx context.Context, tx *sql.Tx) error {
	var missing int64
	err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MIN(orders.payment_id), 0)
		FROM orders LEFT JOIN payments ON payments.id = orders.payment_id
		WHERE orders.payment_id IS NOT NULL AND payments.id IS NULL
	`).Scan(&missing)
	if err != nil {
		return fmt.Errorf("validate imported order payment references: %w", err)
	}
	if missing != 0 {
		return fmt.Errorf("%w: existing orders reference missing payment %d", ErrConflict, missing)
	}
	return nil
}

func validateLegacyPaymentsImport(input LegacyPaymentsImport) error {
	if input.Slice != LegacyPaymentsSlice || !validLowerSHA256(input.SourceSHA256) || input.SourceSize < 1 ||
		input.RollbackBackupPath == "" || len(input.RollbackBackupPath) > 4096 || !utf8.ValidString(input.RollbackBackupPath) ||
		strings.IndexFunc(input.RollbackBackupPath, unicode.IsControl) >= 0 || !validLowerSHA256(input.RollbackBackupSHA256) ||
		!validLowerSHA256(input.PlaintextSourceChecksum) || input.PaymentsChecksum != LegacyPaymentsChecksum(input.Payments) {
		return fmt.Errorf("%w: invalid legacy payment import", ErrInvalidInput)
	}
	return ValidateLegacyPaymentsData(input.Payments)
}

func lookupLegacyPaymentsImportTx(ctx context.Context, tx *sql.Tx, sourceSHA256 string) (LegacyPaymentsImportReport, bool, error) {
	var encoded string
	err := tx.QueryRowContext(ctx, `SELECT report_json FROM legacy_migration_runs WHERE slice = ? AND source_sha256 = ?`, LegacyPaymentsSlice, sourceSHA256).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return LegacyPaymentsImportReport{}, false, nil
	}
	if err != nil {
		return LegacyPaymentsImportReport{}, false, fmt.Errorf("lookup legacy payment migration: %w", err)
	}
	var report LegacyPaymentsImportReport
	if err := json.Unmarshal([]byte(encoded), &report); err != nil {
		return LegacyPaymentsImportReport{}, false, fmt.Errorf("decode legacy payment migration report: %w", err)
	}
	return report, true, nil
}

func readLegacyTargetPayments(ctx context.Context, database queryer) ([]LegacyPayment, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id, uuid, provider, name, icon, config_ciphertext, notify_domain, handling_fee_fixed,
		       handling_fee_basis_points, enabled, sort_position, created_at, updated_at
		FROM payments ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("read imported legacy payments: %w", err)
	}
	defer rows.Close()
	payments := make([]LegacyPayment, 0)
	for rows.Next() {
		var item LegacyPayment
		var icon, notifyDomain sql.NullString
		if err := rows.Scan(&item.ID, &item.UUID, &item.Provider, &item.Name, &icon, &item.ConfigCiphertext,
			&notifyDomain, &item.HandlingFeeFixed, &item.HandlingFeeBasisPoints, &item.Enabled,
			&item.SortPosition, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan imported legacy payment: %w", err)
		}
		item.Icon = icon.String
		item.NotifyDomain = notifyDomain.String
		item.ConfigCiphertext = append([]byte(nil), item.ConfigCiphertext...)
		payments = append(payments, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate imported legacy payments: %w", err)
	}
	return payments, nil
}
