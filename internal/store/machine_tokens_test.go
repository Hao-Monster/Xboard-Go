package store

import (
	"bytes"
	"context"
	appsettings "github.com/Hao-Monster/Xboard-Go/internal/settings"
	"testing"
	"time"
)

func TestMachineTokenEncryptionReadAndReset(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	box, err := appsettings.NewCipher(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	m, enrollment, err := s.CreateMachine(ctx, CreateMachineInput{Name: "token-test", IsActive: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	old, err := s.ExchangeEnrollment(ctx, m.ID, enrollment.Code, now)
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.MachineToken(ctx, m.ID, box)
	if err != nil || token != "" {
		t.Fatal("hash-only token must remain unavailable")
	}
	if _, err := s.AuthenticateMachine(ctx, m.ID, old.Token, now); err != nil {
		t.Fatal("view changed old credential")
	}
	if _, err := s.ResetMachineToken(ctx, m.ID, nil, now); err == nil {
		t.Fatal("missing encryption key accepted")
	}
	if _, err := s.AuthenticateMachine(ctx, m.ID, old.Token, now); err != nil {
		t.Fatal("failed reset revoked credential")
	}
	pending, err := s.CreateEnrollment(ctx, m.ID, false, now)
	if err != nil {
		t.Fatal(err)
	}
	token, err = s.ResetMachineToken(ctx, m.ID, box, now)
	if err != nil || token == "" {
		t.Fatal("reset failed")
	}
	read, err := s.MachineToken(ctx, m.ID, box)
	if err != nil || read != token {
		t.Fatal("read/reset mismatch")
	}
	if _, err := s.AuthenticateMachine(ctx, m.ID, old.Token, now); err == nil {
		t.Fatal("old credential still active")
	}
	if _, err := s.AuthenticateMachine(ctx, m.ID, token, now); err != nil {
		t.Fatal("new credential invalid")
	}
	if _, err := s.ExchangeEnrollment(ctx, m.ID, pending.Code, now); err == nil {
		t.Fatal("old enrollment still active")
	}
	var payload []byte
	if err := s.db.QueryRow(`SELECT token_cipher FROM server_machine_credentials WHERE machine_id=? AND revoked_at IS NULL`, m.ID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, []byte(token)) {
		t.Fatal("plaintext token stored")
	}
	wrong, _ := appsettings.NewCipher(bytes.Repeat([]byte{8}, 32))
	if _, err := s.MachineToken(ctx, m.ID, wrong); err == nil {
		t.Fatal("wrong key accepted")
	}
	next, err := s.CreateEnrollment(ctx, m.ID, false, now)
	if err != nil {
		t.Fatal(err)
	}
	exchanged, err := s.ExchangeEnrollmentWithCipher(ctx, m.ID, next.Code, now.Add(time.Second), box)
	if err != nil {
		t.Fatal(err)
	}
	read, err = s.MachineToken(ctx, m.ID, box)
	if err != nil || read != exchanged.Token {
		t.Fatal("enrolled token not recoverable")
	}
	if _, err := s.ResetMachineToken(ctx, m.ID+900, box, now); err != ErrNotFound {
		t.Fatal("unknown machine accepted")
	}
}
