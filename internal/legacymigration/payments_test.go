package legacymigration

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
	_ "modernc.org/sqlite"
)

func TestReadPaymentsSnapshotNormalizesConfigFeesAndOrdering(t *testing.T) {
	path := createLegacyPaymentsSnapshot(t)
	snapshot, err := ReadPaymentsSnapshot(context.Background(), path)
	if err != nil {
		t.Fatalf("ReadPaymentsSnapshot() error = %v", err)
	}
	if snapshot.Path != path || snapshot.Size < 512 || len(snapshot.SHA256) != 64 || len(snapshot.PaymentsChecksum) != 64 || len(snapshot.Payments) != 2 {
		t.Fatalf("snapshot identity = %#v", snapshot)
	}
	first, second := snapshot.Payments[0], snapshot.Payments[1]
	if first.ID != 7 || first.Provider != store.PaymentProviderCoinPayments || first.SortPosition != 1 ||
		first.HandlingFeeFixed != 123 || first.HandlingFeeBasisPoints != 250 || !first.Enabled ||
		first.Config["coinpayments_currency"] != "CNY" || first.Config["coinpayments_ipn_secret"] != "secret-one" {
		t.Fatalf("first payment = %#v", first)
	}
	if second.ID != 9 || second.Provider != store.PaymentProviderEPay || second.SortPosition != 2 ||
		second.HandlingFeeBasisPoints != 0 || second.Enabled || second.Config["url"] != "https://epay.example.test" {
		t.Fatalf("second payment = %#v", second)
	}
}

func TestReadPaymentsSnapshotRejectsUnsafeConfigAndUnrepresentableFee(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		statement string
		contains  string
	}{
		{name: "unsafe endpoint", statement: `UPDATE v2_payment SET config = '{"url":"http://epay.example.test","pid":"1001","key":"secret-two","type":"alipay"}' WHERE id = 9`, contains: "configuration"},
		{name: "unknown provider", statement: `UPDATE v2_payment SET payment = 'Unknown' WHERE id = 9`, contains: "configuration"},
		{name: "fractional basis point", statement: `UPDATE v2_payment SET handling_fee_percent = '1.001' WHERE id = 9`, contains: "at most two"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			path := createLegacyPaymentsSnapshot(t)
			database, err := sql.Open("sqlite", "file:"+path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(scenario.statement); err != nil {
				_ = database.Close()
				t.Fatal(err)
			}
			_ = database.Close()
			if _, err := ReadPaymentsSnapshot(context.Background(), path); err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(scenario.contains)) {
				t.Fatalf("ReadPaymentsSnapshot() error = %v, want %q", err, scenario.contains)
			}
		})
	}
}

func createLegacyPaymentsSnapshot(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy-payments.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TABLE v2_payment (
			id INTEGER PRIMARY KEY, uuid TEXT NOT NULL, payment TEXT NOT NULL, name TEXT NOT NULL, icon TEXT,
			config TEXT NOT NULL, notify_domain TEXT, handling_fee_fixed INTEGER, handling_fee_percent NUMERIC,
			enable INTEGER NOT NULL, sort INTEGER, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
		);
		INSERT INTO v2_payment VALUES (
			9, 'epay0009', 'EPay', '易支付', NULL,
			'{"url":"https://epay.example.test/","pid":1001,"key":"secret-two","type":"alipay"}',
			NULL, NULL, NULL, 0, 9, 1700000000, 1700000100
		);
		INSERT INTO v2_payment VALUES (
			7, 'coin0007', 'CoinPayments', 'CoinPayments', 'https://cdn.example.test/coin.svg',
			'{"coinpayments_merchant_id":"merchant-one","coinpayments_ipn_secret":"secret-one","coinpayments_currency":"cny"}',
			'https://notify.example.test/', 123, 2.5, 1, NULL, 1700000200, 1700000300
		);
	`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
