package store

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	maxPaymentListSize    = 200
	maxPaymentNameBytes   = 255
	maxPaymentConfigBytes = 8192
	maxPaymentQueryBytes  = 255
)

const paymentUUIDAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"

func (s *Store) CreatePayment(ctx context.Context, input SavePaymentInput, now time.Time) (Payment, error) {
	normalized, err := normalizePaymentInput(input, now)
	if err != nil {
		return Payment{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Payment{}, fmt.Errorf("begin create payment: %w", err)
	}
	defer tx.Rollback()
	var sortPosition int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sort_position), 0) + 1 FROM payments`).Scan(&sortPosition); err != nil {
		return Payment{}, fmt.Errorf("allocate payment sort position: %w", err)
	}
	var payment Payment
	for attempt := 0; attempt < 8; attempt++ {
		uuid, uuidErr := newPaymentUUID()
		if uuidErr != nil {
			return Payment{}, uuidErr
		}
		result, insertErr := tx.ExecContext(ctx, `
			INSERT INTO payments (
				uuid, provider, name, icon, config_ciphertext, notify_domain, handling_fee_fixed,
				handling_fee_basis_points, enabled, sort_position, revision, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
		`, uuid, normalized.Provider, normalized.Name, nullableText(normalized.Icon), normalized.ConfigCiphertext,
			nullableText(normalized.NotifyDomain), normalized.HandlingFeeFixed, normalized.HandlingFeeBasisPoints,
			normalized.Enabled, sortPosition, now.Unix(), now.Unix())
		if insertErr != nil {
			var exists bool
			if queryErr := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM payments WHERE uuid = ?)`, uuid).Scan(&exists); queryErr == nil && exists {
				continue
			}
			return Payment{}, fmt.Errorf("create payment: %w", insertErr)
		}
		id, idErr := result.LastInsertId()
		if idErr != nil {
			return Payment{}, fmt.Errorf("read payment ID: %w", idErr)
		}
		payment, err = getPayment(ctx, tx, id)
		if err != nil {
			return Payment{}, err
		}
		break
	}
	if payment.ID == 0 {
		return Payment{}, fmt.Errorf("%w: payment UUID collision", ErrConflict)
	}
	if err := tx.Commit(); err != nil {
		return Payment{}, fmt.Errorf("commit create payment: %w", err)
	}
	return payment, nil
}

func (s *Store) UpdatePayment(ctx context.Context, paymentID, revision int64, input SavePaymentInput, now time.Time) (Payment, error) {
	if paymentID < 1 || revision < 1 {
		return Payment{}, ErrInvalidInput
	}
	normalized, err := normalizePaymentInput(input, now)
	if err != nil {
		return Payment{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Payment{}, fmt.Errorf("begin update payment: %w", err)
	}
	defer tx.Rollback()
	var currentProvider PaymentProvider
	var currentConfig []byte
	var currentRevision int64
	if err := tx.QueryRowContext(ctx, `SELECT provider, config_ciphertext, revision FROM payments WHERE id = ?`, paymentID).Scan(&currentProvider, &currentConfig, &currentRevision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Payment{}, ErrNotFound
		}
		return Payment{}, fmt.Errorf("read payment before update: %w", err)
	}
	if currentRevision != revision {
		return Payment{}, ErrRevisionConflict
	}
	if currentProvider != normalized.Provider || !bytes.Equal(currentConfig, normalized.ConfigCiphertext) {
		var createdCheckout bool
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM payment_checkout_attempts AS attempt
				WHERE attempt.payment_id = ? AND attempt.status IN (0, 1)
			)
		`, paymentID).Scan(&createdCheckout); err != nil {
			return Payment{}, fmt.Errorf("check created payment checkout: %w", err)
		}
		if createdCheckout {
			return Payment{}, ErrPaymentConfigInUse
		}
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE payments SET provider = ?, name = ?, icon = ?, config_ciphertext = ?, notify_domain = ?,
			handling_fee_fixed = ?, handling_fee_basis_points = ?, enabled = ?, revision = revision + 1, updated_at = ?
		WHERE id = ? AND revision = ?
	`, normalized.Provider, normalized.Name, nullableText(normalized.Icon), normalized.ConfigCiphertext,
		nullableText(normalized.NotifyDomain), normalized.HandlingFeeFixed, normalized.HandlingFeeBasisPoints,
		normalized.Enabled, now.Unix(), paymentID, revision)
	if err != nil {
		return Payment{}, fmt.Errorf("update payment: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return Payment{}, fmt.Errorf("count updated payment: %w", err)
	}
	if updated != 1 {
		return Payment{}, ErrRevisionConflict
	}
	if err := tx.Commit(); err != nil {
		return Payment{}, fmt.Errorf("commit update payment: %w", err)
	}
	return s.GetPayment(ctx, paymentID)
}

