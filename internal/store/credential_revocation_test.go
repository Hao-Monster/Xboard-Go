package store

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
)

type revocationCredentials struct {
	reset           PasswordResetRequestInput
	checked         PasswordResetChallenge
	quick, mail     []byte
	session, bearer string
}

func seedRevocationCredentials(t *testing.T, db *Store, user AdminUser, marker byte, now time.Time) revocationCredentials {
	t.Helper()
	ctx := t.Context()
	protector, err := security.NewPasswordResetProtector(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	input := passwordResetInput(t, protector, user.Email, "483729")
	if queued, err := db.RequestPasswordReset(ctx, input, now); err != nil || !queued {
		t.Fatalf("queue reset: queued=%v err=%v", queued, err)
	}
	checked, err := db.CheckPasswordResetChallenge(ctx, input.EmailDigest, input.CodeDigest, now)
	if err != nil {
		t.Fatal(err)
	}
	quick, mail := testLoginLinkDigest(marker), testLoginLinkDigest(marker+1)
	if err := db.CreateQuickLoginLink(ctx, user.ID, quick, "dashboard", now); err != nil {
		t.Fatal(err)
	}
	if queued, err := db.RequestMailLoginLink(ctx, MailLoginLinkRequestInput{
		Email: user.Email, ExpectedUserID: user.ID, EmailDigest: testLoginLinkDigest(marker + 2), TokenDigest: mail,
		TokenCipher: bytes.Repeat([]byte{marker}, 48), Redirect: "dashboard", LinkBaseURL: "https://panel.example.test",
	}, now); err != nil || !queued {
		t.Fatalf("queue login: queued=%v err=%v", queued, err)
	}
	session, bearer := fmt.Sprintf("qa-session-%d", user.ID), fmt.Sprintf("%064x", user.ID)
	if err := db.CreateSession(ctx, user.ID, session, "qa-csrf", now.Add(time.Hour), now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateAccessToken(ctx, CreateAccessTokenInput{UserID: user.ID, TokenHash: bearer, Name: "QA-revocation"}, now); err != nil {
		t.Fatal(err)
	}
	return revocationCredentials{input, checked, quick, mail, session, bearer}
}

func newRevocationUser(t *testing.T, db *Store, email string, now time.Time) AdminUser {
	t.Helper()
	user, err := db.CreateAdminUser(t.Context(), CreateAdminUserInput{Email: email, PasswordHash: "old-hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	return user
}

func TestCredentialChangesRevokeOutstandingCredentialsAtomically(t *testing.T) {
	for _, change := range []string{"self-password", "admin-reset", "admin-password", "email", "ban", "admin-role", "staff-role", "distributor-role", "password-recovery"} {
		t.Run(change, func(t *testing.T) {
			db := newPasswordResetStore(t)
			ctx := t.Context()
			now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
			if _, err := db.db.ExecContext(ctx, `UPDATE app_settings SET login_with_mail_link_enable=1 WHERE id=1`); err != nil {
				t.Fatal(err)
			}
			user := newRevocationUser(t, db, "qa-revoke@example.test", now)
			other := newRevocationUser(t, db, "qa-other@example.test", now)
			before := seedRevocationCredentials(t, db, user, 10, now)
			untouched := seedRevocationCredentials(t, db, other, 20, now)
			// An in-flight mail worker must lose its claim too; an already sent
			// message cannot be recalled, so redemption remains the boundary.
			if job, found, err := db.ClaimPasswordResetMail(ctx, "qa-reset-claim", now, time.Minute); err != nil || !found || job.Recipient != user.Email {
				t.Fatalf("claim reset: found=%v err=%v", found, err)
			}
			if job, found, err := db.ClaimLoginLinkMail(ctx, "qa-login-claim", now, time.Minute); err != nil || !found || job.UserID != user.ID {
				t.Fatalf("claim login: found=%v err=%v", found, err)
			}
			if job, found, err := db.ClaimPasswordResetMail(ctx, "qa-other-reset-claim", now, time.Minute); err != nil || !found || job.Recipient != other.Email {
				t.Fatalf("claim unrelated reset: found=%v err=%v", found, err)
			}
			if job, found, err := db.ClaimLoginLinkMail(ctx, "qa-other-login-claim", now, time.Minute); err != nil || !found || job.UserID != other.ID {
				t.Fatalf("claim unrelated login: found=%v err=%v", found, err)
			}
			outboxes := []struct{ table, cipher string }{{"password_reset_mail_outbox", "code_cipher"}, {"login_link_mail_outbox", "token_cipher"}}
			mailSnapshot := func(table, cipher string) string {
				t.Helper()
				var snapshot string
				query := "SELECT json_object('sent',sent_at,'failed',failed_at,'cancelled',cancelled_at,'cipher',hex(" + cipher + "),'claim',claim_token,'claimed',claimed_at,'attempts',attempt_count,'error',last_error,'updated',updated_at) FROM " + table + " WHERE user_id=?"
				if err := db.db.QueryRowContext(ctx, query, other.ID).Scan(&snapshot); err != nil {
					t.Fatal(err)
				}
				return snapshot
			}
			untouchedMail := make(map[string]string, len(outboxes))
			for _, outbox := range outboxes {
				untouchedMail[outbox.table] = mailSnapshot(outbox.table, outbox.cipher)
			}
			changedAt := now.Add(time.Second)
			var err error
			switch change {
			case "self-password":
				err = db.ChangePassword(ctx, user.ID, "old-hash", "new-hash", changedAt)
			case "admin-reset":
				_, err = db.ResetAdminUserPassword(ctx, user.ID, user.Revision, "new-hash", changedAt)
			case "password-recovery":
				err = db.ResetPasswordWithChallenge(ctx, before.reset.EmailDigest, before.reset.CodeDigest, before.checked, "new-hash", changedAt)
			default:
				input := UpdateAdminUserInput{Revision: user.Revision, Email: user.Email}
				value := true
				switch change {
				case "admin-password":
					hash := "new-hash"
					input.PasswordHash = &hash
				case "email":
					input.Email = "qa-renamed@example.test"
				case "ban":
					input.Banned = true
				case "admin-role":
					input.IsAdmin = &value
				case "staff-role":
					input.IsStaff = &value
				case "distributor-role":
					input.IsDistributor = &value
					name := "QA-distributor"
					input.DistributorName = &name
				}
				_, _, err = db.UpdateAdminUser(ctx, user.ID, input, changedAt)
			}
			if err != nil {
				t.Fatalf("credential change: %v", err)
			}
			for _, table := range []string{"login_link_tokens", "password_reset_challenges"} {
				var count int
				if err := db.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE user_id=?", user.ID).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count != 0 {
					t.Errorf("%s retained %d credentials", table, count)
				}
			}
			if _, err := db.CheckPasswordResetChallenge(ctx, before.reset.EmailDigest, before.reset.CodeDigest, changedAt); !errors.Is(err, ErrPasswordResetInvalid) {
				t.Errorf("old reset challenge remains valid: %v", err)
			}
			if err := db.ResetPasswordWithChallenge(ctx, before.reset.EmailDigest, before.reset.CodeDigest, before.checked, "stale-reset-hash", changedAt); !errors.Is(err, ErrPasswordResetInvalid) {
				t.Errorf("prechecked reset was not invalidated: %v", err)
			}
			for index, digest := range [][]byte{before.quick, before.mail} {
				_, err := db.ExchangeLoginLink(ctx, LoginLinkExchangeInput{TokenDigest: digest, SessionTokenHash: fmt.Sprintf("qa-late-%d", index), CSRFHash: "qa-late-csrf", SessionExpiresAt: now.Add(time.Hour)}, changedAt)
				if !errors.Is(err, ErrLoginLinkInvalid) {
					t.Errorf("old login link remains valid: %v", err)
				}
			}
			if _, err := db.AuthenticateSession(ctx, before.session, changedAt); !errors.Is(err, ErrNotFound) {
				t.Errorf("old session: %v", err)
			}
			if _, err := db.AuthenticateAccessToken(ctx, before.bearer, changedAt); !errors.Is(err, ErrNotFound) {
				t.Errorf("old bearer: %v", err)
			}
			for _, outbox := range outboxes {
				var cancelled, erased, released bool
				query := "SELECT cancelled_at IS NOT NULL, " + outbox.cipher + " IS NULL, claim_token IS NULL AND claimed_at IS NULL FROM " + outbox.table + " WHERE user_id=?"
				if err := db.db.QueryRowContext(ctx, query, user.ID).Scan(&cancelled, &erased, &released); err != nil {
					t.Fatal(err)
				}
				if !cancelled || !erased || !released {
					t.Errorf("%s cancelled=%v erased=%v released=%v", outbox.table, cancelled, erased, released)
				}
				if mailSnapshot(outbox.table, outbox.cipher) != untouchedMail[outbox.table] {
					t.Errorf("unrelated %s state, ciphertext or claim changed", outbox.table)
				}
			}
			if _, err := db.CheckPasswordResetChallenge(ctx, untouched.reset.EmailDigest, untouched.reset.CodeDigest, changedAt); err != nil {
				t.Errorf("unrelated reset affected: %v", err)
			}
			if _, err := db.AuthenticateSession(ctx, untouched.session, changedAt); err != nil {
				t.Errorf("unrelated session affected: %v", err)
			}
			if tokenUser, err := db.AuthenticateAccessToken(ctx, untouched.bearer, changedAt); err != nil || tokenUser.UserID != other.ID {
				t.Errorf("unrelated bearer affected: %v", err)
			}
			for index, digest := range [][]byte{untouched.quick, untouched.mail} {
				if _, err := db.ExchangeLoginLink(ctx, LoginLinkExchangeInput{TokenDigest: digest, SessionTokenHash: fmt.Sprintf("qa-other-login-%d", index), CSRFHash: "qa-other-csrf", SessionExpiresAt: now.Add(time.Hour)}, changedAt); err != nil {
					t.Errorf("unrelated link affected: %v", err)
				}
			}
		})
	}
}

func TestCredentialRevocationFailureRollsBackPasswordAndCredentials(t *testing.T) {
	db := newPasswordResetStore(t)
	ctx := t.Context()
	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	if _, err := db.db.ExecContext(ctx, `UPDATE app_settings SET login_with_mail_link_enable=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	user := newRevocationUser(t, db, "qa-atomic@example.test", now)
	before := seedRevocationCredentials(t, db, user, 30, now)
	if _, err := db.db.ExecContext(ctx, `CREATE TRIGGER qa_fail_credential_cleanup BEFORE UPDATE ON login_link_mail_outbox BEGIN SELECT RAISE(ABORT, 'qa cleanup failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := db.ChangePassword(ctx, user.ID, "old-hash", "new-hash", now.Add(time.Second)); err == nil {
		t.Fatal("credential change ignored cleanup failure")
	}
	current, err := db.FindUserByID(ctx, user.ID)
	if err != nil || current.PasswordHash != "old-hash" {
		t.Fatalf("password was not rolled back: %v", err)
	}
	if _, err := db.AuthenticateSession(ctx, before.session, now); err != nil {
		t.Errorf("session was not rolled back: %v", err)
	}
	if _, err := db.AuthenticateAccessToken(ctx, before.bearer, now); err != nil {
		t.Errorf("bearer was not rolled back: %v", err)
	}
	if _, err := db.CheckPasswordResetChallenge(ctx, before.reset.EmailDigest, before.reset.CodeDigest, now); err != nil {
		t.Errorf("reset was not rolled back: %v", err)
	}
	var links int
	if err := db.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM login_link_tokens WHERE user_id=?`, user.ID).Scan(&links); err != nil || links != 2 {
		t.Fatalf("links after rollback=%d err=%v", links, err)
	}
}
