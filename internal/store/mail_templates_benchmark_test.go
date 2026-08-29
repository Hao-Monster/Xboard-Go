package store

import (
	"context"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/mailtemplate"
)

func BenchmarkGetMailTemplate(b *testing.B) {
	database, err := OpenSQLite("file:benchmark-mail-template?mode=memory&cache=shared")
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
		if _, err := database.GetMailTemplate(ctx, mailtemplate.Notify); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListMailTemplateSummaries(b *testing.B) {
	database, err := OpenSQLite("file:benchmark-mail-template-summaries?mode=memory&cache=shared")
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
		if _, err := database.ListMailTemplateSummaries(ctx); err != nil {
			b.Fatal(err)
		}
	}
}