func (s *Store) SetPaymentEnabled(ctx context.Context, paymentID int64, enabled bool, now time.Time) (Payment, error) {
	if paymentID < 1 || now.Unix() < 0 {
		return Payment{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `UPDATE payments SET enabled = ?, revision = revision + 1, updated_at = ? WHERE id = ?`, enabled, now.Unix(), paymentID)
	if err != nil {
		return Payment{}, fmt.Errorf("set payment enabled: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return Payment{}, fmt.Errorf("count payment enabled update: %w", err)
	}
	if updated != 1 {
		return Payment{}, ErrNotFound
	}
	return s.GetPayment(ctx, paymentID)
}

func (s *Store) GetPayment(ctx context.Context, paymentID int64) (Payment, error) {
	if paymentID < 1 {
		return Payment{}, ErrInvalidInput
	}
	return getPayment(ctx, s.db, paymentID)
}

func (s *Store) GetPaymentByUUID(ctx context.Context, uuid string) (Payment, error) {
	uuid = strings.TrimSpace(uuid)
	if !validPaymentUUID(uuid) {
		return Payment{}, ErrInvalidInput
	}
	return scanPayment(s.db.QueryRowContext(ctx, paymentSelect+` WHERE uuid = ?`, uuid))
}

func (s *Store) ListStoredPaymentConfigs(ctx context.Context) ([]StoredPaymentConfig, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT provider, config_ciphertext FROM payments ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list stored payment configurations: %w", err)
	}
	defer rows.Close()
	configs := make([]StoredPaymentConfig, 0)
	for rows.Next() {
		var config StoredPaymentConfig
		if err := rows.Scan(&config.Provider, &config.Ciphertext); err != nil {
			return nil, fmt.Errorf("scan stored payment configuration: %w", err)
		}
		configs = append(configs, config)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate stored payment configurations: %w", err)
	}
	return configs, nil
}

func (s *Store) ListPayments(ctx context.Context, filter PaymentFilter) (PaymentPage, error) {
	if filter.Page < 1 || filter.PageSize < 1 || filter.PageSize > maxPaymentListSize || len(filter.Query) > maxPaymentQueryBytes ||
		(filter.Provider != "" && !validPaymentProvider(filter.Provider)) {
		return PaymentPage{}, ErrInvalidInput
	}
	where := make([]string, 0, 2)
	arguments := make([]any, 0, 4)
	query := strings.TrimSpace(filter.Query)
	if query != "" {
		where = append(where, `(name LIKE ? ESCAPE '\' OR provider LIKE ? ESCAPE '\')`)
		like := "%" + escapeLike(query) + "%"
		arguments = append(arguments, like, like)
	}
	if filter.Provider != "" {
		where = append(where, `provider = ?`)
		arguments = append(arguments, filter.Provider)
	}
	whereSQL := ""
	if len(where) > 0 {
		whereSQL = " WHERE " + strings.Join(where, " AND ")
	}
	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM payments`+whereSQL, arguments...).Scan(&total); err != nil {
		return PaymentPage{}, fmt.Errorf("count payments: %w", err)
	}
	listArguments := append(append([]any(nil), arguments...), filter.PageSize, (filter.Page-1)*filter.PageSize)
	rows, err := s.db.QueryContext(ctx, paymentSelect+whereSQL+` ORDER BY sort_position, id LIMIT ? OFFSET ?`, listArguments...)
	if err != nil {
		return PaymentPage{}, fmt.Errorf("list payments: %w", err)
	}
	defer rows.Close()
	items := make([]Payment, 0, filter.PageSize)
	for rows.Next() {
		payment, scanErr := scanPayment(rows)
		if scanErr != nil {
			return PaymentPage{}, scanErr
		}
		items = append(items, payment)
	}
	if err := rows.Err(); err != nil {
		return PaymentPage{}, fmt.Errorf("iterate payments: %w", err)
	}
	return PaymentPage{Items: items, Total: total, Page: filter.Page, PageSize: filter.PageSize}, nil
}

func (s *Store) ListEnabledPayments(ctx context.Context) ([]Payment, error) {
	rows, err := s.db.QueryContext(ctx, paymentSelect+` WHERE enabled = 1 AND EXISTS (
		SELECT 1 FROM trusted_plugins AS trusted
		WHERE trusted.enabled = 1 AND trusted.code = CASE provider
			WHEN 'AlipayF2F' THEN 'alipay_f2f'
			WHEN 'BTCPay' THEN 'btcpay'
			WHEN 'CoinPayments' THEN 'coin_payments'
			WHEN 'Coinbase' THEN 'coinbase'
			WHEN 'EPay' THEN 'epay'
			WHEN 'MGate' THEN 'mgate'
		END
	) ORDER BY sort_position, id`)
	if err != nil {
		return nil, fmt.Errorf("list enabled payments: %w", err)
	}
	defer rows.Close()
	payments := make([]Payment, 0)
	for rows.Next() {
		payment, scanErr := scanPayment(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		payments = append(payments, payment)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate enabled payments: %w", err)
	}
	return payments, nil
}

func (s *Store) ReorderPayments(ctx context.Context, paymentIDs []int64, now time.Time) error {
	if len(paymentIDs) < 1 || now.Unix() < 0 {
		return ErrInvalidInput
	}
	seen := make(map[int64]struct{}, len(paymentIDs))
	for _, paymentID := range paymentIDs {
		if paymentID < 1 {
			return ErrInvalidInput
		}
		if _, exists := seen[paymentID]; exists {
			return ErrInvalidInput
		}
		seen[paymentID] = struct{}{}
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin reorder payments: %w", err)
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM payments`).Scan(&count); err != nil {
		return fmt.Errorf("count payments for reorder: %w", err)
	}
	if count != len(paymentIDs) {
		return ErrInvalidInput
	}
	for index, paymentID := range paymentIDs {
		result, err := tx.ExecContext(ctx, `UPDATE payments SET sort_position = ?, revision = revision + 1, updated_at = ? WHERE id = ?`, index+1, now.Unix(), paymentID)
		if err != nil {
			return fmt.Errorf("reorder payment: %w", err)
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("count reordered payment: %w", err)
		}
		if updated != 1 {
			return ErrInvalidInput
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit reorder payments: %w", err)
	}
	return nil
}

func (s *Store) DeletePayment(ctx context.Context, paymentID int64) error {
	if paymentID < 1 {
		return ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete payment: %w", err)
	}
	defer tx.Rollback()
	var referenced bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM orders WHERE payment_id = ?)
		    OR EXISTS(SELECT 1 FROM payment_checkout_attempts WHERE payment_id = ?)
		    OR EXISTS(SELECT 1 FROM payment_webhook_receipts WHERE payment_id = ?)
	`, paymentID, paymentID, paymentID).Scan(&referenced); err != nil {
		return fmt.Errorf("check payment references: %w", err)
	}
	if referenced {
		return ErrPaymentReferenced
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM payments WHERE id = ?`, paymentID)
	if err != nil {
		return fmt.Errorf("delete payment: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count deleted payment: %w", err)
	}
	if deleted != 1 {
		return ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete payment: %w", err)
	}
	return nil
}

