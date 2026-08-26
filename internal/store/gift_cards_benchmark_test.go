package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

const benchmarkGiftCardRows = 100_000

func BenchmarkCheckGiftCardAtHundredThousandRows(b *testing.B) {
	database, userID, now := benchmarkGiftCardStore(b)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		preview, err := database.CheckGiftCard(context.Background(), userID, "GCBENCH00000000099999", now)
		if err != nil {
			b.Fatal(err)
		}
		if preview.Rewards.Balance != 500 {
			b.Fatalf("CheckGiftCard() balance = %d", preview.Rewards.Balance)
		}
	}
}

func BenchmarkListGiftCardCodesAtHundredThousandRows(b *testing.B) {
	database, _, _ := benchmarkGiftCardStore(b)
	filter := GiftCardCodeFilter{Page: 1, PageSize: 20}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		page, err := database.ListGiftCardCodes(context.Background(), filter)
		if err != nil {
			b.Fatal(err)
		}
		if page.Total != benchmarkGiftCardRows || len(page.Items) != filter.PageSize {
			b.Fatalf("ListGiftCardCodes() total/items = %d/%d", page.Total, len(page.Items))
		}
	}
}

func benchmarkGiftCardStore(b *testing.B) (*Store, int64, time.Time) {
	b.Helper()
	database := newTestStore(b)
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	admin, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "gift-benchmark-admin@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		b.Fatal(err)
	}
	user, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "gift-benchmark-user@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		b.Fatal(err)
	}
	template, err := database.CreateGiftCardTemplate(ctx, SaveGiftCardTemplateInput{
		Name: "Benchmark reward", Type: GiftCardTypeGeneral, Status: true,
		Rewards: GiftCardReward{Balance: 500}, Limits: GiftCardLimits{MaxUsePerUser: 1},
	}, admin.ID, now)
	if err != nil {
		b.Fatal(err)
	}
	tx, err := database.db.BeginTx(ctx, nil)
	if err != nil {
		b.Fatal(err)
	}
	statement, err := tx.PrepareContext(ctx, `
		INSERT INTO gift_card_codes (
			template_id, code, batch_no, status, usage_count, max_usage,
			metadata_json, created_at, updated_at
		) VALUES (?, ?, 'GCBENCHMARKBATCH01', 0, 0, 1, '{}', ?, ?)
	`)
	if err != nil {
		_ = tx.Rollback()
		b.Fatal(err)
	}
	for index := 0; index < benchmarkGiftCardRows; index++ {
		if _, err := statement.ExecContext(ctx, template.ID, fmt.Sprintf("GCBENCH%014d", index), now.Unix(), now.Unix()); err != nil {
			_ = statement.Close()
			_ = tx.Rollback()
			b.Fatal(err)
		}
	}
	if err := statement.Close(); err != nil {
		_ = tx.Rollback()
		b.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	return database, user.ID, now
}
