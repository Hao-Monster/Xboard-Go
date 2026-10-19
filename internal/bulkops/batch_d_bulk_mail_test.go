package bulkops

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/mailer"
	appsettings "github.com/Hao-Monster/Xboard-Go/internal/settings"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestBL16BulkMailCancelAfterClaimAndStaleReap(t *testing.T) {
	t.Run("cancelled_between_claim_and_active_check_releases_claim_and_terminates_job", func(t *testing.T) {
		ctx := context.Background()
		dbPath := filepath.ToSlash(filepath.Join(t.TempDir(), "bulk-bl16.db"))
		database, err := store.OpenSQLite("file:" + dbPath)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = database.Close() })
		if err := database.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
		administrator, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{
			Email: "bl16-admin@example.test", PasswordHash: "hash", IsAdmin: true,
		}, now)
		if err != nil {
			t.Fatal(err)
		}
		target, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{
			Email: "bl16-target@example.test", PasswordHash: "hash", TransferEnable: 20 << 30,
		}, now)
		if err != nil {
			t.Fatal(err)
		}
		cipherBox, err := appsettings.NewCipher(bytes.Repeat([]byte{0x51}, 32))
		if err != nil {
			t.Fatal(err)
		}
		settings, err := database.GetTicketSettings(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.UpdateTicketSettings(ctx, administrator.ID, settings.Revision, store.SaveTicketSettingsInput{
			AppName: "U5 Board", AppURL: "https://panel.example.test", SMTPEnabled: true,
			SMTPHost: "smtp.example.test", SMTPPort: 587, SMTPEncryption: mailer.EncryptionStartTLS,
			SMTPFromAddress: "no-reply@example.test",
		}, now); err != nil {
			t.Fatal(err)
		}

		job, err := database.CreateAdminUserBulkJob(ctx, store.CreateAdminUserBulkJobInput{
			Kind:            store.AdminUserBulkKindMail,
			AdministratorID: administrator.ID,
			Scope:           store.AdminUserBulkScope{Scope: store.AdminUserBulkScopeSelected, UserIDs: []int64{target.ID}},
			Subject:         "取消窗口测试",
			Content:         "should not send after cancellation",
		}, now)
		if err != nil {
			t.Fatal(err)
		}

		raw, err := sql.Open("sqlite", "file:"+dbPath)
		if err != nil {
			t.Fatal(err)
		}
		defer raw.Close()
		// Simulate cancellation arriving immediately after ClaimAdminUserBulkMail sets job status='running'
		// and before RunMailOnce checks AdminUserBulkMailClaimActive.
		if _, err := raw.ExecContext(ctx, `
			CREATE TRIGGER test_bl16_cancel_after_claim
			AFTER UPDATE OF status ON admin_user_bulk_jobs
			WHEN NEW.status = 'running'
			BEGIN
				UPDATE admin_user_bulk_jobs
				SET status = 'cancelling', cancelled_at = NEW.updated_at
				WHERE id = NEW.id;
			END
		`); err != nil {
			t.Fatal(err)
		}

		sender := &captureSender{}
		service, err := New(database, Options{
			Cipher:     cipherBox,
			Sender:     sender,
			ExportRoot: t.TempDir(),
		})
		if err != nil {
			t.Fatal(err)
		}

		worked, err := service.RunMailOnce(ctx, now)
		if err != nil || !worked {
			t.Fatalf("RunMailOnce() = (%v, %v), want true, nil", worked, err)
		}
		if got := len(sender.Messages()); got != 0 {
			t.Fatalf("sender.Messages() len = %d, want 0", got)
		}

		targets, err := database.ListAdminUserBulkTargets(ctx, job.ID, 0, 10)
		if err != nil || len(targets) != 1 {
			t.Fatalf("ListAdminUserBulkTargets() = (%#v, %v)", targets, err)
		}
		if targets[0].Status != store.AdminUserBulkTargetCancelled {
			t.Fatalf("target.Status = %q, want %q", targets[0].Status, store.AdminUserBulkTargetCancelled)
		}

		updatedJob, err := database.GetAdminUserBulkJob(ctx, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if updatedJob.Status != store.AdminUserBulkStatusCancelled || updatedJob.CancelledCount != 1 || updatedJob.ProcessedCount != 1 {
			t.Fatalf("updatedJob = %#v, want Status=cancelled CancelledCount=1 ProcessedCount=1", updatedJob)
		}
	})

	t.Run("abandoned_processing_claim_on_cancelling_job_is_reaped_after_lease", func(t *testing.T) {
		database, administrator, target, cipherBox, now := newBulkServiceFixture(t)
		ctx := context.Background()

		job, err := database.CreateAdminUserBulkJob(ctx, store.CreateAdminUserBulkJobInput{
			Kind:            store.AdminUserBulkKindMail,
			AdministratorID: administrator.ID,
			Scope:           store.AdminUserBulkScope{Scope: store.AdminUserBulkScopeSelected, UserIDs: []int64{target.ID}},
			Subject:         "失联回收测试",
			Content:         "worker vanished while cancelling",
		}, now)
		if err != nil {
			t.Fatal(err)
		}

		if _, claimed, err := database.ClaimAdminUserBulkMail(ctx, "vanished-worker-token", now, mailClaimLease); err != nil || !claimed {
			t.Fatalf("ClaimAdminUserBulkMail() = (%v, %v), want true, nil", claimed, err)
		}
		cancelledJob, err := database.CancelAdminUserBulkJob(ctx, job.ID, administrator.ID, now.Add(5*time.Second))
		if err != nil || cancelledJob.Status != store.AdminUserBulkStatusCancelling {
			t.Fatalf("CancelAdminUserBulkJob() = (%#v, %v), want status=cancelling", cancelledJob, err)
		}

		sender := &captureSender{}
		service, err := New(database, Options{
			Cipher:     cipherBox,
			Sender:     sender,
			ExportRoot: t.TempDir(),
		})
		if err != nil {
			t.Fatal(err)
		}

		// Before lease expiry (t0 + lease - 1s): must NOT reap yet.
		if worked, err := service.RunMailOnce(ctx, now.Add(mailClaimLease-time.Second)); err != nil || worked {
			t.Fatalf("RunMailOnce(before lease) = (%v, %v), want false, nil", worked, err)
		}
		midJob, err := database.GetAdminUserBulkJob(ctx, job.ID)
		if err != nil || midJob.Status != store.AdminUserBulkStatusCancelling {
			t.Fatalf("midJob = %#v, %v, want still cancelling", midJob, err)
		}

		// After lease expiry (t0 + lease + 1s): reaps stale processing target as failed (unknown delivery result) and finishes job as cancelled.
		if worked, err := service.RunMailOnce(ctx, now.Add(mailClaimLease+time.Second)); err != nil || worked {
			t.Fatalf("RunMailOnce(after lease) = (%v, %v), want false, nil", worked, err)
		}
		if got := len(sender.Messages()); got != 0 {
			t.Fatalf("sender.Messages() len = %d, want 0", got)
		}

		targets, err := database.ListAdminUserBulkTargets(ctx, job.ID, 0, 10)
		if err != nil || len(targets) != 1 {
			t.Fatalf("ListAdminUserBulkTargets() = (%#v, %v)", targets, err)
		}
		if targets[0].Status != store.AdminUserBulkTargetFailed || targets[0].LastError != "mail delivery result unknown after cancellation" {
			t.Fatalf("reaped target = %#v, want status=failed last_error='mail delivery result unknown after cancellation'", targets[0])
		}

		finalJob, err := database.GetAdminUserBulkJob(ctx, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if finalJob.Status != store.AdminUserBulkStatusCancelled || finalJob.FailureCount != 1 || finalJob.ProcessedCount != 1 {
			t.Fatalf("finalJob = %#v, want Status=cancelled FailureCount=1 ProcessedCount=1", finalJob)
		}
	})
}
