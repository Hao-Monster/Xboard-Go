package geoip

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/lionsoul2014/ip2region/binding/golang/xdb"
)

const (
	// LegacyXDBSize and LegacyXDBSHA256 pin the exact XDB distributed by
	// zoujingli/ip2region v2.0.8 in the captured Xboard dependency lock.
	LegacyXDBSize   = 11_042_429
	LegacyXDBSHA256 = "5555fd1aab63e06096d9ee2a4187e93b8451e650b77ce4138a26eb9cf4d81469"
	maxRegionBytes  = 512
	maxSearchers    = 32
)

type Resolver struct {
	searchers chan *xdb.Searcher
}

func OpenLegacy(path string) (*Resolver, error) {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) {
		return nil, errors.New("Ip2Region XDB path must be absolute")
	}
	metadata, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect Ip2Region XDB: %w", err)
	}
	if !metadata.Mode().IsRegular() {
		return nil, errors.New("Ip2Region XDB must be a regular file")
	}
	if metadata.Size() != LegacyXDBSize {
		return nil, fmt.Errorf("Ip2Region XDB size is %d, want %d", metadata.Size(), LegacyXDBSize)
	}

	handle, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open Ip2Region XDB: %w", err)
	}
	defer handle.Close()
	content, err := io.ReadAll(io.LimitReader(handle, LegacyXDBSize+1))
	if err != nil {
		return nil, fmt.Errorf("read Ip2Region XDB: %w", err)
	}
	if len(content) != LegacyXDBSize {
		return nil, fmt.Errorf("Ip2Region XDB size is %d, want %d", len(content), LegacyXDBSize)
	}
	digest := sha256.Sum256(content)
	if !bytes.Equal(digest[:], mustLegacyXDBDigest()) {
		return nil, errors.New("Ip2Region XDB checksum does not match the pinned legacy data")
	}
	if _, err := handle.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("rewind Ip2Region XDB: %w", err)
	}
	if err := xdb.Verify(handle); err != nil {
		return nil, fmt.Errorf("verify Ip2Region XDB structure: %w", err)
	}

	searcherCount := min(max(runtime.GOMAXPROCS(0), 1), maxSearchers)
	resolver := &Resolver{searchers: make(chan *xdb.Searcher, searcherCount)}
	for range searcherCount {
		searcher, err := xdb.NewWithBuffer(xdb.IPv4, content)
		if err != nil {
			return nil, fmt.Errorf("initialize Ip2Region searcher: %w", err)
		}
		resolver.searchers <- searcher
	}
	return resolver, nil
}

func (resolver *Resolver) Region(ip string) (string, error) {
	address, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil || !address.Is4() || resolver == nil || resolver.searchers == nil {
		return "", errors.New("Ip2Region query requires an IPv4 address and initialized resolver")
	}
	searcher := <-resolver.searchers
	defer func() { resolver.searchers <- searcher }()
	raw, err := searcher.Search(address.String())
	if err != nil {
		return "", fmt.Errorf("query Ip2Region XDB: %w", err)
	}
	if raw == "" || !utf8.ValidString(raw) || len(raw) > maxRegionBytes || strings.IndexByte(raw, 0) >= 0 {
		return "", errors.New("Ip2Region XDB returned an invalid region")
	}
	region := formatLegacyRegion(raw)
	if region == "" || !utf8.ValidString(region) || len(region) > maxRegionBytes || strings.IndexByte(region, 0) >= 0 {
		return "", errors.New("Ip2Region XDB returned an invalid formatted region")
	}
	return region, nil
}

func formatLegacyRegion(region string) string {
	parts := strings.Split(strings.ReplaceAll(region, "0|", "|"), "|")
	last := parts[len(parts)-1]
	parts = parts[:len(parts)-1]
	// PHP's empty("0") is true, so the pinned simple() formatter suppresses
	// both the database placeholder and the special internal-network suffix.
	if last == "0" || last == "内网IP" {
		last = ""
	}
	result := strings.Join(parts, "")
	if last != "" {
		result += "【" + last + "】"
	}
	return result
}

func mustLegacyXDBDigest() []byte {
	return []byte{
		0x55, 0x55, 0xfd, 0x1a, 0xab, 0x63, 0xe0, 0x60,
		0x96, 0xd9, 0xee, 0x2a, 0x41, 0x87, 0xe9, 0x3b,
		0x84, 0x51, 0xe6, 0x50, 0xb7, 0x7c, 0xe4, 0x13,
		0x8a, 0x26, 0xeb, 0x9c, 0xf4, 0xd8, 0x14, 0x69,
	}
}
