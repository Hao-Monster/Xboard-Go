package store

import (
	"context"
	"testing"
	"time"
)

func BenchmarkRegistrationEmailPolicy(b *testing.B) {
	settings := SiteSettings{
		EmailWhitelistEnabled:  true,
		EmailWhitelistSuffixes: []string{"gmail.com", "qq.com", "163.com", "yahoo.com", "sina.com", "126.com", "outlook.com", "yeah.net", "foxmail.com"},
		GmailAliasLimitEnabled: true,
	}
	b.ReportAllocs()
	for b.Loop() {
		if err := CheckRegistrationEmailPolicy(settings, "BENCHMARK@OUTLOOK.COM"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRegistrationIPLimitPrecheck(b *testing.B) {
	database, _ := newSiteSettingsBenchmarkStore(b)
	settings, err := database.GetSiteSettings(context.Background())
	if err != nil {
		b.Fatal(err)
	}
	settings.RegistrationIPLimitEnabled = true
	settings.RegistrationIPLimitCount = 3
	settings.RegistrationIPLimitMinutes = 60
	now := time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := database.CheckRegistrationIPLimit(context.Background(), settings, "192.0.2.100", now); err != nil {
			b.Fatal(err)
		}
	}
}
