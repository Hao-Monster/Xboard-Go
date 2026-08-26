package legacymigration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strings"

	"github.com/Hao-Monster/Xboard-Go/internal/payment"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const (
	maxLegacyPaymentRows      = 10_000
	maxLegacyPaymentDataBytes = int64(64 << 20)
)

type PaymentRecord struct {
	ID                     int64
	UUID                   string
	Provider               store.PaymentProvider
	Name                   string
	Icon                   string
	Config                 map[string]string
	NotifyDomain           string
	HandlingFeeFixed       int64
	HandlingFeeBasisPoints int64
	Enabled                bool
	SortPosition           int
	CreatedAt              int64
	UpdatedAt              int64
}

type PaymentsSnapshot struct {
	Path             string
	Size             int64
	SHA256           string
	Payments         []PaymentRecord
	PaymentsChecksum string
}

func ReadPaymentsSnapshot(ctx context.Context, sourcePath string) (PaymentsSnapshot, error) {
	payments := []PaymentRecord{}
	identity, err := readLegacySnapshot(ctx, sourcePath, func(database *sql.DB) error {
		if err := requireRealTable(ctx, database, "v2_payment", []string{
			"id", "uuid", "payment", "name", "icon", "config", "notify_domain", "handling_fee_fixed",
			"handling_fee_percent", "enable", "sort", "created_at", "updated_at",
		}); err != nil {
			return err
		}
		if err := validateLegacyQueryBudget(ctx, database, `
			SELECT COUNT(*), COALESCE(SUM(
				length(CAST(uuid AS BLOB)) + length(CAST(payment AS BLOB)) + length(CAST(name AS BLOB)) +
				COALESCE(length(CAST(icon AS BLOB)), 0) + length(CAST(config AS BLOB)) +
				COALESCE(length(CAST(notify_domain AS BLOB)), 0)
			), 0) FROM v2_payment
		`, maxLegacyPaymentRows, maxLegacyPaymentDataBytes, "legacy payments"); err != nil {
			return err
		}
		var readErr error
		payments, readErr = readLegacyPayments(ctx, database)
		return readErr
	})
	if err != nil {
		return PaymentsSnapshot{}, err
	}
	return PaymentsSnapshot{
		Path: identity.Path, Size: identity.Size, SHA256: identity.SHA256,
		Payments: payments, PaymentsChecksum: legacyPaymentsPlaintextChecksum(payments),
	}, nil
}

func readLegacyPayments(ctx context.Context, database *sql.DB) ([]PaymentRecord, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id, uuid, payment, name, icon, config, notify_domain, handling_fee_fixed,
		       CAST(handling_fee_percent AS TEXT), enable, sort,
		       `+legacyUnixExpression("created_at")+`, `+legacyUnixExpression("updated_at")+`
		FROM v2_payment
		ORDER BY sort ASC, id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("read legacy payments: %w", err)
	}
	defer rows.Close()
	payments := make([]PaymentRecord, 0)
	var bytesRead int64
	for rows.Next() {
		if len(payments) >= maxLegacyPaymentRows {
			return nil, fmt.Errorf("legacy payments exceed the %d-row migration limit", maxLegacyPaymentRows)
		}
		var item PaymentRecord
		var icon, notifyDomain, percent sql.NullString
		var fixed sql.NullInt64
		var enabled int64
		var ignoredSort sql.NullInt64
		var rawConfig string
		if err := rows.Scan(&item.ID, &item.UUID, &item.Provider, &item.Name, &icon, &rawConfig, &notifyDomain,
			&fixed, &percent, &enabled, &ignoredSort, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan legacy payment: %w", err)
		}
		item.Name = strings.TrimSpace(item.Name)
		item.Icon = strings.TrimSpace(icon.String)
		item.NotifyDomain = strings.TrimRight(strings.TrimSpace(notifyDomain.String), "/")
		if fixed.Valid {
			item.HandlingFeeFixed = fixed.Int64
		}
		item.HandlingFeeBasisPoints, err = legacyPaymentBasisPoints(percent)
		if err != nil {
			return nil, fmt.Errorf("legacy payment id %d percentage fee: %w", item.ID, err)
		}
		if enabled != 0 && enabled != 1 {
			return nil, fmt.Errorf("legacy payment id %d has an ambiguous enable value", item.ID)
		}
		item.Enabled = enabled == 1
		item.SortPosition = len(payments) + 1
		item.Config, err = decodeLegacyPaymentConfig(item.Provider, rawConfig)
		if err != nil {
			return nil, fmt.Errorf("legacy payment id %d configuration: %w", item.ID, err)
		}
		bytesRead += int64(len(item.UUID) + len(item.Provider) + len(item.Name) + len(item.Icon) + len(rawConfig) + len(item.NotifyDomain))
		if bytesRead > maxLegacyPaymentDataBytes {
			return nil, errors.New("legacy payments exceed the migration data limit")
		}
		payments = append(payments, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate legacy payments: %w", err)
	}
	return payments, nil
}

