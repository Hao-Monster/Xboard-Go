package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const paymentCheckoutLease = 2 * time.Minute

func (s *Store) StartPaymentCheckout(ctx context.Context, input StartPaymentCheckoutInput, now time.Time) (PaymentCheckoutStart, error) {
	if input.UserID < 1 || input.PaymentID < 1 || !validTradeNo(input.TradeNo) || now.Unix() < 0 {
		return PaymentCheckoutStart{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PaymentCheckoutStart{}, fmt.Errorf("begin payment checkout: %w", err)
	}
	defer tx.Rollback()
	order, err := scanOrder(tx.QueryRowContext(ctx, orderSelect+` WHERE o.user_id = ? AND o.trade_no = ?`, input.UserID, input.TradeNo))
	if err != nil {
		return PaymentCheckoutStart{}, err
	}
	if order.Status != OrderStatusPending || order.TotalAmount <= 0 {
		return PaymentCheckoutStart{}, ErrOrderState
	}
	method, err := getPayment(ctx, tx, input.PaymentID)
	if errors.Is(err, ErrNotFound) || err == nil && !method.Enabled {
		return PaymentCheckoutStart{}, ErrPaymentUnavailable
	}
	if err != nil {
		return PaymentCheckoutStart{}, err
	}
	pluginCode, ok := TrustedPluginCodeForPaymentProvider(method.Provider)
	if !ok {
		return PaymentCheckoutStart{}, ErrPaymentUnavailable
	}
	var pluginEnabled bool
	if err := tx.QueryRowContext(ctx, `SELECT enabled FROM trusted_plugins WHERE code = ?`, pluginCode).Scan(&pluginEnabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PaymentCheckoutStart{}, ErrPaymentUnavailable
		}
		return PaymentCheckoutStart{}, fmt.Errorf("read payment plugin status: %w", err)
	}
	if !pluginEnabled {
		return PaymentCheckoutStart{}, ErrPaymentUnavailable
	}
	fee, err := PaymentHandlingFee(order.TotalAmount, method.HandlingFeeFixed, method.HandlingFeeBasisPoints)
	if err != nil || order.TotalAmount > maxOrderMoneyCents-fee {
		return PaymentCheckoutStart{}, ErrInvalidInput
	}
	expectedAmount := order.TotalAmount + fee
	attempt, findErr := getPaymentCheckoutByOrderAndMethod(ctx, tx, order.ID, method.ID)
	cached := false
	if findErr == nil {
		switch attempt.Status {
		case PaymentCheckoutCreated:
			cached = true
		case PaymentCheckoutCreating:
			if attempt.UpdatedAt.Add(paymentCheckoutLease).After(now) {
				return PaymentCheckoutStart{}, ErrPaymentInProgress
			}
		}
		if !cached {
			result, updateErr := tx.ExecContext(ctx, `
			UPDATE payment_checkout_attempts SET expected_amount = ?, currency = 'CNY', status = 0,
				external_id = NULL, response_type = NULL, response_data = NULL, error_code = NULL, updated_at = ?
			WHERE id = ?
		`, expectedAmount, now.Unix(), attempt.ID)
			if updateErr != nil {
				return PaymentCheckoutStart{}, fmt.Errorf("restart payment checkout: %w", updateErr)
			}
			updated, updateErr := result.RowsAffected()
			if updateErr != nil || updated != 1 {
				return PaymentCheckoutStart{}, fmt.Errorf("restart payment checkout: unexpected updated rows")
			}
			attempt.ExpectedAmount = expectedAmount
			attempt.Currency = "CNY"
			attempt.Status = PaymentCheckoutCreating
			attempt.ExternalID = ""
			attempt.ResponseType = nil
			attempt.ResponseData = ""
			attempt.ErrorCode = ""
			attempt.UpdatedAt = now.UTC()
		}
	} else if !errors.Is(findErr, ErrNotFound) {
		return PaymentCheckoutStart{}, findErr
	} else {
		idempotencyKey, keyErr := newPaymentIdempotencyKey()
		if keyErr != nil {
			return PaymentCheckoutStart{}, keyErr
		}
		result, insertErr := tx.ExecContext(ctx, `
			INSERT INTO payment_checkout_attempts (
				order_id, payment_id, idempotency_key, expected_amount, currency, status, created_at, updated_at
			) VALUES (?, ?, ?, ?, 'CNY', 0, ?, ?)
		`, order.ID, method.ID, idempotencyKey, expectedAmount, now.Unix(), now.Unix())
		if insertErr != nil {
			return PaymentCheckoutStart{}, fmt.Errorf("create payment checkout: %w", insertErr)
		}
		attemptID, idErr := result.LastInsertId()
		if idErr != nil {
			return PaymentCheckoutStart{}, fmt.Errorf("read payment checkout ID: %w", idErr)
		}
		attempt = PaymentCheckoutAttempt{
			ID: attemptID, OrderID: order.ID, PaymentID: method.ID, IdempotencyKey: idempotencyKey,
			ExpectedAmount: expectedAmount, Currency: "CNY", Status: PaymentCheckoutCreating,
			CreatedAt: now.UTC(), UpdatedAt: now.UTC(),
		}
	}
	if cached {
		if attempt.ExpectedAmount < order.TotalAmount {
			return PaymentCheckoutStart{}, fmt.Errorf("%w: cached payment amount is below the order total", ErrConflict)
		}
		expectedAmount = attempt.ExpectedAmount
		fee = expectedAmount - order.TotalAmount
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE orders SET payment_id = ?, handling_amount = ?, updated_at = ? WHERE id = ? AND status = 0
	`, method.ID, nullablePositiveMoney(fee), now.Unix(), order.ID)
	if err != nil {
		return PaymentCheckoutStart{}, fmt.Errorf("bind order payment: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return PaymentCheckoutStart{}, fmt.Errorf("count bound order payment: %w", err)
	}
	if updated != 1 {
		return PaymentCheckoutStart{}, ErrOrderState
	}
	order.PaymentID = &method.ID
	order.HandlingAmount = nil
	if fee > 0 {
		order.HandlingAmount = &fee
	}
	order.UpdatedAt = now.UTC()
	if err := tx.Commit(); err != nil {
		return PaymentCheckoutStart{}, fmt.Errorf("commit payment checkout: %w", err)
	}
	return PaymentCheckoutStart{Attempt: attempt, Payment: method, Order: order, Cached: cached}, nil
}

func (s *Store) CompletePaymentCheckout(ctx context.Context, attemptID int64, idempotencyKey string, responseType int, responseData, externalID string, now time.Time) (PaymentCheckoutAttempt, error) {
	if attemptID < 1 || !validPaymentIdempotencyKey(idempotencyKey) || (responseType != 0 && responseType != 1) ||
		!validPaymentResponseData(responseData) || !validOptionalExternalPaymentID(externalID) || now.Unix() < 0 {
		return PaymentCheckoutAttempt{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PaymentCheckoutAttempt{}, fmt.Errorf("begin complete payment checkout: %w", err)
	}
	defer tx.Rollback()
	attempt, err := getPaymentCheckout(ctx, tx, attemptID)
	if err != nil {
		return PaymentCheckoutAttempt{}, err
	}
	if attempt.IdempotencyKey != idempotencyKey {
		return PaymentCheckoutAttempt{}, ErrNotFound
	}
	if attempt.Status == PaymentCheckoutCreated {
		if attempt.ResponseType == nil || *attempt.ResponseType != responseType || attempt.ResponseData != responseData || attempt.ExternalID != externalID {
			return PaymentCheckoutAttempt{}, ErrConflict
		}
		return attempt, nil
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE payment_checkout_attempts SET status = 1, external_id = ?, response_type = ?, response_data = ?,
			error_code = NULL, updated_at = ? WHERE id = ? AND status = 0
	`, nullableText(externalID), responseType, responseData, now.Unix(), attempt.ID)
	if err != nil {
		return PaymentCheckoutAttempt{}, fmt.Errorf("complete payment checkout: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return PaymentCheckoutAttempt{}, fmt.Errorf("count completed payment checkout: %w", err)
	}
	if updated != 1 {
		return PaymentCheckoutAttempt{}, ErrConflict
	}
	attempt.Status = PaymentCheckoutCreated
	attempt.ExternalID = externalID
	attempt.ResponseType = &responseType
	attempt.ResponseData = responseData
	attempt.ErrorCode = ""
	attempt.UpdatedAt = now.UTC()
	if err := tx.Commit(); err != nil {
		return PaymentCheckoutAttempt{}, fmt.Errorf("commit complete payment checkout: %w", err)
	}
	return attempt, nil
}

func (s *Store) FailPaymentCheckout(ctx context.Context, attemptID int64, idempotencyKey, errorCode string, now time.Time) error {
	errorCode = strings.TrimSpace(errorCode)
	if attemptID < 1 || !validPaymentIdempotencyKey(idempotencyKey) || len(errorCode) < 1 || len(errorCode) > 64 ||
		strings.IndexFunc(errorCode, unicode.IsControl) >= 0 || now.Unix() < 0 {
		return ErrInvalidInput
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		UPDATE payment_checkout_attempts SET status = 2, external_id = NULL, response_type = NULL,
			response_data = NULL, error_code = ?, updated_at = ?
		WHERE id = ? AND idempotency_key = ? AND status = 0
	`, errorCode, now.Unix(), attemptID, idempotencyKey)
	if err != nil {
		return fmt.Errorf("fail payment checkout: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count failed payment checkout: %w", err)
	}
	if updated != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) CompletePaymentWebhook(ctx context.Context, input CompletePaymentWebhookInput, now time.Time) (Order, error) {
	input.ExternalID = strings.TrimSpace(input.ExternalID)
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	input.PayloadSHA256 = strings.ToLower(strings.TrimSpace(input.PayloadSHA256))
	if input.PaymentID < 1 || !validPaymentProvider(input.Provider) || !validExternalPaymentID(input.ExternalID) ||
		!validTradeNo(input.TradeNo) || input.Amount < 1 || input.Amount > maxOrderMoneyCents || !validPaymentCurrency(input.Currency) ||
		!validLowerHex(input.PayloadSHA256, 64) || now.Unix() < 0 {
		return Order{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Order{}, fmt.Errorf("begin complete payment webhook: %w", err)
	}
	defer tx.Rollback()
	method, err := getPayment(ctx, tx, input.PaymentID)
	if err != nil {
		return Order{}, err
	}
	// Disabling a method stops new checkouts, but a provider must still be able to
	// settle an attempt that was created while the method was enabled.
	if method.Provider != input.Provider {
		return Order{}, ErrPaymentMismatch
	}
	order, err := scanOrder(tx.QueryRowContext(ctx, orderSelect+` WHERE o.trade_no = ?`, input.TradeNo))
	if err != nil {
		return Order{}, err
	}
	if input.Currency != "CNY" {
		return Order{}, ErrPaymentMismatch
	}
	var expectedAmount int64
	if err := tx.QueryRowContext(ctx, `
		SELECT expected_amount FROM payment_checkout_attempts WHERE order_id = ? AND payment_id = ? AND currency = ?
	`, order.ID, input.PaymentID, input.Currency).Scan(&expectedAmount); errors.Is(err, sql.ErrNoRows) {
		return Order{}, ErrPaymentMismatch
	} else if err != nil {
		return Order{}, fmt.Errorf("validate payment checkout attempt: %w", err)
	}
	if expectedAmount != input.Amount || input.Amount < order.TotalAmount {
		return Order{}, ErrPaymentMismatch
	}
	existing, found, err := getPaymentReceipt(ctx, tx, input.PaymentID, input.ExternalID)
	if err != nil {
		return Order{}, err
	}
	if found {
		if existing.orderID != order.ID || existing.provider != input.Provider || existing.tradeNo != input.TradeNo ||
			existing.amount != input.Amount || existing.currency != input.Currency {
			return Order{}, ErrPaymentMismatch
		}
		// Providers may append retry metadata or reorder signed form fields. The
		// first payload digest remains an audit record while the verified business
		// binding above provides idempotency for legitimate retries.
		return order, nil
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO payment_webhook_receipts (
			payment_id, order_id, provider, external_id, trade_no, amount, currency, payload_sha256, received_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, input.PaymentID, order.ID, input.Provider, input.ExternalID, input.TradeNo, input.Amount, input.Currency,
		input.PayloadSHA256, now.Unix()); err != nil {
		return Order{}, fmt.Errorf("record payment webhook: %w", err)
	}
	if order.Status == OrderStatusCompleted {
		if err := tx.Commit(); err != nil {
			return Order{}, fmt.Errorf("commit duplicate completed payment webhook: %w", err)
		}
		return order, nil
	}
	handlingAmount := input.Amount - order.TotalAmount
	result, err := tx.ExecContext(ctx, `
		UPDATE orders SET payment_id = ?, handling_amount = ?, updated_at = ?
		WHERE id = ? AND status IN (0, 1)
	`, input.PaymentID, nullablePositiveMoney(handlingAmount), now.Unix(), order.ID)
	if err != nil {
		return Order{}, fmt.Errorf("bind settled order payment: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return Order{}, fmt.Errorf("count settled order payment binding: %w", err)
	}
	if updated != 1 {
		return Order{}, ErrOrderState
	}
	order.PaymentID = &input.PaymentID
	order.HandlingAmount = nil
	if handlingAmount > 0 {
		order.HandlingAmount = &handlingAmount
	}
	order.UpdatedAt = now.UTC()
	if err := completeOrderTx(ctx, tx, &order, input.ExternalID, now); err != nil {
		return Order{}, err
	}
	if err := tx.Commit(); err != nil {
		return Order{}, fmt.Errorf("commit complete payment webhook: %w", err)
	}
	return order, nil
}

type paymentReceipt struct {
	orderID       int64
	provider      PaymentProvider
	tradeNo       string
	amount        int64
	currency      string
	payloadSHA256 string
}

func getPaymentReceipt(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, paymentID int64, externalID string) (paymentReceipt, bool, error) {
	var receipt paymentReceipt
	err := database.QueryRowContext(ctx, `
		SELECT order_id, provider, trade_no, amount, currency, payload_sha256
		FROM payment_webhook_receipts WHERE payment_id = ? AND external_id = ?
	`, paymentID, externalID).Scan(&receipt.orderID, &receipt.provider, &receipt.tradeNo, &receipt.amount, &receipt.currency, &receipt.payloadSHA256)
	if errors.Is(err, sql.ErrNoRows) {
		return paymentReceipt{}, false, nil
	}
	if err != nil {
		return paymentReceipt{}, false, fmt.Errorf("read payment webhook receipt: %w", err)
	}
	return receipt, true, nil
}

func getPaymentCheckout(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, attemptID int64) (PaymentCheckoutAttempt, error) {
	return scanPaymentCheckout(database.QueryRowContext(ctx, paymentCheckoutSelect+` WHERE id = ?`, attemptID))
}

func getPaymentCheckoutByOrderAndMethod(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, orderID, paymentID int64) (PaymentCheckoutAttempt, error) {
	return scanPaymentCheckout(database.QueryRowContext(ctx, paymentCheckoutSelect+` WHERE order_id = ? AND payment_id = ?`, orderID, paymentID))
}

func scanPaymentCheckout(row rowScanner) (PaymentCheckoutAttempt, error) {
	var attempt PaymentCheckoutAttempt
	var externalID, responseData, errorCode sql.NullString
	var responseType sql.NullInt64
	var createdAt, updatedAt int64
	if err := row.Scan(&attempt.ID, &attempt.OrderID, &attempt.PaymentID, &attempt.IdempotencyKey,
		&attempt.ExpectedAmount, &attempt.Currency, &attempt.Status, &externalID, &responseType,
		&responseData, &errorCode, &createdAt, &updatedAt); errors.Is(err, sql.ErrNoRows) {
		return PaymentCheckoutAttempt{}, ErrNotFound
	} else if err != nil {
		return PaymentCheckoutAttempt{}, fmt.Errorf("scan payment checkout: %w", err)
	}
	attempt.ExternalID = externalID.String
	if responseType.Valid {
		value := int(responseType.Int64)
		attempt.ResponseType = &value
	}
	attempt.ResponseData = responseData.String
	attempt.ErrorCode = errorCode.String
	attempt.CreatedAt = time.Unix(createdAt, 0).UTC()
	attempt.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return attempt, nil
}

func newPaymentIdempotencyKey() (string, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate payment idempotency key: %w", err)
	}
	return hex.EncodeToString(random), nil
}

func validPaymentIdempotencyKey(value string) bool { return validLowerHex(value, 64) }

func validPaymentResponseData(value string) bool {
	return utf8.ValidString(value) && len(value) >= 1 && len(value) <= 4096 && strings.IndexFunc(value, unicode.IsControl) < 0
}

func validOptionalExternalPaymentID(value string) bool {
	return value == "" || validExternalPaymentID(value)
}

func validExternalPaymentID(value string) bool {
	return utf8.ValidString(value) && len(value) >= 1 && len(value) <= 255 && strings.IndexFunc(value, unicode.IsControl) < 0
}

func validPaymentCurrency(value string) bool {
	if len(value) != 3 {
		return false
	}
	for _, character := range value {
		if character < 'A' || character > 'Z' {
			return false
		}
	}
	return true
}

func validLowerHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			if character < 'a' || character > 'f' {
				return false
			}
		}
	}
	return true
}

func nullablePositiveMoney(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}

const paymentCheckoutSelect = `SELECT id, order_id, payment_id, idempotency_key, expected_amount, currency,
    status, external_id, response_type, response_data, error_code, created_at, updated_at FROM payment_checkout_attempts`
