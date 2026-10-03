package store

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPurchaseChannelsPersistenceCASAndMigration(t *testing.T) {
	db := newTestStore(t)
	ctx := t.Context()
	admin, err := db.CreateAdminUser(ctx, CreateAdminUserInput{Email: "channels@example.test", PasswordHash: "hash", IsAdmin: true}, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	initial, err := db.GetPurchaseChannels(ctx)
	if err != nil || initial != (PurchaseChannels{Revision: 1}) {
		t.Fatalf("initial=%+v error=%v", initial, err)
	}
	input := PurchaseChannels{Revision: 1, CardStoreURL: " https://cards.example.test/product?sku=1 ", ChatwootBaseURL: "https://chat.example.test/", ChatwootWebsiteToken: "public-widget_123"}
	updated, err := db.UpdatePurchaseChannels(ctx, admin.ID, input, time.Unix(2, 0))
	if err != nil || updated.Revision != 2 || updated.CardStoreURL != strings.TrimSpace(input.CardStoreURL) || updated.ChatwootBaseURL != "https://chat.example.test" {
		t.Fatalf("updated=%+v error=%v", updated, err)
	}
	if _, err := db.UpdatePurchaseChannels(ctx, admin.ID, input, time.Unix(3, 0)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update error=%v", err)
	}
	persisted, err := db.GetPurchaseChannels(ctx)
	if err != nil || persisted != updated {
		t.Fatalf("persisted=%+v error=%v", persisted, err)
	}
	// Reapplying migrations must preserve already configured values.
	if _, err := db.db.ExecContext(ctx, `PRAGMA user_version = 66`); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	persisted, err = db.GetPurchaseChannels(ctx)
	if err != nil || persisted != updated {
		t.Fatalf("replayed=%+v error=%v", persisted, err)
	}
	// Upgrade an actual v66 shape and preserve unrelated application settings.
	if _, err := db.db.ExecContext(ctx, `DROP TABLE purchase_channels; UPDATE app_settings SET app_name = 'Preserved'; PRAGMA user_version = 66`); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	migrated, err := db.GetPurchaseChannels(ctx)
	if err != nil || migrated != initial {
		t.Fatalf("migrated=%+v error=%v", migrated, err)
	}
	var version int
	var name string
	if err := db.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := db.db.QueryRowContext(ctx, `SELECT app_name FROM app_settings WHERE id=1`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if version != CurrentSchemaVersion() || name != "Preserved" {
		t.Fatalf("version=%d name=%s", version, name)
	}
	cleared, err := db.UpdatePurchaseChannels(ctx, admin.ID, PurchaseChannels{Revision: 1}, time.Unix(4, 0))
	if err != nil || cleared != (PurchaseChannels{Revision: 2}) {
		t.Fatalf("clear=%+v err=%v", cleared, err)
	}
}

func TestPurchaseChannelsRejectInvalidConfiguration(t *testing.T) {
	db := newTestStore(t)
	admin, err := db.CreateAdminUser(t.Context(), CreateAdminUserInput{Email: "invalid-channels@example.test", PasswordHash: "hash", IsAdmin: true}, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	invalid := []PurchaseChannels{
		{Revision: 0},
		{Revision: 1, ChatwootBaseURL: "/"},
		{Revision: 1, ChatwootBaseURL: "https://chat.example.test"},
		{Revision: 1, ChatwootWebsiteToken: "widget"},
	}
	for _, address := range []string{"http://cards.example.test", "javascript:alert(1)", "//cards.example.test", "https://user:pass@cards.example.test", "https://cards.example.test/#", "https://cards.example.test/a\\b", "https://cards.example.test:99999", "https://cards.example.test/with space", "https://cards.example.test/" + strings.Repeat("a", 2048)} {
		invalid = append(invalid, PurchaseChannels{Revision: 1, CardStoreURL: address})
	}
	for _, address := range []string{"https://chat.example.test//", "https://chat.example.test/path", "https://chat.example.test?q=1", "https://chat.example.test?", "https://chat.example.test#x"} {
		invalid = append(invalid, PurchaseChannels{Revision: 1, ChatwootBaseURL: address, ChatwootWebsiteToken: "public-widget"})
	}
	for _, token := range []string{"invalid token", "token/script", strings.Repeat("a", 129)} {
		invalid = append(invalid, PurchaseChannels{Revision: 1, ChatwootBaseURL: "https://chat.example.test", ChatwootWebsiteToken: token})
	}
	for i, input := range invalid {
		if _, err := db.UpdatePurchaseChannels(t.Context(), admin.ID, input, time.Unix(2, 0)); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("case %d error=%v", i, err)
		}
	}
	current, err := db.GetPurchaseChannels(t.Context())
	if err != nil || current != (PurchaseChannels{Revision: 1}) {
		t.Fatalf("invalid write changed state: %+v %v", current, err)
	}
}