func PaymentHandlingFee(amount, fixed, basisPoints int64) (int64, error) {
	if amount < 0 || amount > maxOrderMoneyCents || fixed < 0 || fixed > maxOrderMoneyCents || basisPoints < 0 || basisPoints > 10_000 {
		return 0, ErrInvalidInput
	}
	percentage := (amount/10_000)*basisPoints + ((amount%10_000)*basisPoints+5_000)/10_000
	if percentage > maxOrderMoneyCents-fixed {
		return 0, ErrInvalidInput
	}
	return percentage + fixed, nil
}

func normalizePaymentInput(input SavePaymentInput, now time.Time) (SavePaymentInput, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Icon = strings.TrimSpace(input.Icon)
	input.NotifyDomain = strings.TrimRight(strings.TrimSpace(input.NotifyDomain), "/")
	if !validPaymentProvider(input.Provider) || !utf8.ValidString(input.Name) || len(input.Name) < 1 || len(input.Name) > maxPaymentNameBytes ||
		strings.IndexFunc(input.Name, unicode.IsControl) >= 0 || len(input.ConfigCiphertext) < 1 || len(input.ConfigCiphertext) > maxPaymentConfigBytes ||
		input.HandlingFeeFixed < 0 || input.HandlingFeeFixed > maxOrderMoneyCents || input.HandlingFeeBasisPoints < 0 || input.HandlingFeeBasisPoints > 10_000 || now.Unix() < 0 ||
		!validOptionalPaymentURL(input.Icon, 2048) || !validOptionalPaymentURL(input.NotifyDomain, 512) {
		return SavePaymentInput{}, ErrInvalidInput
	}
	input.ConfigCiphertext = append([]byte(nil), input.ConfigCiphertext...)
	return input, nil
}

