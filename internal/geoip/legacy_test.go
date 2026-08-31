package geoip

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestFormatLegacyRegionMatchesPinnedPHPBehavior(t *testing.T) {
	for _, test := range []struct {
		raw  string
		want string
	}{
		{raw: "0|0|内网IP|内网IP", want: "内网IP"},
		{raw: "美国|0|0|Level3", want: "美国【Level3】"},
		{raw: "澳大利亚|0|0|0", want: "澳大利亚"},
		{raw: "中国|江苏省|南京市|0", want: "中国江苏省南京市"},
	} {
		if got := formatLegacyRegion(test.raw); got != test.want {
			t.Errorf("formatLegacyRegion(%q) = %q, want %q", test.raw, got, test.want)
		}
	}
}

func TestOpenLegacyRejectsUnpinnedOrUnsafeFiles(t *testing.T) {
	if _, err := OpenLegacy("relative.xdb"); err == nil {
		t.Fatal("OpenLegacy() accepted a relative path")
	}
	directory := t.TempDir()
	truncated := filepath.Join(directory, "truncated.xdb")
	if err := os.WriteFile(truncated, []byte("not an xdb"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenLegacy(truncated); err == nil || !strings.Contains(err.Error(), "size") {
		t.Fatalf("OpenLegacy(truncated) error = %v, want size rejection", err)
	}

	target := filepath.Join(directory, "target.xdb")
	if err := os.WriteFile(target, make([]byte, LegacyXDBSize), 0o600); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(directory, "symlink.xdb")
	if err := os.Symlink(target, symlink); err == nil {
		if _, err := OpenLegacy(symlink); err == nil || !strings.Contains(err.Error(), "regular") {
			t.Fatalf("OpenLegacy(symlink) error = %v, want regular-file rejection", err)
		}
	}
	if _, err := OpenLegacy(target); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("OpenLegacy(wrong checksum) error = %v, want checksum rejection", err)
	}
}

func TestOpenLegacyReadsPinnedFixtureWhenAvailable(t *testing.T) {
	path := strings.TrimSpace(os.Getenv("XBOARD_TEST_LEGACY_IP2REGION_XDB"))
	if path == "" {
		t.Skip("pinned 11 MB legacy XDB is exercised by packaged-image CI")
	}
	resolver, err := OpenLegacy(path)
	if err != nil {
		t.Fatal(err)
	}
	for ip, want := range map[string]string{
		"127.0.0.1":       "内网IP",
		"172.18.0.1":      "内网IP",
		"8.8.8.8":         "美国【Level3】",
		"1.1.1.1":         "澳大利亚",
		"114.114.114.114": "中国江苏省南京市",
	} {
		got, err := resolver.Region(ip)
		if err != nil || got != want {
			t.Errorf("Region(%s) = (%q, %v), want %q", ip, got, err, want)
		}
	}
	if _, err := resolver.Region("::ffff:8.8.8.8"); err == nil {
		t.Fatal("Region() accepted an IPv4-mapped IPv6 address")
	}

	const workers = 128
	errorsChannel := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if got, err := resolver.Region("8.8.8.8"); err != nil || got != "美国【Level3】" {
				errorsChannel <- fmt.Errorf("parallel Region() = (%q, %v)", got, err)
			}
		}()
	}
	wait.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		t.Error(err)
	}
}
