package store

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	commissionWithdrawLimitScale   = int64(100)
	maxCommissionWithdrawLimit     = CurrencyAmount(9_000_000_000_000_000)
	defaultCommissionWithdrawLimit = CurrencyAmount(10_000)
)

// CurrencyAmount stores exact hundredths of a currency unit while preserving a
// JSON number at the API boundary. The legacy withdrawal balance is also stored
// in hundredths, so this avoids floating-point decisions at that boundary.
type CurrencyAmount int64

func ParseCurrencyAmount(value string) (CurrencyAmount, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "-") || strings.HasPrefix(value, "+") {
		return 0, ErrInvalidInput
	}
	integer, fraction, found := strings.Cut(value, ".")
	if integer == "" || strings.Contains(fraction, ".") {
		return 0, ErrInvalidInput
	}
	for _, digit := range integer {
		if digit < '0' || digit > '9' {
			return 0, ErrInvalidInput
		}
	}
	if found {
		if fraction == "" {
			return 0, ErrInvalidInput
		}
		for _, digit := range fraction {
			if digit < '0' || digit > '9' {
				return 0, ErrInvalidInput
			}
		}
		if len(fraction) > 2 && strings.Trim(fraction[2:], "0") != "" {
			return 0, ErrInvalidInput
		}
		if len(fraction) > 2 {
			fraction = fraction[:2]
		}
	}
	if len(integer) > 14 {
		return 0, ErrInvalidInput
	}
	major, err := strconv.ParseInt(integer, 10, 64)
	if err != nil || major > int64(maxCommissionWithdrawLimit)/commissionWithdrawLimitScale {
		return 0, ErrInvalidInput
	}
	minor := int64(0)
	if fraction != "" {
		if len(fraction) == 1 {
			fraction += "0"
		}
		minor, err = strconv.ParseInt(fraction, 10, 64)
		if err != nil {
			return 0, ErrInvalidInput
		}
	}
	amount := CurrencyAmount(major*commissionWithdrawLimitScale + minor)
	if !validCommissionWithdrawLimit(amount) {
		return 0, ErrInvalidInput
	}
	return amount, nil
}

func (amount CurrencyAmount) String() string {
	major := int64(amount) / commissionWithdrawLimitScale
	minor := int64(amount) % commissionWithdrawLimitScale
	switch {
	case minor == 0:
		return strconv.FormatInt(major, 10)
	case minor%10 == 0:
		return fmt.Sprintf("%d.%d", major, minor/10)
	default:
		return fmt.Sprintf("%d.%02d", major, minor)
	}
}

func (amount CurrencyAmount) MarshalJSON() ([]byte, error) {
	if !validCommissionWithdrawLimit(amount) {
		return nil, ErrInvalidInput
	}
	return []byte(amount.String()), nil
}

func (amount *CurrencyAmount) UnmarshalJSON(encoded []byte) error {
	if amount == nil {
		return ErrInvalidInput
	}
	raw := strings.TrimSpace(string(encoded))
	if strings.HasPrefix(raw, `"`) {
		var value string
		if err := json.Unmarshal(encoded, &value); err != nil {
			return ErrInvalidInput
		}
		raw = value
	}
	parsed, err := ParseCurrencyAmount(raw)
	if err != nil {
		return err
	}
	*amount = parsed
	return nil
}

func (amount CurrencyAmount) Value() (driver.Value, error) {
	if !validCommissionWithdrawLimit(amount) {
		return nil, ErrInvalidInput
	}
	return int64(amount), nil
}

func (amount *CurrencyAmount) Scan(value any) error {
	if amount == nil {
		return ErrInvalidInput
	}
	var cents int64
	switch typed := value.(type) {
	case int64:
		cents = typed
	case int:
		cents = int64(typed)
	case []byte:
		parsed, err := strconv.ParseInt(string(typed), 10, 64)
		if err != nil {
			return fmt.Errorf("scan currency amount: %w", err)
		}
		cents = parsed
	case string:
		parsed, err := strconv.ParseInt(typed, 10, 64)
		if err != nil {
			return fmt.Errorf("scan currency amount: %w", err)
		}
		cents = parsed
	default:
		return errors.New("scan currency amount: unsupported database value")
	}
	parsed := CurrencyAmount(cents)
	if !validCommissionWithdrawLimit(parsed) {
		return ErrInvalidInput
	}
	*amount = parsed
	return nil
}

func validCommissionWithdrawLimit(amount CurrencyAmount) bool {
	return amount >= 0 && amount <= maxCommissionWithdrawLimit
}
