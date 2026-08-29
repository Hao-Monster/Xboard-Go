package legacymigration

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

type MailTemplatesSnapshot struct {
	Path      string
	Size      int64
	SHA256    string
	Templates []store.LegacyMailTemplate
	Checksum  string
}

func ReadMailTemplatesSnapshot(ctx context.Context, sourcePath string) (MailTemplatesSnapshot, error) {
	templates := make([]store.LegacyMailTemplate, 0, 5)
	identity, err := readLegacySnapshot(ctx, sourcePath, func(database *sql.DB) error {
		if err := requireRealTable(ctx, database, "v2_mail_templates", []string{"name", "subject", "content"}); err != nil {
			return err
		}
		if err := validateLegacyQueryBudget(ctx, database, `
			SELECT COUNT(*), COALESCE(SUM(length(CAST(name AS BLOB)) + length(CAST(subject AS BLOB)) + length(CAST(content AS BLOB))), 0)
			FROM v2_mail_templates
		`, 5, 5*(64+1024+262144), "legacy mail templates"); err != nil {
			return err
		}
		rows, err := database.QueryContext(ctx, `SELECT name,subject,content FROM v2_mail_templates ORDER BY name`)
		if err != nil {
			return fmt.Errorf("read legacy mail templates: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var template store.LegacyMailTemplate
			if err := rows.Scan(&template.Name, &template.Subject, &template.Content); err != nil {
				return fmt.Errorf("scan legacy mail template: %w", err)
			}
			templates = append(templates, template)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate legacy mail templates: %w", err)
		}
		if err := store.ValidateLegacyMailTemplatesData(templates); err != nil {
			return fmt.Errorf("validate legacy mail templates: %w", err)
		}
		return nil
	})
	if err != nil {
		return MailTemplatesSnapshot{}, err
	}
	return MailTemplatesSnapshot{
		Path: identity.Path, Size: identity.Size, SHA256: identity.SHA256,
		Templates: templates, Checksum: store.LegacyMailTemplatesChecksum(templates),
	}, nil
}
