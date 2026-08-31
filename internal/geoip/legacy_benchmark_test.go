package geoip

import (
	"os"
	"strings"
	"testing"
)

func BenchmarkLegacyRegion(b *testing.B) {
	path := strings.TrimSpace(os.Getenv("XBOARD_TEST_LEGACY_IP2REGION_XDB"))
	if path == "" {
		b.Skip("set XBOARD_TEST_LEGACY_IP2REGION_XDB to the pinned legacy fixture")
	}
	resolver, err := OpenLegacy(path)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := resolver.Region("114.114.114.114"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLegacyRegionParallel(b *testing.B) {
	path := strings.TrimSpace(os.Getenv("XBOARD_TEST_LEGACY_IP2REGION_XDB"))
	if path == "" {
		b.Skip("set XBOARD_TEST_LEGACY_IP2REGION_XDB to the pinned legacy fixture")
	}
	resolver, err := OpenLegacy(path)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(parallel *testing.PB) {
		for parallel.Next() {
			if _, err := resolver.Region("114.114.114.114"); err != nil {
				b.Error(err)
				return
			}
		}
	})
}
