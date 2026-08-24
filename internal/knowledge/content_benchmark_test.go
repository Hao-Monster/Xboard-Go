package knowledge

import (
	"strings"
	"testing"
)

func BenchmarkRenderPublicArticle(b *testing.B) {
	article := strings.Repeat("## Client setup\n\nUse **Xboard-Go** with [the guide](https://docs.example.test).\n\n", 100)
	b.ReportAllocs()
	for range b.N {
		document, err := RenderPublic(article)
		if err != nil || len(document.TOC) != 100 {
			b.Fatalf("RenderPublic() toc=%d err=%v", len(document.TOC), err)
		}
	}
}
