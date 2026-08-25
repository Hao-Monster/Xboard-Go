package store

import "testing"

func BenchmarkSubscriptionRenderConfigProjection(b *testing.B) {
	database := openBenchmarkStore(b, "subscription-render-config.db")
	ctx := b.Context()
	b.Run("single-projection", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := database.GetSubscriptionRenderConfig(ctx, "clash"); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("full-settings-and-site", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := database.GetSubscriptionSettings(ctx); err != nil {
				b.Fatal(err)
			}
			if _, err := database.GetSiteSettings(ctx); err != nil {
				b.Fatal(err)
			}
		}
	})
}
