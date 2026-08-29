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

	"github.com/Hao-Monster/Xboard-Go/internal/mailtemplate"
)

const LegacyMailTemplatesSlice = "mail-templates-v1"

type LegacyMailTemplate struct {
	Name    mailtemplate.Name `json:"name"`
	Subject string            `json:"subject"`
	Content string            `json:"content"`
}

type LegacyMailTemplatesImport struct {
	Slice, SourceSHA256                      string
	SourceSize                               int64
	Templates                                []LegacyMailTemplate
	Checksum                                 string
	RollbackBackupPath, RollbackBackupSHA256 string
}

type LegacyMailTemplatesImportReport struct {
	Slice                string             `json:"slice"`
	SourceSHA256         string             `json:"source_sha256"`
	SourceSize           int64              `json:"source_size"`
	RollbackBackupPath   string             `json:"rollback_backup_path"`
	RollbackBackupSHA256 string             `json:"rollback_backup_sha256"`
	Templates            LegacyDomainResult `json:"templates"`
	AppliedAt            time.Time          `json:"applied_at"`
	AlreadyApplied       bool               `json:"already_applied"`
}

func LegacyMailTemplatesChecksum(templates []LegacyMailTemplate) string {
	ordered := append([]LegacyMailTemplate(nil), templates...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].Name < ordered[right].Name })
	if ordered == nil {
		ordered = []LegacyMailTemplate{}
	}
	return legacyCanonicalChecksum(ordered)
}

func ValidateLegacyMailTemplatesData(templates []LegacyMailTemplate) error {
	if len(templates) > len(mailtemplate.Definitions()) {
		return fmt.Errorf("%w: too many legacy mail templates", ErrInvalidInput)
	}
	seen := make(map[mailtemplate.Name]struct{}, len(templates))
	for _, template := range templates {
		if _, duplicate := seen[template.Name]; duplicate {
			return fmt.Errorf("%w: duplicate legacy mail template", ErrInvalidInput)
		}
		seen[template.Name] = struct{}{}
		if err := mailtemplate.Validate(template.Name, template.Subject, template.Content); err != nil {
			return fmt.Errorf("%w: invalid legacy mail template %q", ErrInvalidInput, template.Name)
		}
	}
	return nil
}

func (s *Store) LookupLegacyMailTemplatesImport(ctx context.Context, sourceSHA256 string) (LegacyMailTemplatesImportReport, bool, error) {
	if !validLowerSHA256(sourceSHA256) {
		return LegacyMailTemplatesImportReport{}, false, ErrInvalidInput
	}
	return lookupLegacyMailTemplatesImport(ctx, s.db, sourceSHA256)
}

func lookupLegacyMailTemplatesImport(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, sourceSHA256 string) (LegacyMailTemplatesImportReport, bool, error) {
	var encoded string
	err := database.QueryRowContext(ctx, `SELECT report_json FROM legacy_migration_runs WHERE slice=? AND source_sha256=?`, LegacyMailTemplatesSlice, sourceSHA256).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return LegacyMailTemplatesImportReport{}, false, nil
	}
	if err != nil {
		return LegacyMailTemplatesImportReport{}, false, fmt.Errorf("lookup legacy mail template migration: %w", err)
	}
	var report LegacyMailTemplatesImportReport
	if err := json.Unmarshal([]byte(encoded), &report); err != nil {
		return LegacyMailTemplatesImportReport{}, false, fmt.Errorf("decode legacy mail template migration report: %w", err)
	}
	report.AlreadyApplied = true
	return report, true, nil
}