func decodeLegacyPaymentConfig(provider store.PaymentProvider, encoded string) (map[string]string, error) {
	decoder := json.NewDecoder(strings.NewReader(encoded))
	decoder.UseNumber()
	var raw map[string]any
	if err := decoder.Decode(&raw); err != nil || raw == nil {
		return nil, payment.ErrInvalidConfig
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, payment.ErrInvalidConfig
	}
	values := make(map[string]string, len(raw))
	for key, value := range raw {
		switch typed := value.(type) {
		case string:
			values[key] = typed
		case json.Number:
			values[key] = typed.String()
		case bool:
			values[key] = fmt.Sprintf("%t", typed)
		case nil:
			values[key] = ""
		default:
			return nil, payment.ErrInvalidConfig
		}
	}
	return payment.MergeConfig(provider, values, nil, nil, true)
}

func legacyPaymentBasisPoints(value sql.NullString) (int64, error) {
	if !value.Valid || strings.TrimSpace(value.String) == "" {
		return 0, nil
	}
	percentage := new(big.Rat)
	if _, ok := percentage.SetString(strings.TrimSpace(value.String)); !ok || percentage.Sign() < 0 {
		return 0, errors.New("must be an exact decimal from 0 to 100 with at most two fractional digits")
	}
	basisPoints := new(big.Rat).Mul(percentage, big.NewRat(100, 1))
	if !basisPoints.IsInt() || !basisPoints.Num().IsInt64() {
		return 0, errors.New("must be an exact decimal from 0 to 100 with at most two fractional digits")
	}
	result := basisPoints.Num().Int64()
	if result > 10_000 {
		return 0, errors.New("must be between 0 and 100")
	}
	return result, nil
}

func legacyPaymentsPlaintextChecksum(payments []PaymentRecord) string {
	type canonicalPayment struct {
		ID                     int64                 `json:"id"`
		UUID                   string                `json:"uuid"`
		Provider               store.PaymentProvider `json:"provider"`
		Name                   string                `json:"name"`
		Icon                   string                `json:"icon"`
		Config                 map[string]string     `json:"config"`
		NotifyDomain           string                `json:"notify_domain"`
		HandlingFeeFixed       int64                 `json:"handling_fee_fixed"`
		HandlingFeeBasisPoints int64                 `json:"handling_fee_basis_points"`
		Enabled                bool                  `json:"enabled"`
		SortPosition           int                   `json:"sort_position"`
		CreatedAt              int64                 `json:"created_at"`
		UpdatedAt              int64                 `json:"updated_at"`
	}
	canonical := make([]canonicalPayment, len(payments))
	for index, item := range payments {
		canonical[index] = canonicalPayment{
			ID: item.ID, UUID: item.UUID, Provider: item.Provider, Name: item.Name, Icon: item.Icon,
			Config: item.Config, NotifyDomain: item.NotifyDomain, HandlingFeeFixed: item.HandlingFeeFixed,
			HandlingFeeBasisPoints: item.HandlingFeeBasisPoints, Enabled: item.Enabled,
			SortPosition: item.SortPosition, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
		}
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		panic(fmt.Sprintf("marshal canonical legacy payments: %v", err))
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}
