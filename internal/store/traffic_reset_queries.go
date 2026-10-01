package store

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

type TrafficResetFilter struct {
	UserID                   int64
	Email, ResetType, Source string
	Start, End               *time.Time
	Page, PageSize           int
}
type TrafficResetEntry struct {
	ID, UserID, UploadBefore, DownloadBefore, UploadAfter, DownloadAfter int64
	AdministratorID                                                      *int64
	AdministratorEmail                                                   *string
	Email, ResetType, Source, Reason                                     string
	ResetAt                                                              time.Time
}
type TrafficResetLogPage struct {
	Items          []TrafficResetEntry
	Total          int64
	Page, PageSize int
}
type TrafficResetStats struct {
	Total  int64 `json:"total_resets"`
	Auto   int64 `json:"auto_resets"`
	Manual int64 `json:"manual_resets"`
	Cron   int64 `json:"cron_resets"`
}

const trafficResetQuery = ` FROM traffic_reset_logs l JOIN users u ON u.id=l.user_id LEFT JOIN plans p ON p.id=l.plan_id CROSS JOIN app_settings settings `
const trafficResetTypeExpression = `CASE WHEN l.trigger_source='manual' THEN 'manual' ELSE CASE COALESCE(l.reset_method,p.reset_traffic_method,settings.traffic_reset_method) WHEN 0 THEN 'first_day_month' WHEN 1 THEN 'monthly' WHEN 3 THEN 'first_day_year' WHEN 4 THEN 'yearly' ELSE 'manual' END END`
const trafficResetSourceExpression = `CASE l.trigger_source WHEN 'scheduled' THEN 'cron' ELSE l.trigger_source END`

func trafficResetWhere(f TrafficResetFilter) (string, []any, error) {
	if f.UserID < 0 || !utf8.ValidString(f.Email) || len(f.Email) > 320 || f.Page < 1 || f.Page > 1000000 || f.PageSize < 1 || f.PageSize > 10000 || f.Start != nil && f.End != nil && !f.Start.Before(*f.End) {
		return "", nil, ErrInvalidInput
	}
	clauses := []string{"1=1"}
	args := []any{}
	if f.UserID > 0 {
		clauses = append(clauses, "l.user_id=?")
		args = append(args, f.UserID)
	}
	if f.Email != "" {
		clauses = append(clauses, `u.email LIKE ? ESCAPE '\'`)
		args = append(args, "%"+strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(f.Email)+"%")
	}
	if f.ResetType != "" {
		switch f.ResetType {
		case "monthly", "first_day_month", "yearly", "first_day_year", "manual", "purchase":
		default:
			return "", nil, ErrInvalidInput
		}
		clauses = append(clauses, "("+trafficResetTypeExpression+")=?")
		args = append(args, f.ResetType)
	}
	if f.Source != "" {
		switch f.Source {
		case "auto", "manual", "api", "cron", "user_access", "order", "gift_card":
		default:
			return "", nil, ErrInvalidInput
		}
		clauses = append(clauses, "("+trafficResetSourceExpression+")=?")
		args = append(args, f.Source)
	}
	if f.Start != nil {
		clauses = append(clauses, "l.reset_at>=?")
		args = append(args, f.Start.Unix())
	}
	if f.End != nil {
		clauses = append(clauses, "l.reset_at<?")
		args = append(args, f.End.Unix())
	}
	return " WHERE " + strings.Join(clauses, " AND "), args, nil
}
func (s *Store) ListTrafficResetLogs(ctx context.Context, f TrafficResetFilter) (TrafficResetLogPage, error) {
	result := TrafficResetLogPage{Items: []TrafficResetEntry{}, Page: f.Page, PageSize: f.PageSize}
	where, args, err := trafficResetWhere(f)
	if err != nil {
		return result, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*)"+trafficResetQuery+where, args...).Scan(&result.Total); err != nil {
		return result, err
	}
	query := `SELECT l.id,l.user_id,u.email,l.reset_at,l.upload_before,l.download_before,l.upload_after,l.download_after,COALESCE(l.reason,''),l.administrator_id,l.administrator_email,` + trafficResetTypeExpression + `,` + trafficResetSourceExpression + trafficResetQuery + where + ` ORDER BY l.reset_at DESC,l.id DESC LIMIT ? OFFSET ?`
	args = append(args, f.PageSize, (f.Page-1)*f.PageSize)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var e TrafficResetEntry
		var at int64
		if err = rows.Scan(&e.ID, &e.UserID, &e.Email, &at, &e.UploadBefore, &e.DownloadBefore, &e.UploadAfter, &e.DownloadAfter, &e.Reason, &e.AdministratorID, &e.AdministratorEmail, &e.ResetType, &e.Source); err != nil {
			rows.Close()
			return result, err
		}
		e.ResetAt = time.Unix(at, 0).UTC()
		result.Items = append(result.Items, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	return result, tx.Commit()
}
func (s *Store) GetTrafficResetStats(ctx context.Context, days int, now time.Time) (TrafficResetStats, error) {
	var result TrafficResetStats
	if days < 1 || days > 365 {
		return result, ErrInvalidInput
	}
	if trafficResetLocationError != nil {
		return result, fmt.Errorf("traffic reset timezone: %w", trafficResetLocationError)
	}
	local := now.In(trafficResetLocation).AddDate(0, 0, -days)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, trafficResetLocation)
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(trigger_source='manual'),0),COALESCE(SUM(trigger_source='scheduled'),0) FROM traffic_reset_logs WHERE reset_at>=?`, start.Unix()).Scan(&result.Total, &result.Manual, &result.Cron)
	return result, err
}
