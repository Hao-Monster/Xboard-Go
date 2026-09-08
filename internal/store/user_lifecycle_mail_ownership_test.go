package store

import (
	"bytes"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestUserLifecycleAuthMailOwnershipSurvivesEmailChange(t *testing.T) {
	for _, creation := range []string{"verified_registration", "administrator_creation"} {
		t.Run(creation, func(t *testing.T) {
			db, protector, now := newRegistrationEmailStore(t)
			ctx := t.Context()
			admin, err := db.FindUserByEmail(ctx, "registration-admin@example.test")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.db.ExecContext(ctx, `UPDATE app_settings SET login_with_mail_link_enable=1 WHERE id=1`); err != nil {
				t.Fatal(err)
			}
			email := "before-change@example.test"
			input := registrationEmailInput(t, protector, email, "127.0.0.1", "123456")
			for _, at := range []time.Time{now, now.Add(time.Minute)} {
				if queued, err := db.RequestRegistrationEmailVerification(ctx, input, at); err != nil || !queued {
					t.Fatalf("registration request queued=%t err=%v", queued, err)
				}
			}
			at := now.Add(2 * time.Minute)
			var userID int64
			if creation == "verified_registration" {
				user, err := db.RegisterUser(ctx, RegisterUserInput{Email: email, PasswordHash: "hash", SourceIP: input.SourceIP, EmailDigest: input.EmailDigest, EmailCodeDigest: input.CodeDigest}, at)
				if err != nil {
					t.Fatal(err)
				}
				userID = user.ID
			} else {
				user, err := db.CreateAdminUser(ctx, CreateAdminUserInput{Email: email, PasswordHash: "hash"}, at)
				if err != nil {
					t.Fatal(err)
				}
				userID = user.ID
			}
			reset := PasswordResetRequestInput{Email: email, EmailDigest: bytes.Repeat([]byte{8}, 32), CodeDigest: bytes.Repeat([]byte{9}, 32), CodeCipher: bytes.Repeat([]byte{10}, 32)}
			if queued, err := db.RequestPasswordReset(ctx, reset, at); err != nil || !queued {
				t.Fatalf("reset request queued=%t err=%v", queued, err)
			}
			login := MailLoginLinkRequestInput{Email: email, ExpectedUserID: userID, EmailDigest: bytes.Repeat([]byte{11}, 32), TokenDigest: bytes.Repeat([]byte{12}, 32), TokenCipher: bytes.Repeat([]byte{13}, 32), Redirect: "dashboard", LinkBaseURL: "https://panel.example.test"}
			if queued, err := db.RequestMailLoginLink(ctx, login, at); err != nil || !queued {
				t.Fatalf("login request queued=%t err=%v", queued, err)
			}
			for _, table := range []string{"password_reset_mail_outbox", "registration_email_mail_outbox", "login_link_mail_outbox"} {
				if _, err = db.db.ExecContext(ctx, `UPDATE `+table+` SET last_error=?`, "550 unknown recipient "+email); err != nil {
					t.Fatal(err)
				}
			}
			user, err := db.GetAdminUser(ctx, userID)
			if err != nil {
				t.Fatal(err)
			}
			changed, _, err := db.UpdateAdminUser(ctx, userID, UpdateAdminUserInput{Revision: user.Revision, Email: "after-change@example.test"}, at)
			if err != nil {
				t.Fatal(err)
			}
			inactive, _, err := db.ChangeUserLifecycle(ctx, UserLifecycleInput{AdministratorID: admin.ID, UserID: userID, Revision: changed.Revision, Action: "deactivate"}, at)
			if err != nil {
				t.Fatal(err)
			}
			anonymous, _, err := db.ChangeUserLifecycle(ctx, UserLifecycleInput{AdministratorID: admin.ID, UserID: userID, Revision: inactive.Revision, Action: "anonymize"}, at.Add(UserRecoveryWindow))
			if err != nil {
				t.Fatal(err)
			}
			for _, table := range []string{"password_reset_mail_outbox", "registration_email_mail_outbox", "login_link_mail_outbox"} {
				var count, scrubbed int
				if err = db.db.QueryRowContext(ctx, `SELECT count(*),sum(recipient=? AND last_error='[anonymized]' AND cancelled_at IS NOT NULL AND claim_token IS NULL) FROM `+table, anonymous.Email).Scan(&count, &scrubbed); err != nil {
					t.Fatal(err)
				}
				want := 1
				if table == "registration_email_mail_outbox" {
					want = 2
				}
				if count != want || scrubbed != want {
					t.Errorf("%s historical identity/claim remains: rows=%d scrubbed=%d want=%d", table, count, scrubbed, want)
				}
			}
		})
	}
}

func TestUserLifecycleRegistrationMailboxReuseDoesNotAdoptUnknownHistory(t *testing.T) {
	db, protector, now := newRegistrationEmailStore(t)
	ctx := t.Context()
	email := "reused-register@example.test"
	// A pre-upgrade row with no verifiable owner is not evidence that the next
	// account at the same address owns this history.
	if _, err := db.db.ExecContext(ctx, `INSERT INTO registration_email_mail_outbox(email_digest,recipient,app_name,available_at,failed_at,last_error,created_at,updated_at) VALUES(zeroblob(32),?,'Legacy',?,?,'legacy unknown owner',?,?)`, email, now.Unix(), now.Unix(), now.Unix(), now.Unix()); err != nil {
		t.Fatal(err)
	}
	input := registrationEmailInput(t, protector, email, "127.0.0.1", "123456")
	if queued, err := db.RequestRegistrationEmailVerification(ctx, input, now.Add(time.Minute)); err != nil || !queued {
		t.Fatalf("new request queued=%t err=%v", queued, err)
	}
	first, err := db.CreateAdminUser(ctx, CreateAdminUserInput{Email: email, PasswordHash: "first-hash"}, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	changed, _, err := db.UpdateAdminUser(ctx, first.ID, UpdateAdminUserInput{Revision: first.Revision, Email: "first-renamed@example.test"}, now.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if queued, err := db.RequestRegistrationEmailVerification(ctx, input, now.Add(4*time.Minute)); err != nil || !queued {
		t.Fatalf("reused request queued=%t err=%v", queued, err)
	}
	second, err := db.CreateAdminUser(ctx, CreateAdminUserInput{Email: email, PasswordHash: "second-hash"}, now.Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	admin, err := db.FindUserByEmail(ctx, "registration-admin@example.test")
	if err != nil {
		t.Fatal(err)
	}
	inactive, _, err := db.ChangeUserLifecycle(ctx, UserLifecycleInput{AdministratorID: admin.ID, UserID: first.ID, Revision: changed.Revision, Action: "deactivate"}, now.Add(6*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = db.ChangeUserLifecycle(ctx, UserLifecycleInput{AdministratorID: admin.ID, UserID: first.ID, Revision: inactive.Revision, Action: "anonymize"}, now.Add(UserRecoveryWindow+6*time.Minute)); err != nil {
		t.Fatal(err)
	}
	var unknown, firstMail, secondMail string
	for id, target := range map[int64]*string{1: &unknown, 2: &firstMail, 3: &secondMail} {
		if err = db.db.QueryRowContext(ctx, `SELECT recipient FROM registration_email_mail_outbox WHERE id=?`, id).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if unknown != email || secondMail != second.Email || !strings.HasSuffix(firstMail, "@users.invalid") {
		t.Fatalf("ownership isolation: unknown=%q first=%q second=%q", unknown, firstMail, secondMail)
	}
	for id, want := range map[int64]int64{1: 0, 2: first.ID, 3: second.ID} {
		var owner sql.NullInt64
		var pending int
		if err = db.db.QueryRowContext(ctx, `SELECT user_id,owner_pending FROM registration_email_mail_outbox WHERE id=?`, id).Scan(&owner, &pending); err != nil || owner.Int64 != want || owner.Valid != (want != 0) || pending != 0 {
			t.Fatalf("mail id=%d owner=%v pending=%d err=%v", id, owner, pending, err)
		}
	}
	for _, query := range []string{
		`UPDATE registration_email_mail_outbox SET user_id=NULL WHERE id=2`,
		`UPDATE registration_email_mail_outbox SET user_id=` + fmt.Sprint(second.ID) + ` WHERE id=2`,
		`UPDATE registration_email_mail_outbox SET owner_pending=1 WHERE id=1`,
	} {
		if _, err = db.db.ExecContext(ctx, query); err == nil {
			t.Errorf("immutable ownership weakened: %s", query)
		}
	}
}

func TestUserLifecycleMailOwnershipMigrationOnlyBackfillsProvenLatestReset(t *testing.T) {
	db, admin, user, now := lifecycleFixture(t)
	ctx := t.Context()
	if _, err := db.db.ExecContext(ctx, `UPDATE app_settings SET smtp_enabled=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	input := PasswordResetRequestInput{Email: user.Email, EmailDigest: bytes.Repeat([]byte{1}, 32), CodeDigest: bytes.Repeat([]byte{2}, 32), CodeCipher: bytes.Repeat([]byte{3}, 32)}
	for _, at := range []time.Time{now, now.Add(time.Minute)} {
		if queued, err := db.RequestPasswordReset(ctx, input, at); err != nil || !queued {
			t.Fatalf("reset queued=%t err=%v", queued, err)
		}
	}
	if _, _, err := db.UpdateAdminUser(ctx, user.ID, UpdateAdminUserInput{Revision: user.Revision, Email: "renamed-reset-owner@example.test"}, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `INSERT INTO registration_email_mail_outbox(email_digest,recipient,app_name,available_at,failed_at,created_at,updated_at) VALUES(zeroblob(32),?,'Legacy',?,?,?,?)`, user.Email, now.Unix(), now.Unix(), now.Unix(), now.Unix()); err != nil {
		t.Fatal(err)
	}
	// Restore the actual pre-v62 column shapes for the two altered tables. Other
	// historical schemas are irrelevant to this focused upgrade regression.
	for _, statement := range []string{
		`DROP TRIGGER users_registration_mail_owner`,
		`DROP TRIGGER password_reset_mail_owner_guard`,
		`DROP TRIGGER registration_email_mail_owner_guard`,
		`DROP INDEX idx_password_reset_mail_owner`,
		`DROP INDEX idx_password_reset_mail_digest`,
		`DROP INDEX idx_registration_email_mail_owner`,
		`DROP INDEX idx_registration_email_mail_pending_owner`,
		`ALTER TABLE registration_email_mail_outbox DROP COLUMN owner_pending`,
		`ALTER TABLE registration_email_mail_outbox DROP COLUMN user_id`,
		`ALTER TABLE password_reset_mail_outbox DROP COLUMN user_id`,
	} {
		if _, err := db.db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("restore historical mail shape: %v", err)
		}
	}
	for range 2 {
		tx, err := db.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err = applySchemaV62UserLifecycle(ctx, tx); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	for id, want := range map[int64]int64{1: 0, 2: user.ID} {
		var owner sql.NullInt64
		if err := db.db.QueryRowContext(ctx, `SELECT user_id FROM password_reset_mail_outbox WHERE id=?`, id).Scan(&owner); err != nil || owner.Int64 != want || owner.Valid != (want != 0) {
			t.Fatalf("reset historical id=%d owner=%v err=%v", id, owner, err)
		}
	}
	if _, err := db.db.ExecContext(ctx, `UPDATE password_reset_mail_outbox SET user_id=? WHERE id=2`, admin.ID); err == nil {
		t.Fatal("known password reset owner reassigned")
	}
	if _, err := db.CreateAdminUser(ctx, CreateAdminUserInput{Email: user.Email, PasswordHash: "new-owner"}, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	var owner sql.NullInt64
	if err := db.db.QueryRowContext(ctx, `SELECT user_id FROM registration_email_mail_outbox WHERE id=1`).Scan(&owner); err != nil || owner.Valid {
		t.Fatalf("unknown old registration history falsely assigned: owner=%v err=%v", owner, err)
	}
	if err := ValidateSchema(ctx, db.db, currentSchemaVersion); err != nil {
		t.Fatalf("upgraded owner schema invalid: %v", err)
	}
}

func TestUserLifecycleMailOwnershipSchemaRejectsWeakenedProtection(t *testing.T) {
	for name, mutation := range map[string]string{
		"binding_trigger": `DROP TRIGGER users_registration_mail_owner; CREATE TRIGGER users_registration_mail_owner AFTER INSERT ON users BEGIN SELECT 1; END`,
		"owner_guard":     `DROP TRIGGER password_reset_mail_owner_guard; CREATE TRIGGER password_reset_mail_owner_guard BEFORE UPDATE OF user_id ON password_reset_mail_outbox BEGIN SELECT 1; END`,
		"pending_index":   `DROP INDEX idx_registration_email_mail_pending_owner; CREATE INDEX idx_registration_email_mail_pending_owner ON registration_email_mail_outbox(recipient COLLATE NOCASE,created_at) WHERE user_id IS NULL AND owner_pending=0`,
	} {
		t.Run(name, func(t *testing.T) {
			db := newTestStore(t)
			if _, err := db.db.ExecContext(t.Context(), mutation); err != nil {
				t.Fatal(err)
			}
			if err := ValidateSchema(t.Context(), db.db, currentSchemaVersion); err == nil {
				t.Fatal("weakened mail ownership protection passed schema validation")
			}
		})
	}
}
