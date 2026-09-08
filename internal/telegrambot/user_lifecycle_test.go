package telegrambot

import (
	"github.com/Hao-Monster/Xboard-Go/internal/store"
	"testing"
	"time"
)

func TestUserLifecycleCancelledTelegramSnapshotNeverReachesTransport(t *testing.T) {
	db, cipherBox, now := telegramWorkerStore(t)
	target, err := db.CreateAdminUser(t.Context(), store.CreateAdminUserInput{Email: "private-telegram@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	chat := int64(778899)
	target, _, err = db.UpdateAdminUser(t.Context(), target.ID, store.UpdateAdminUserInput{Revision: target.Revision, Email: target.Email, TelegramIDSet: true, TelegramID: &chat}, now)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, claimed, err := db.ClaimTelegramMessage(t.Context(), "old-telegram-claim", now, time.Minute)
	if err != nil || !claimed {
		t.Fatalf("claim %t %v", claimed, err)
	}
	if _, _, err = db.ChangeUserLifecycle(t.Context(), store.UserLifecycleInput{AdministratorID: 1, UserID: target.ID, Revision: target.Revision, Action: "deactivate"}, now); err != nil {
		t.Fatal(err)
	}
	sender := &recordingMessageSender{}
	worker := NewWorker(db, cipherBox, sender, time.Second, nil)
	_, err = worker.deliverClaimed(t.Context(), snapshot, "old-telegram-claim", now)
	if sender.calls != 0 {
		t.Fatalf("cancelled Telegram snapshot reached transport %d times; err=%v", sender.calls, err)
	}
	if err != nil {
		t.Fatalf("discard cancelled snapshot: %v", err)
	}
}
