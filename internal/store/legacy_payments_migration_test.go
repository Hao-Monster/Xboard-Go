package store

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestImportLegacyPaymentsPreservesIDsOrderingCiphertextAndIsIdempotent(t *testing.T) {
	database := newTestStore(t)
	input := validLegacyPaymentsImport()
	now := time.Unix(1_800_000_000, 0).UTC()
	report, err := database.ImportLegacyPayments(context.Background(), input, now)
	if err != nil {
		t.Fatalf("ImportLegacyPayments() error = %v", err)
	}
	if report.AlreadyApplied || report.Payments.SourceRows != 2 || report.Payments.TargetRows != 2 ||
		report.Payments.SourceChecksum != report.Payments.TargetChecksum || report.PlaintextSourceChecksum != input.PlaintextSourceChecksum {
		t.Fatalf("report = %#v", report)
	}
	page, err := database.ListPayments(context.Background(), PaymentFilter{Page: 1, PageSize: 20})
	if err != nil || len(page.Items) != 2 || page.Items[0].ID != 7 || page.Items[1].ID != 9 ||
		page.Items[0].SortPosition != 1 || page.Items[1].SortPosition != 2 ||
		!bytes.Equal(page.Items[0].ConfigCiphertext, input.Payments[0].ConfigCiphertext) {
		t.Fatalf("imported payments = (%#v, %v)", page.Items, err)
	}
	repeated, err := database.ImportLegacyPayments(context.Background(), input, now.Add(time.Hour))
	if err != nil || !repeated.AlreadyApplied || repeated.AppliedAt != report.AppliedAt {
		t.Fatalf("repeated import = (%#v, %v)", repeated, err)
	}
}

func TestImportLegacyPaymentsRejectsInvalidOrNonEmptyTargetAtomically(t *testing.T) {
	t.Run("duplicate UUID", func(t *testing.T) {
		database := newTestStore(t)
		input := validLegacyPaymentsImport()
		input.Payments[1].UUID = input.Payments[0].UUID
		input.PaymentsChecksum = LegacyPaymentsChecksum(input.Payments)
		if _, err := database.ImportLegacyPayments(context.Background(), input, time.Now()); !errors.Is(err, ErrConflict) {
			t.Fatalf("ImportLegacyPayments() error = %v, want ErrConflict", err)
		}
		var payments, runs int
		_ = database.db.QueryRow(`SELECT COUNT(*) FROM payments`).Scan(&payments)
		_ = database.db.QueryRow(`SELECT COUNT(*) FROM legacy_migration_runs WHERE slice = ?`, LegacyPaymentsSlice).Scan(&runs)
		if payments != 0 || runs != 0 {
			t.Fatalf("partial writes payments=%d runs=%d", payments, runs)
		}
	})

	t.Run("non-empty target", func(t *testing.T) {
		database := newTestStore(t)
		if _, err := database.CreatePayment(context.Background(), SavePaymentInput{
			Provider: PaymentProviderEPay, Name: "existing", ConfigCiphertext: []byte("existing-ciphertext"),
		}, time.Now()); err != nil {
			t.Fatal(err)
		}
		if _, err := database.ImportLegacyPayments(context.Background(), validLegacyPaymentsImport(), time.Now()); !errors.Is(err, ErrConflict) {
			t.Fatalf("ImportLegacyPayments() error = %v, want ErrConflict", err)
		}
	})
}

func validLegacyPaymentsImport() LegacyPaymentsImport {
	payments := []LegacyPayment{
		{ID: 7, UUID: "coin0007", Provider: PaymentProviderCoinPayments, Name: "CoinPayments", Icon: "https://cdn.example.test/coin.svg",
			ConfigCiphertext: []byte("encrypted-coinpayments-config"), NotifyDomain: "https://notify.example.test",
			HandlingFeeFixed: 123, HandlingFeeBasisPoints: 250, Enabled: true, SortPosition: 1,
			CreatedAt: 1_700_000_000, UpdatedAt: 1_700_000_100},
		{ID: 9, UUID: "epay0009", Provider: PaymentProviderEPay, Name: "易支付",
			ConfigCiphertext: []byte("encrypted-epay-config"), SortPosition: 2,
			CreatedAt: 1_700_000_200, UpdatedAt: 1_700_000_300},
	}
	return LegacyPaymentsImport{
		Slice: LegacyPaymentsSlice, SourceSHA256: strings.Repeat("a", 64), SourceSize: 4096,
		Payments: payments, PaymentsChecksum: LegacyPaymentsChecksum(payments),
		PlaintextSourceChecksum: strings.Repeat("b", 64), RollbackBackupPath: "/tmp/payment-rollback.tar.gz",
		RollbackBackupSHA256: strings.Repeat("c", 64),
	}
}
