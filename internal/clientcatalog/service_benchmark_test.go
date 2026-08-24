package clientcatalog

import (
	"context"
	"testing"
	"time"
)

func BenchmarkUserCatalogCachedConfig(b *testing.B) {
	service := New(Options{
		Store:    testStore(b),
		PanelURL: "https://panel.example.test",
		Now:      func() time.Time { return time.Unix(1_777_000_000, 0) },
	})
	if _, err := service.UserCatalog(context.Background()); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := service.UserCatalog(context.Background()); err != nil {
			b.Fatal(err)
		}
	}
}
