package bulkops

import (
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestUserLifecycleCancelledBulkMailNeverReachesSender(t *testing.T) {
	db, admin, target, cipherBox, now := newBulkServiceFixture(t)
	_, err := db.CreateAdminUserBulkJob(t.Context(), store.CreateAdminUserBulkJobInput{Kind: store.AdminUserBulkKindMail, AdministratorID: admin.ID, Subject: "Personal notice", Content: "Private content", Scope: store.AdminUserBulkScope{Scope: store.AdminUserBulkScopeSelected, UserIDs: []int64{target.ID}}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = db.ChangeUserLifecycle(t.Context(), store.UserLifecycleInput{AdministratorID: admin.ID, UserID: target.ID, Revision: target.Revision, Action: "deactivate"}, now); err != nil {
		t.Fatal(err)
	}
	sender := &captureSender{}
	service, err := New(db, Options{Cipher: cipherBox, Sender: sender, ExportRoot: t.TempDir(), PanelURL: "https://panel.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if worked, err := service.RunMailOnce(t.Context(), now); err != nil || worked {
		t.Fatalf("cancelled job executed worked=%t err=%v", worked, err)
	}
	if messages := sender.Messages(); len(messages) != 0 {
		t.Fatalf("cancelled user received %d messages", len(messages))
	}
}
