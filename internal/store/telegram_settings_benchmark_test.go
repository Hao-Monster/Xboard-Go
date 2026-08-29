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