func (s *Store) ImportLegacyMailTemplates(ctx context.Context, input LegacyMailTemplatesImport, now time.Time) (LegacyMailTemplatesImportReport, error) {
	if err := validateLegacyMailTemplatesImport(input); err != nil {
		return LegacyMailTemplatesImportReport{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LegacyMailTemplatesImportReport{}, fmt.Errorf("begin legacy mail template import: %w", err)
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version != CurrentSchemaVersion() {
		return LegacyMailTemplatesImportReport{}, fmt.Errorf("legacy mail template import requires current schema %d, found %d", CurrentSchemaVersion(), version)
	}
	if err := ValidateSchema(ctx, tx, version); err != nil {
		return LegacyMailTemplatesImportReport{}, fmt.Errorf("validate legacy mail template target schema: %w", err)
	}
	if existing, found, err := lookupLegacyMailTemplatesImport(ctx, tx, input.SourceSHA256); err != nil {
		return LegacyMailTemplatesImportReport{}, err
	} else if found {
		if err := tx.Commit(); err != nil {
			return LegacyMailTemplatesImportReport{}, err
		}
		return existing, nil
	}
	var runs int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_migration_runs WHERE slice=?`, LegacyMailTemplatesSlice).Scan(&runs); err != nil {
		return LegacyMailTemplatesImportReport{}, err
	}
	if runs != 0 {
		return LegacyMailTemplatesImportReport{}, fmt.Errorf("%w: legacy mail templates were already imported from another snapshot", ErrConflict)
	}
	var nonPristine int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM mail_templates WHERE subject IS NOT NULL OR content IS NOT NULL OR revision <> 1 OR updated_by IS NOT NULL OR updated_at <> 0`).Scan(&nonPristine); err != nil {
		return LegacyMailTemplatesImportReport{}, err
	}
	if nonPristine != 0 {
		return LegacyMailTemplatesImportReport{}, fmt.Errorf("%w: legacy mail template import requires a pristine target", ErrConflict)
	}
	for _, template := range input.Templates {
		result, err := tx.ExecContext(ctx, `UPDATE mail_templates SET subject=?,content=?,revision=2,updated_by=NULL,updated_at=? WHERE name=? AND revision=1 AND subject IS NULL AND content IS NULL`,
			template.Subject, template.Content, now.UTC().Unix(), template.Name)
		if err != nil {
			return LegacyMailTemplatesImportReport{}, fmt.Errorf("write legacy mail template %q: %w", template.Name, err)
		}
		rows, _ := result.RowsAffected()
		if rows != 1 {
			return LegacyMailTemplatesImportReport{}, errors.New("legacy mail template target catalog changed during import")
		}
	}
	target, err := readLegacyMailTemplateTarget(ctx, tx)
	if err != nil {
		return LegacyMailTemplatesImportReport{}, err
	}
	report := LegacyMailTemplatesImportReport{
		Slice: input.Slice, SourceSHA256: input.SourceSHA256, SourceSize: input.SourceSize,
		RollbackBackupPath: input.RollbackBackupPath, RollbackBackupSHA256: input.RollbackBackupSHA256,
		Templates: LegacyDomainResult{
			SourceRows: len(input.Templates), TargetRows: len(target),
			SourceChecksum: input.Checksum, TargetChecksum: LegacyMailTemplatesChecksum(target),
		},
		AppliedAt: now.UTC(),
	}
	if report.Templates.SourceRows != report.Templates.TargetRows || report.Templates.SourceChecksum != report.Templates.TargetChecksum {
		return LegacyMailTemplatesImportReport{}, errors.New("legacy mail template target verification does not match source")
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return LegacyMailTemplatesImportReport{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO legacy_migration_runs(slice,source_sha256,source_size,rollback_backup_path,rollback_backup_sha256,report_json,applied_at) VALUES(?,?,?,?,?,?,?)`,
		report.Slice, report.SourceSHA256, report.SourceSize, report.RollbackBackupPath, report.RollbackBackupSHA256, string(encoded), report.AppliedAt.Unix()); err != nil {
		return LegacyMailTemplatesImportReport{}, fmt.Errorf("record legacy mail template migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LegacyMailTemplatesImportReport{}, err
	}
	return report, nil
}

func validateLegacyMailTemplatesImport(input LegacyMailTemplatesImport) error {
	if input.Slice != LegacyMailTemplatesSlice || !validLowerSHA256(input.SourceSHA256) || input.SourceSize < 1 ||
		input.RollbackBackupPath == "" || len(input.RollbackBackupPath) > 4096 || !utf8.ValidString(input.RollbackBackupPath) || strings.IndexFunc(input.RollbackBackupPath, unicode.IsControl) >= 0 ||
		!validLowerSHA256(input.RollbackBackupSHA256) || input.Checksum != LegacyMailTemplatesChecksum(input.Templates) {
		return fmt.Errorf("%w: invalid legacy mail template import", ErrInvalidInput)
	}
	return ValidateLegacyMailTemplatesData(input.Templates)
}

func readLegacyMailTemplateTarget(ctx context.Context, database interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}) ([]LegacyMailTemplate, error) {
	rows, err := database.QueryContext(ctx, `SELECT name,subject,content FROM mail_templates WHERE subject IS NOT NULL ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("verify legacy mail templates: %w", err)
	}
	defer rows.Close()
	result := make([]LegacyMailTemplate, 0, 5)
	for rows.Next() {
		var template LegacyMailTemplate
		if err := rows.Scan(&template.Name, &template.Subject, &template.Content); err != nil {
			return nil, fmt.Errorf("verify legacy mail templates: %w", err)
		}
		result = append(result, template)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("verify legacy mail templates: %w", err)
	}
	return result, nil
}
