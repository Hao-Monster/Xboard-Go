package httpapi

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func BenchmarkLegacyConfigurationFullFetch(b *testing.B) {
	database, err := store.OpenSQLite("file:" + filepath.ToSlash(filepath.Join(b.TempDir(), "legacy-configuration-full-fetch.db")))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(context.Background()); err != nil {
		b.Fatal(err)
	}
	if err := database.EnsureNodeAgentSettings(b.Context(), store.NodeAgentSettingsDefaults{
		PullInterval: 60, PushInterval: 60,
	}, time.Unix(1_800_000_000, 0)); err != nil {
		b.Fatal(err)
	}
	api := &server{store: database}
	b.ReportAllocs()
	b.ReportMetric(float64(len(legacyConfigGroupNames)), "groups/op")
	b.ResetTimer()
	for b.Loop() {
		groups, err := loadLegacyConfigGroups(b.Context(), legacyConfigGroupNames[:], api.readLegacyConfigGroup)
		if err != nil || len(groups) != len(legacyConfigGroupNames) {
			b.Fatalf("full configuration groups=%d err=%v", len(groups), err)
		}
	}
}
