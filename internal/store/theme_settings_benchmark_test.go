package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/theme"
)

func BenchmarkGetActiveThemeAppearance(b *testing.B) {
	database := openBenchmarkStore(b, "theme-active.db")
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := database.GetActiveThemeAppearance(b.Context()); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListThemes1000(b *testing.B) {
	database := openBenchmarkStore(b, "theme-list.db")
	var manifest, config string
	if err := database.db.QueryRow(`SELECT manifest_json, config_json FROM themes WHERE name='Xboard'`).Scan(&manifest, &config); err != nil {
		b.Fatal(err)
	}
	tx, err := database.db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	statement, err := tx.Prepare(`INSERT INTO themes(name,description,version,manifest_json,config_json,package_sha256) VALUES(?,?,?,?,?,?)`)
	if err != nil {
		b.Fatal(err)
	}
	for index := 1; index < 1000; index++ {
		if _, err := statement.Exec(fmt.Sprintf("Theme%04d", index), "Benchmark", "1.0.0", manifest, config, fmt.Sprintf("%064x", index)); err != nil {
			b.Fatal(err)
		}
	}
	_ = statement.Close()
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		catalog, err := database.ListThemes(b.Context())
		if err != nil || len(catalog.Themes) != 1000 {
			b.Fatalf("themes=%d err=%v", len(catalog.Themes), err)
		}
	}
}

func BenchmarkGetThemeAsset8MiB(b *testing.B) {
	database := openBenchmarkStore(b, "theme-asset.db")
	administrator, err := database.CreateAdminUser(b.Context(), CreateAdminUserInput{
		Email: "theme-asset-benchmark@example.test", PasswordHash: "hash", IsAdmin: true,
	}, time.Unix(1, 0).UTC())
	if err != nil {
		b.Fatal(err)
	}
	body := bytes.Repeat([]byte{0x5a}, 8<<20)
	digest := sha256.Sum256(body)
	packaged := testThemePackage("AssetBenchmark", "1.0.0", "a")
	packaged.Manifest.Images = []string{"assets/preview.png"}
	packaged.Assets = []theme.Asset{{
		Path: "assets/preview.png", MIME: "image/png", SHA256: hex.EncodeToString(digest[:]),
		Width: 1, Height: 1, Data: body,
	}}
	if _, err := database.InstallTheme(b.Context(), administrator.ID, packaged, time.Unix(2, 0).UTC()); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		asset, err := database.GetThemeAsset(b.Context(), packaged.Manifest.Name, packaged.SHA256, packaged.Manifest.Images[0])
		if err != nil || len(asset.Data) != len(body) {
			b.Fatalf("asset bytes=%d err=%v", len(asset.Data), err)
		}
	}
}
