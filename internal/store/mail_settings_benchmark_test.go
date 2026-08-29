package store

import (
	"fmt"
	"testing"
	"time"
)

func BenchmarkScheduleSubscriptionReminders100kUsers(b *testing.B) {
	database := newTestStore(b)
	ctx := b.Context()
	now := time.Date(2026, 8, 29, 3, 30, 0, 0, time.UTC)
	administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "benchmark-reminder-admin@example.test", PasswordHash: "hash", IsAdmin: true}, now)
	if err != nil {
		b.Fatal(err)
	}
	settings, _ := database.GetMailSettings(ctx)
	if _, err := database.UpdateMailSettings(ctx, administrator.ID, settings.Revision, SaveMailSettingsInput{
		SMTPEnabled: true, SMTPHost: "mailpit", SMTPPort: 1025, SMTPEncryption: "none",
		SMTPFromAddress: "support@example.test", RemindMailEnabled: true,
	}, now); err != nil {
		b.Fatal(err)
	}
	tx, err := database.db.BeginTx(ctx, nil)
	if err != nil {
		b.Fatal(err)
	}
	statement, err := tx.PrepareContext(ctx, `
		INSERT INTO users (
			email, password_hash, uuid, subscription_token, transfer_enable, traffic_u, traffic_d,
			expired_at, remind_expire, remind_traffic, created_at, updated_at
		) VALUES (?, 'hash', ?, ?, 1000, ?, 0, ?, 1, 1, ?, ?)
	`)
	if err != nil {
		b.Fatal(err)
	}
	for index := 0; index < 100_000; index++ {
		used := int64(100)
		if index%10 == 0 {
			used = 800
		}
		expiresAt := now.Add(30 * 24 * time.Hour).Unix()
		if index%20 == 0 {
			expiresAt = now.Add(23 * time.Hour).Unix()
		}
		uuid := fmt.Sprintf("%08x-0000-4000-8000-%012x", index, index)
		token := fmt.Sprintf("%032x", index+1)
		if _, err := statement.ExecContext(ctx, fmt.Sprintf("benchmark-%06d@example.test", index), uuid, token, used, expiresAt, now.Unix(), now.Unix()); err != nil {
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
		day := now.AddDate(0, 0, index).Format("2006-01-02")
		if _, err := database.ScheduleSubscriptionReminders(ctx, now, day, 500); err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		if _, err := database.db.ExecContext(ctx, `DELETE FROM subscription_reminder_outbox`); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
	}
}
