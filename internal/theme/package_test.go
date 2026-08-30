package theme

import (
	"archive/zip"
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
	"testing"
)

func TestParseArchiveAcceptsBoundedDeclarativeTheme(t *testing.T) {
	archive := themeArchive(t, validManifest("Aurora", "1.2.3"), map[string][]byte{
		"assets/preview.png": testPNG(t),
	})
	parsed, err := ParseArchive(archive)
	if err != nil {
		t.Fatalf("ParseArchive() error=%v", err)
	}
	if parsed.Manifest.Name != "Aurora" || parsed.Manifest.Version != "1.2.3" || len(parsed.Assets) != 1 {
		t.Fatalf("parsed package=%#v", parsed)
	}
	if parsed.Assets[0].Path != "assets/preview.png" || parsed.Assets[0].MIME != "image/png" ||
		parsed.Assets[0].Width != 2 || parsed.Assets[0].Height != 2 || len(parsed.SHA256) != 64 {
		t.Fatalf("parsed asset=%#v package digest=%q", parsed.Assets[0], parsed.SHA256)
	}
}

func TestParseArchiveRejectsExecutableAndAmbiguousArchives(t *testing.T) {
	tests := map[string][]byte{
		"zip slip": themeArchive(t, validManifest("Aurora", "1.0.0"), map[string][]byte{
			"../escape.png": testPNG(t),
		}),
		"unreferenced payload": themeArchive(t, validManifest("Aurora", "1.0.0"), map[string][]byte{
			"assets/preview.png": testPNG(t), "assets/payload.js": []byte("alert(1)"),
		}),
		"case collision": archiveWithEntries(t, []archiveEntry{
			{name: "manifest.json", body: []byte(validManifest("Aurora", "1.0.0"))},
			{name: "assets/preview.png", body: testPNG(t)},
			{name: "ASSETS/PREVIEW.PNG", body: testPNG(t)},
		}),
		"unknown manifest field": themeArchive(t, strings.Replace(validManifest("Aurora", "1.0.0"), `"format_version":1`, `"format_version":1,"custom_html":"<script>"`, 1), map[string][]byte{
			"assets/preview.png": testPNG(t),
		}),
		"low contrast": themeArchive(t, strings.Replace(validManifest("Aurora", "1.0.0"), `"text":"#f4f4f5"`, `"text":"#111111"`, 1), map[string][]byte{
			"assets/preview.png": testPNG(t),
		}),
		"misleading extension": themeArchive(t, strings.Replace(validManifest("Aurora", "1.0.0"), "assets/preview.png", "assets/preview.js", 1), map[string][]byte{
			"assets/preview.js": testPNG(t),
		}),
	}
	for name, archive := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseArchive(archive); err == nil {
				t.Fatal("ParseArchive() unexpectedly accepted unsafe archive")
			}
		})
	}
}

func TestParseArchiveRejectsInvalidVersionAndOversizedCompressedInput(t *testing.T) {
	archive := themeArchive(t, validManifest("Aurora", "1.0"), map[string][]byte{"assets/preview.png": testPNG(t)})
	if _, err := ParseArchive(archive); err == nil {
		t.Fatal("ParseArchive() accepted invalid semantic version")
	}
	archive = themeArchive(t, validManifest("Aurora", "2147483648.0.0"), map[string][]byte{"assets/preview.png": testPNG(t)})
	if _, err := ParseArchive(archive); err == nil {
		t.Fatal("ParseArchive() accepted a semantic version that cannot be compared during upgrades")
	}
	if _, err := ParseArchive(make([]byte, MaxArchiveBytes+1)); err == nil {
		t.Fatal("ParseArchive() accepted oversized input")
	}
}