func validPaymentProvider(provider PaymentProvider) bool {
	switch provider {
	case PaymentProviderAlipayF2F, PaymentProviderBTCPay, PaymentProviderCoinPayments, PaymentProviderCoinbase, PaymentProviderEPay, PaymentProviderMGate:
		return true
	default:
		return false
	}
}

func validOptionalPaymentURL(value string, maximum int) bool {
	if value == "" {
		return true
	}
	if !utf8.ValidString(value) || len(value) > maximum || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Host == "" {
		return false
	}
	if parsed.Scheme == "https" {
		return true
	}
	if parsed.Scheme != "http" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "localhost" || host == "::1" || net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()
}

func getPayment(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, paymentID int64) (Payment, error) {
	return scanPayment(database.QueryRowContext(ctx, paymentSelect+` WHERE id = ?`, paymentID))
}

func scanPayment(row rowScanner) (Payment, error) {
	var payment Payment
	var icon, notifyDomain sql.NullString
	var createdAt, updatedAt int64
	if err := row.Scan(&payment.ID, &payment.UUID, &payment.Provider, &payment.Name, &icon, &payment.ConfigCiphertext,
		&notifyDomain, &payment.HandlingFeeFixed, &payment.HandlingFeeBasisPoints, &payment.Enabled,
		&payment.SortPosition, &payment.Revision, &createdAt, &updatedAt); errors.Is(err, sql.ErrNoRows) {
		return Payment{}, ErrNotFound
	} else if err != nil {
		return Payment{}, fmt.Errorf("scan payment: %w", err)
	}
	payment.Icon = icon.String
	payment.NotifyDomain = notifyDomain.String
	payment.ConfigCiphertext = append([]byte(nil), payment.ConfigCiphertext...)
	payment.CreatedAt = time.Unix(createdAt, 0).UTC()
	payment.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return payment, nil
}

func newPaymentUUID() (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate payment UUID: %w", err)
	}
	result := make([]byte, len(random))
	for index, value := range random {
		result[index] = paymentUUIDAlphabet[int(value)%len(paymentUUIDAlphabet)]
	}
	return string(result), nil
}

func validPaymentUUID(value string) bool {
	if len(value) < 8 || len(value) > 32 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			if character < 'A' || character > 'Z' {
				if character < 'a' || character > 'z' {
					return false
				}
			}
		}
	}
	return true
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

const paymentSelect = `SELECT id, uuid, provider, name, icon, config_ciphertext, notify_domain,
    handling_fee_fixed, handling_fee_basis_points, enabled, sort_position, revision, created_at, updated_at FROM payments`
