package store

import "testing"

func BenchmarkLegacyConfigurationProjections(b *testing.B) {
	database := openBenchmarkStore(b, "legacy-configuration-projections.db")
	ctx := b.Context()

	b.Run("invitation", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := database.GetLegacyInvitationSettings(ctx); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("site", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := database.GetLegacySiteConfig(ctx); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("frontend", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := database.GetLegacyFrontendSettings(ctx); err != nil {
				b.Fatal(err)
			}
		}
	})
}