func TestParseArchiveAcceptsExplicitSafeDirectoryEntries(t *testing.T) {
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	directory := &zip.FileHeader{Name: "assets/", Method: zip.Store}
	directory.SetMode(os.ModeDir | 0o755)
	if _, err := writer.CreateHeader(directory); err != nil {
		t.Fatal(err)
	}
	manifest, _ := writer.Create("manifest.json")
	_, _ = manifest.Write([]byte(validManifest("DirectoryTheme", "1.0.0")))
	preview, _ := writer.Create("assets/preview.png")
	_, _ = preview.Write(testPNG(t))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseArchive(output.Bytes()); err != nil {
		t.Fatalf("ParseArchive() rejected a safe explicit directory: %v", err)
	}
}

func TestParseArchiveRejectsSymlinkCompressionBombAndEntryFlood(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		var output bytes.Buffer
		writer := zip.NewWriter(&output)
		manifest, _ := writer.Create("manifest.json")
		_, _ = manifest.Write([]byte(validManifest("Symlink", "1.0.0")))
		header := &zip.FileHeader{Name: "assets/preview.png", Method: zip.Store}
		header.SetMode(os.ModeSymlink | 0o777)
		asset, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = asset.Write([]byte("../outside.png"))
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := ParseArchive(output.Bytes()); err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("ParseArchive(symlink) error=%v", err)
		}
	})

	t.Run("compression bomb", func(t *testing.T) {
		archive := themeArchive(t, validManifest("CompressionBomb", "1.0.0"), map[string][]byte{
			"assets/preview.png": make([]byte, 256<<10),
		})
		if _, err := ParseArchive(archive); err == nil || !strings.Contains(err.Error(), "compression ratio") {
			t.Fatalf("ParseArchive(compression bomb) error=%v", err)
		}
	})

	t.Run("entry flood", func(t *testing.T) {
		var output bytes.Buffer
		writer := zip.NewWriter(&output)
		for index := 0; index <= maxFiles; index++ {
			header := &zip.FileHeader{Name: fmt.Sprintf("directory-%02d/", index), Method: zip.Store}
			header.SetMode(os.ModeDir | 0o755)
			if _, err := writer.CreateHeader(header); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := ParseArchive(output.Bytes()); err == nil || !strings.Contains(err.Error(), "invalid number of entries") {
			t.Fatalf("ParseArchive(entry flood) error=%v", err)
		}
	})
}

func FuzzParseArchiveNeverPanics(f *testing.F) {
	f.Add([]byte("not a zip"))
	f.Add(themeArchive(f, validManifest("FuzzSeed", "1.0.0"), map[string][]byte{"assets/preview.png": testPNG(f)}))
	f.Fuzz(func(t *testing.T, archive []byte) {
		_, _ = ParseArchive(archive)
	})
}

type archiveEntry struct {
	name string
	body []byte
}

func themeArchive(t testing.TB, manifest string, files map[string][]byte) []byte {
	t.Helper()
	entries := []archiveEntry{{name: "manifest.json", body: []byte(manifest)}}
	for name, body := range files {
		entries = append(entries, archiveEntry{name: name, body: body})
	}
	return archiveWithEntries(t, entries)
}

func archiveWithEntries(t testing.TB, entries []archiveEntry) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for _, entry := range entries {
		file, err := writer.Create(entry.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(entry.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func testPNG(t testing.TB) []byte {
	t.Helper()
	var output bytes.Buffer
	canvas := image.NewRGBA(image.Rect(0, 0, 2, 2))
	canvas.Set(0, 0, color.White)
	if err := png.Encode(&output, canvas); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func validManifest(name, version string) string {
	return `{
		"format_version":1,
		"name":"` + name + `",
		"description":"Safe theme",
		"version":"` + version + `",
		"images":["assets/preview.png"],
		"backgrounds":[],
		"palettes":{"default":{"background":"#111111","surface":"#18181b","text":"#f4f4f5","muted":"#a1a1aa","primary":"#a5b4fc","primary_text":"#111111","border":"#3f3f46"}},
		"default_config":{"theme_color":"default","background_url":"","font_scale":"normal","radius":"rounded"}
	}`
}
