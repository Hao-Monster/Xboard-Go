package store

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"
	"time"
)

var (
	benchmarkPaymentAmount      int64 = 100_000
	benchmarkPaymentFixed       int64 = 123
	benchmarkPaymentBasisPoints int64 = 250
	benchmarkPaymentFee         int64
)

func BenchmarkPaymentHandlingFee(b *testing.B) {
	b.ReportAllocs()
	for index := 0; index < b.N; index++ {
		fee, err := PaymentHandlingFee(benchmarkPaymentAmount, benchmarkPaymentFixed, benchmarkPaymentBasisPoints)
		if err != nil {
			b.Fatal(err)
		}
		benchmarkPaymentFee = fee
	}
}

func BenchmarkListPaymentsTenThousand(b *testing.B) {
	database := newTestStore(b)
	ctx := context.Background()
	tx, err := database.db.BeginTx(ctx, nil)
	if err != nil {
		b.Fatal(err)
	}
	statement, err := tx.PrepareContext(ctx, `
		INSERT INTO payments (
			uuid, provider, name, config_ciphertext, handling_fee_fixed,
			handling_fee_basis_points, enabled, sort_position, revision, created_at, updated_at
		) VALUES (?, 'EPay', ?, ?, 123, 250, 1, ?, 1, 1700000000, 1700000000)
	`)
	if err != nil {
		b.Fatal(err)
	}
	for index := 1; index <= 10_000; index++ {
		if _, err := statement.ExecContext(ctx, fmt.Sprintf("PAY%05d", index), fmt.Sprintf("Payment %05d", index), []byte("encrypted-config"), index); err != nil {
			b.Fatal(err)
		}
	}
	if err := statement.Close(); err != nil {
		b.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for index := 0; index < b.N; index++ {
		page, err := database.ListPayments(ctx, PaymentFilter{Page: 1, PageSize: 200})
		if err != nil {
			b.Fatal(err)
		}
		if page.Total != 10_000 || len(page.Items) != 200 {
			b.Fatalf("unexpected page: total=%d items=%d", page.Total, len(page.Items))
		}
	}
}

func BenchmarkDuplicatePaymentWebhook(b *testing.B) {
	database := newTestStore(b)
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	plan, userID := createOrderFixture(b, database, now, PlanPrices{"monthly": 100_000}, nil)
	method, err := database.CreatePayment(ctx, SavePaymentInput{
		Provider: PaymentProviderCoinPayments, Name: "CoinPayments", ConfigCiphertext: []byte("encrypted-config"), Enabled: true,
	}, now)
	if err != nil {
		b.Fatal(err)
	}
	order, err := database.CreateOrder(ctx, CreateOrderInput{UserID: userID, PlanID: plan.ID, Period: "monthly"}, now)
	if err != nil {
		b.Fatal(err)
	}
	started, err := database.StartPaymentCheckout(ctx, StartPaymentCheckoutInput{UserID: userID, TradeNo: order.TradeNo, PaymentID: method.ID}, now)
	if err != nil {
		b.Fatal(err)
	}
	payload := sha256.Sum256([]byte("verified benchmark webhook"))
	input := CompletePaymentWebhookInput{
		PaymentID: method.ID, Provider: method.Provider, ExternalID: "benchmark-payment",
		TradeNo: order.TradeNo, Amount: started.Attempt.ExpectedAmount, Currency: "CNY", PayloadSHA256: fmt.Sprintf("%x", payload),
	}
	if _, err := database.CompletePaymentWebhook(ctx, input, now); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for index := 0; index < b.N; index++ {
		if _, err := database.CompletePaymentWebhook(ctx, input, now.Add(time.Duration(index+1)*time.Second)); err != nil {
			b.Fatal(err)
		}
	}
}
