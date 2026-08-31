package store

import (
	"testing"
	"time"
)

func BenchmarkTelegramUserAvailable100K(b *testing.B) {
	database := openBenchmarkStore(b, "telegram-users.db")
	ctx := b.Context()
	now := time.Date(2026, 8, 29, 16, 0, 0, 0, time.UTC)
	if _, err := database.db.ExecContext(ctx, `
		WITH RECURSIVE sequence(value) AS (
			SELECT 1 UNION ALL SELECT value + 1 FROM sequence WHERE value < 100000
		)
		INSERT INTO users (
			email,password_hash,uuid,subscription_token,telegram_id,transfer_enable,expired_at,created_at,updated_at
		)
		SELECT printf('telegram-%06d@example.test',value),'hash',
		       printf('%08x-0000-4000-8000-%012x',value,value),printf('%032x',value),
		       1000000 + value,1024,?,?,?
		FROM sequence
	`, now.Add(24*time.Hour).Unix(), now.Unix(), now.Unix()); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for _, benchmark := range []struct {
		name       string
		telegramID int64
		want       bool
	}{
		{name: "indexed-hit", telegramID: 1_100_000, want: true},
		{name: "indexed-miss", telegramID: 9_999_999, want: false},
	} {
		b.Run(benchmark.name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				available, err := database.TelegramUserAvailable(ctx, benchmark.telegramID, now)
				if err != nil || available != benchmark.want {
					b.Fatalf("TelegramUserAvailable()=(%t,%v), want %t", available, err, benchmark.want)
				}
			}
		})
	}
}

func BenchmarkTelegramTrafficLookup100K(b *testing.B) {
	database := openBenchmarkStore(b, "telegram-command-users.db")
	ctx := b.Context()
	now := time.Date(2026, 8, 31, 16, 0, 0, 0, time.UTC)
	if _, err := database.db.ExecContext(ctx, `
		WITH RECURSIVE sequence(value) AS (
			SELECT 1 UNION ALL SELECT value + 1 FROM sequence WHERE value < 100000
		)
		INSERT INTO users (
			email,password_hash,uuid,subscription_token,telegram_id,traffic_u,traffic_d,transfer_enable,created_at,updated_at
		)
		SELECT printf('telegram-command-%06d@example.test',value),'hash',
		       printf('%08x-0000-4000-8000-%012x',value,value),printf('%032x',value),
		       2000000 + value,1073741824,2147483648,10737418240,?,?
		FROM sequence
	`, now.Unix(), now.Unix()); err != nil {
		b.Fatal(err)
	}
	tx, err := database.db.BeginTx(ctx, nil)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = tx.Rollback() })
	b.ReportMetric(100_000, "users")
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		response, err := telegramTrafficResponse(ctx, tx, 2_100_000)
		if err != nil || response == "" {
			b.Fatalf("telegramTrafficResponse()=(%q,%v)", response, err)
		}
	}
}

func BenchmarkTelegramAdminNotificationFanout100K(b *testing.B) {
	database := openBenchmarkStore(b, "telegram-admin-notify-users.db")
	ctx := b.Context()
	now := time.Date(2026, 8, 31, 16, 30, 0, 0, time.UTC)
	enableTelegramNotificationDelivery(b, database)
	if _, err := database.db.ExecContext(ctx, `
		WITH RECURSIVE sequence(value) AS (
			SELECT 1 UNION ALL SELECT value + 1 FROM sequence WHERE value < 100000
		)
		INSERT INTO users (
			email,password_hash,uuid,subscription_token,is_admin,is_staff,telegram_id,created_at,updated_at
		)
		SELECT printf('telegram-notify-%06d@example.test',value),'hash',
		       printf('%08x-0000-4000-8000-%012x',value,value),printf('%032x',value),
		       CASE WHEN value % 20000 = 0 THEN 1 ELSE 0 END,
		       CASE WHEN value % 10000 = 0 AND value % 20000 <> 0 THEN 1 ELSE 0 END,
		       CASE WHEN value % 10000 = 0 THEN 3000000 + value ELSE NULL END,?,?
		FROM sequence
	`, now.Unix(), now.Unix()); err != nil {
		b.Fatal(err)
	}
	tx, err := database.db.BeginTx(ctx, nil)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = tx.Rollback() })
	b.ReportMetric(100_000, "users")
	b.ReportMetric(10, "recipients/op")
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := enqueueTelegramAdminNotificationTx(ctx, tx, telegramNotificationTicket, 1, "bounded notification", now); err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		if _, err := tx.ExecContext(ctx, `DELETE FROM telegram_message_outbox WHERE source_kind='ticket' AND source_id=1`); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
	}
}
