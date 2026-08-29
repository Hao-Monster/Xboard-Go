package store

import (
	"context"
	"testing"
)

func BenchmarkGetCommissionSettings(b *testing.B) {
	database, err := OpenSQLite("file:benchmark-commission-settings?mode=memory&cache=shared")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = database.Close() })
	ctx := context.Background()
	if err := database.Migrate(ctx); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := database.GetCommissionSettings(ctx); err != nil {
			b.Fatal(err)
		}
	}
}
