package store

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestUserLifecycleAnonymizesHistoricalTicketMailWithoutChangingOtherRecipients(t *testing.T) {
	db, admin, user, now := lifecycleFixture(t)
	ctx := t.Context()
	if _, err := db.db.ExecContext(ctx, `UPDATE app_settings SET smtp_enabled=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `UPDATE users SET is_admin=1 WHERE id=?`, user.ID); err != nil {
		t.Fatal(err)
	}
	other, err := db.CreateAdminUser(ctx, CreateAdminUserInput{Email: "other-recipient@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	makeMail := func(owner, author int64, subject string) int64 {
		t.Helper()
		ticket, err := db.CreateTicket(ctx, owner, SaveTicketInput{Subject: subject, Level: 1, Message: "initial"}, now)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.ReplyTicketAsAdmin(ctx, author, ticket.ID, "private answer", now.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		var id int64
		if err = db.db.QueryRowContext(ctx, `SELECT o.id FROM ticket_mail_outbox o JOIN ticket_messages m ON m.id=o.ticket_message_id WHERE m.ticket_id=?`, ticket.ID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err = db.db.ExecContext(ctx, `UPDATE tickets SET status=1 WHERE id=?`, ticket.ID); err != nil {
			t.Fatal(err)
		}
		return id
	}
	ownedMail := makeMail(user.ID, admin.ID, "private owned subject")
	// An anonymized administrator may also have authored a reply to somebody
	// else's ticket. Its content is scrubbed, but that owner's email is retained.
	authoredMail := makeMail(other.ID, user.ID, "other subject")
	if _, err = db.db.ExecContext(ctx, `DELETE FROM ticket_mail_throttle WHERE user_id=?`, other.ID); err != nil {
		t.Fatal(err)
	}
	unrelatedMail := makeMail(other.ID, admin.ID, "unrelated subject")
	for _, id := range []int64{ownedMail, authoredMail, unrelatedMail} {
		if _, err = db.db.ExecContext(ctx, `UPDATE ticket_mail_outbox SET failed_at=?,attempt_count=3,last_error='550 unknown recipient '||recipient WHERE id=?`, now.Add(time.Minute).Unix(), id); err != nil {
			t.Fatal(err)
		}
	}
	changed, _, err := db.UpdateAdminUser(ctx, user.ID, UpdateAdminUserInput{Revision: user.Revision, Email: "new-private-address@example.test"}, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	inactive, _, err := db.ChangeUserLifecycle(ctx, UserLifecycleInput{AdministratorID: admin.ID, UserID: user.ID, Revision: changed.Revision, Action: "deactivate"}, now.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	anonymous, _, err := db.ChangeUserLifecycle(ctx, UserLifecycleInput{AdministratorID: admin.ID, UserID: user.ID, Revision: inactive.Revision, Action: "anonymize"}, now.Add(UserRecoveryWindow+3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		id                              int64
		recipient, subject, body, error string
	}{
		{ownedMail, anonymous.Email, "[anonymized]", "[anonymized]", "[anonymized]"},
		{authoredMail, other.Email, "[anonymized]", "[anonymized]", "[anonymized]"},
		{unrelatedMail, other.Email, "unrelated subject", "private answer", "550 unknown recipient " + other.Email},
	} {
		var recipient, subject, body, failure string
		var attempts int
		if err = db.db.QueryRowContext(ctx, `SELECT recipient,ticket_subject,reply_message,COALESCE(last_error,''),attempt_count FROM ticket_mail_outbox WHERE id=?`, item.id).Scan(&recipient, &subject, &body, &failure, &attempts); err != nil {
			t.Fatal(err)
		}
		if recipient != item.recipient || subject != item.subject || body != item.body || failure != item.error || attempts != 3 {
			t.Errorf("mail %d retained identity or changed unrelated facts: recipient=%q subject=%q body=%q failure=%q attempts=%d", item.id, recipient, subject, body, failure, attempts)
		}
	}
	failures, err := db.ListTicketMailFailures(ctx, 1, 100)
	if err != nil || failures.Total != 3 {
		t.Fatalf("diagnostic failure facts changed: %+v %v", failures, err)
	}
	for _, failure := range failures.Items {
		if strings.Contains(failure.Recipient+failure.LastError, user.Email) || strings.Contains(failure.Recipient+failure.LastError, changed.Email) {
			t.Errorf("diagnostic API exposes anonymized identity: %+v", failure)
		}
	}
}

func TestUserLifecycleAnonymizesNotificationErrorsAndOnlyOwnedBulkFailure(t *testing.T) {
	db, admin, user, now := lifecycleFixture(t)
	ctx := t.Context()
	if _, err := db.db.ExecContext(ctx, `UPDATE app_settings SET smtp_enabled=1,smtp_host='smtp.example.test',smtp_from_address='ops@example.test' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	other, err := db.CreateAdminUser(ctx, CreateAdminUserInput{Email: "other-failure@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	// Imported/historical terminal rows retain the same raw transport failures
	// produced by Fail* methods. Their delivery counts must survive scrubbing.
	for i, owner := range []AdminUser{user, other} {
		if _, err = db.db.ExecContext(ctx, `UPDATE users SET telegram_id=? WHERE id=?`, 7700+i, owner.ID); err != nil {
			t.Fatal(err)
		}
		for _, query := range []string{
			`INSERT INTO password_reset_mail_outbox(user_id,email_digest,recipient,app_name,available_at,failed_at,last_error,created_at,updated_at) VALUES(?,zeroblob(32),?,'Xboard',?,?,?, ?,?)`,
			`INSERT INTO registration_email_mail_outbox(user_id,email_digest,recipient,app_name,available_at,failed_at,last_error,created_at,updated_at) VALUES(?,zeroblob(32),?,'Xboard',?,?,?, ?,?)`,
		} {
			if _, err = db.db.ExecContext(ctx, query, owner.ID, owner.Email, now.Unix(), now.Unix(), "550 unknown recipient "+owner.Email, now.Unix(), now.Unix()); err != nil {
				t.Fatal(err)
			}
		}
		if _, err = db.db.ExecContext(ctx, `INSERT INTO login_link_mail_outbox(token_digest,user_id,recipient,redirect_path,app_name,available_at,failed_at,last_error,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, []byte(strings.Repeat(fmt.Sprint(i+1), 32)), owner.ID, owner.Email, "dashboard", "Xboard", now.Unix(), now.Unix(), "550 unknown recipient "+owner.Email, now.Unix(), now.Unix()); err != nil {
			t.Fatal(err)
		}
		if _, err = db.db.ExecContext(ctx, `INSERT INTO subscription_reminder_outbox(user_id,kind,reminder_day,recipient,app_name,available_at,failed_at,last_error,created_at,updated_at) VALUES(?,'expire','2026-09-08',?,'Xboard',?,?,?,?,?)`, owner.ID, owner.Email, now.Unix(), now.Unix(), "550 unknown recipient "+owner.Email, now.Unix(), now.Unix()); err != nil {
			t.Fatal(err)
		}
		if _, err = db.db.ExecContext(ctx, `INSERT INTO telegram_message_outbox(source_kind,source_id,chat_id,text,available_at,failed_at,last_error,created_at,updated_at) VALUES('command',?,?,'private text',?,?,?,?,?)`, owner.ID, 7700+i, now.Unix(), now.Unix(), "failed notification for "+owner.Email, now.Unix(), now.Unix()); err != nil {
			t.Fatal(err)
		}
	}
	jobs := make([]AdminUserBulkJob, 0, 2)
	for _, errorOwner := range []AdminUser{user, other} {
		job, err := db.CreateAdminUserBulkJob(ctx, CreateAdminUserBulkJobInput{Kind: AdminUserBulkKindMail, AdministratorID: admin.ID, Subject: "Shared notice", Content: "Shared content", Scope: AdminUserBulkScope{Scope: AdminUserBulkScopeSelected, UserIDs: []int64{user.ID, other.ID}}}, now)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.db.ExecContext(ctx, `UPDATE admin_user_bulk_targets SET status='failed',attempt_count=3,processed_at=?,last_error='550 unknown recipient '||email WHERE job_id=?`, now.Unix(), job.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = db.db.ExecContext(ctx, `UPDATE admin_user_bulk_jobs SET status='failed',processed_count=2,failure_count=2,last_error=?,completed_at=? WHERE id=?`, "550 unknown recipient "+errorOwner.Email, now.Unix(), job.ID); err != nil {
			t.Fatal(err)
		}
		jobs = append(jobs, job)
	}
	inactive, _, err := db.ChangeUserLifecycle(ctx, UserLifecycleInput{AdministratorID: admin.ID, UserID: user.ID, Revision: user.Revision, Action: "deactivate"}, now)
	if err != nil {
		t.Fatal(err)
	}
	anonymous, _, err := db.ChangeUserLifecycle(ctx, UserLifecycleInput{AdministratorID: admin.ID, UserID: user.ID, Revision: inactive.Revision, Action: "anonymize"}, now.Add(UserRecoveryWindow))
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"password_reset_mail_outbox", "registration_email_mail_outbox", "login_link_mail_outbox", "subscription_reminder_outbox"} {
		rows, err := db.db.QueryContext(ctx, `SELECT recipient,COALESCE(last_error,''),failed_at FROM `+table+` ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for ; rows.Next(); count++ {
			var recipient, failure string
			var failedAt sql.NullInt64
			if err = rows.Scan(&recipient, &failure, &failedAt); err != nil {
				t.Fatal(err)
			}
			wantRecipient, wantFailure := anonymous.Email, "[anonymized]"
			if count == 1 {
				wantRecipient, wantFailure = other.Email, "550 unknown recipient "+other.Email
			}
			if recipient != wantRecipient || failure != wantFailure || !failedAt.Valid || failedAt.Int64 != now.Unix() {
				t.Errorf("%s row%d: recipient=%q error=%q failedAt=%v", table, count, recipient, failure, failedAt)
			}
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if count != 2 {
			t.Errorf("%s notification facts lost: rows=%d", table, count)
		}
	}
	for i, job := range jobs {
		var failure, subject, content string
		var count int
		if err = db.db.QueryRowContext(ctx, `SELECT COALESCE(last_error,''),failure_count,subject,content FROM admin_user_bulk_jobs WHERE id=?`, job.ID).Scan(&failure, &count, &subject, &content); err != nil {
			t.Fatal(err)
		}
		want := "[anonymized]"
		if i == 1 {
			want = "550 unknown recipient " + other.Email
		}
		if failure != want || count != 2 || subject != "Shared notice" || content != "Shared content" {
			t.Errorf("bulk job %d changed another recipient or retained PII: %q %d %q %q", i, failure, count, subject, content)
		}
		for _, owner := range []AdminUser{user, other} {
			var email, targetFailure string
			if err = db.db.QueryRowContext(ctx, `SELECT email,COALESCE(last_error,'') FROM admin_user_bulk_targets WHERE job_id=? AND user_id=?`, job.ID, owner.ID).Scan(&email, &targetFailure); err != nil {
				t.Fatal(err)
			}
			wantEmail, wantFailure := other.Email, "550 unknown recipient "+other.Email
			if owner.ID == user.ID {
				wantEmail, wantFailure = anonymous.Email, "[anonymized]"
			}
			if email != wantEmail || targetFailure != wantFailure {
				t.Errorf("bulk target %d: email=%q error=%q", owner.ID, email, targetFailure)
			}
		}
	}
	for _, owner := range []AdminUser{user, other} {
		var body, failure string
		if err = db.db.QueryRowContext(ctx, `SELECT text,COALESCE(last_error,'') FROM telegram_message_outbox WHERE source_kind='command' AND source_id=?`, owner.ID).Scan(&body, &failure); err != nil {
			t.Fatal(err)
		}
		wantBody, wantFailure := "private text", "failed notification for "+other.Email
		if owner.ID == user.ID {
			wantBody, wantFailure = "[anonymized]", "[anonymized]"
		}
		if body != wantBody || failure != wantFailure {
			t.Errorf("Telegram recipient %d: text=%q error=%q", owner.ID, body, failure)
		}
	}
}

func TestUserLifecycleHistoricalClaimAndReassignedMailboxUseTicketOwnership(t *testing.T) {
	db, admin, user, now := lifecycleFixture(t)
	ctx := t.Context()
	if _, err := db.db.ExecContext(ctx, `UPDATE app_settings SET smtp_enabled=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	mailFor := func(owner AdminUser, at time.Time) TicketMailJob {
		t.Helper()
		ticket, err := db.CreateTicket(ctx, owner.ID, SaveTicketInput{Subject: "Support", Level: 1, Message: "initial"}, at)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.ReplyTicketAsAdmin(ctx, admin.ID, ticket.ID, "reply for "+owner.Email, at); err != nil {
			t.Fatal(err)
		}
		job, claimed, err := db.ClaimTicketMail(ctx, fmt.Sprintf("claim-owner-%d", owner.ID), at, time.Hour)
		if err != nil || !claimed || job.Recipient != owner.Email {
			t.Fatalf("mail claim=%+v claimed=%t err=%v", job, claimed, err)
		}
		return job
	}
	oldClaim := mailFor(user, now)
	changed, _, err := db.UpdateAdminUser(ctx, user.ID, UpdateAdminUserInput{Revision: user.Revision, Email: "replacement@example.test"}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	other, err := db.CreateAdminUser(ctx, CreateAdminUserInput{Email: user.Email, PasswordHash: "new-owner-hash"}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	otherClaim := mailFor(other, now.Add(time.Minute))
	inactive, _, err := db.ChangeUserLifecycle(ctx, UserLifecycleInput{AdministratorID: admin.ID, UserID: user.ID, Revision: changed.Revision, Action: "deactivate"}, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if active, err := db.OutboxClaimActive(ctx, "ticket", oldClaim.ID, fmt.Sprintf("claim-owner-%d", user.ID)); err != nil || active {
		t.Fatalf("historical owner's claim not cancelled: active=%t err=%v", active, err)
	}
	anonymous, _, err := db.ChangeUserLifecycle(ctx, UserLifecycleInput{AdministratorID: admin.ID, UserID: user.ID, Revision: inactive.Revision, Action: "anonymize"}, now.Add(UserRecoveryWindow+2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if active, err := db.OutboxClaimActive(ctx, "ticket", otherClaim.ID, fmt.Sprintf("claim-owner-%d", other.ID)); err != nil || !active {
		t.Fatalf("new mailbox owner's unrelated claim cancelled: active=%t err=%v", active, err)
	}
	for _, row := range []struct {
		id               int64
		recipient, reply string
	}{{oldClaim.ID, anonymous.Email, "[anonymized]"}, {otherClaim.ID, other.Email, "reply for " + other.Email}} {
		var recipient, reply string
		if err = db.db.QueryRowContext(ctx, `SELECT recipient,reply_message FROM ticket_mail_outbox WHERE id=?`, row.id).Scan(&recipient, &reply); err != nil || recipient != row.recipient || reply != row.reply {
			t.Errorf("mailbox ownership mismatch id=%d recipient=%q reply=%q err=%v", row.id, recipient, reply, err)
		}
	}
}
