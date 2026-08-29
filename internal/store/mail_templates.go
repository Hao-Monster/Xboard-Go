package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/mailtemplate"
)

func (s *Store) ListMailTemplates(ctx context.Context) ([]MailTemplate, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT name, subject, content, revision, updated_at
		FROM mail_templates
		ORDER BY CASE name
			WHEN 'verify' THEN 1 WHEN 'notify' THEN 2 WHEN 'remindExpire' THEN 3
			WHEN 'remindTraffic' THEN 4 WHEN 'mailLogin' THEN 5 ELSE 6 END
	`)
	if err != nil {
		return nil, fmt.Errorf("list mail templates: %w", err)
	}
	defer rows.Close()
	result := make([]MailTemplate, 0, 5)
	for rows.Next() {
		template, err := scanMailTemplate(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, template)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list mail templates: %w", err)
	}
	return result, nil
}

// ListMailTemplateSummaries deliberately excludes subjects and bodies so opening
// the administrator page stays bounded even when every template is near its
// maximum size. The selected template is fetched separately by primary key.
func (s *Store) ListMailTemplateSummaries(ctx context.Context) ([]MailTemplateSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT name, subject IS NOT NULL AND content IS NOT NULL, revision, updated_at
		FROM mail_templates
		ORDER BY CASE name
			WHEN 'verify' THEN 1 WHEN 'notify' THEN 2 WHEN 'remindExpire' THEN 3
			WHEN 'remindTraffic' THEN 4 WHEN 'mailLogin' THEN 5 ELSE 6 END
	`)
	if err != nil {
		return nil, fmt.Errorf("list mail template summaries: %w", err)
	}
	defer rows.Close()
	result := make([]MailTemplateSummary, 0, 5)
	for rows.Next() {
		var name string
		var customized int64
		var revision, updatedAt int64
		if err := rows.Scan(&name, &customized, &revision, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan mail template summary: %w", err)
		}
		definition, ok := mailtemplate.DefinitionFor(mailtemplate.Name(name))
		if !ok {
			return nil, fmt.Errorf("mail template catalog contains unknown name %q", name)
		}
		result = append(result, MailTemplateSummary{
			Name: definition.Name, Label: definition.Label, Customized: customized == 1,
			Revision: revision, UpdatedAt: time.Unix(updatedAt, 0).UTC(),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list mail template summaries: %w", err)
	}
	return result, nil
}

func (s *Store) GetMailTemplate(ctx context.Context, name mailtemplate.Name) (MailTemplate, error) {
	if _, ok := mailtemplate.DefinitionFor(name); !ok {
		return MailTemplate{}, ErrNotFound
	}
	template, err := scanMailTemplate(s.db.QueryRowContext(ctx, `
		SELECT name, subject, content, revision, updated_at FROM mail_templates WHERE name = ?
	`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return MailTemplate{}, ErrNotFound
	}
	return template, err
}

func (s *Store) UpdateMailTemplate(ctx context.Context, administratorID int64, name mailtemplate.Name, revision int64, input SaveMailTemplateInput, now time.Time) (MailTemplate, error) {
	if administratorID < 1 || revision < 1 {
		return MailTemplate{}, ErrInvalidInput
	}
	if _, ok := mailtemplate.DefinitionFor(name); !ok {
		return MailTemplate{}, ErrNotFound
	}
	if err := mailtemplate.Validate(name, input.Subject, input.Content); err != nil {
		return MailTemplate{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		UPDATE mail_templates
		SET subject = ?, content = ?, revision = revision + 1, updated_by = ?, updated_at = ?
		WHERE name = ? AND revision = ?
	`, input.Subject, input.Content, administratorID, now.UTC().Unix(), name, revision)
	if err != nil {
		return MailTemplate{}, fmt.Errorf("update mail template: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		if _, err := s.GetMailTemplate(ctx, name); errors.Is(err, ErrNotFound) {
			return MailTemplate{}, ErrNotFound
		}
		return MailTemplate{}, ErrConflict
	}
	return s.GetMailTemplate(ctx, name)
}

func (s *Store) ResetMailTemplate(ctx context.Context, administratorID int64, name mailtemplate.Name, revision int64, now time.Time) (MailTemplate, error) {
	if administratorID < 1 || revision < 1 {
		return MailTemplate{}, ErrInvalidInput
	}
	if _, ok := mailtemplate.DefinitionFor(name); !ok {
		return MailTemplate{}, ErrNotFound
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		UPDATE mail_templates
		SET subject = NULL, content = NULL, revision = revision + 1, updated_by = ?, updated_at = ?
		WHERE name = ? AND revision = ?
	`, administratorID, now.UTC().Unix(), name, revision)
	if err != nil {
		return MailTemplate{}, fmt.Errorf("reset mail template: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		if _, err := s.GetMailTemplate(ctx, name); errors.Is(err, ErrNotFound) {
			return MailTemplate{}, ErrNotFound
		}
		return MailTemplate{}, ErrConflict
	}
	return s.GetMailTemplate(ctx, name)
}

func scanMailTemplate(row rowScanner) (MailTemplate, error) {
	var name string
	var subject, content sql.NullString
	var revision, updatedAt int64
	if err := row.Scan(&name, &subject, &content, &revision, &updatedAt); err != nil {
		return MailTemplate{}, err
	}
	definition, ok := mailtemplate.DefinitionFor(mailtemplate.Name(name))
	if !ok {
		return MailTemplate{}, fmt.Errorf("mail template catalog contains unknown name %q", name)
	}
	result := MailTemplate{
		Name: definition.Name, Label: definition.Label,
		Subject: definition.DefaultSubject, Content: definition.DefaultContent,
		Required: definition.Required, Optional: definition.Optional,
		Revision: revision, UpdatedAt: time.Unix(updatedAt, 0).UTC(),
	}
	if subject.Valid && content.Valid {
		result.Subject = subject.String
		result.Content = content.String
		result.Customized = true
	}
	return result, nil
}
