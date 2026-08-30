package theme

import "testing"

func BenchmarkParseArchive(b *testing.B) {
	archive := themeArchive(b, validManifest("Benchmark", "1.0.0"), map[string][]byte{"assets/preview.png": testPNG(b)})
	b.ReportAllocs()
	b.SetBytes(int64(len(archive)))
	b.ResetTimer()
	for range b.N {
		if _, err := ParseArchive(archive); err != nil {
			b.Fatal(err)
		}
	}
}
