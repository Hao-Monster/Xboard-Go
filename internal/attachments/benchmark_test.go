package attachments

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func BenchmarkRenderKnowledgeBody100Attachments(b *testing.B) {
	service, _, adminID, now := newAttachmentTestService(b, 1<<20)
	draftToken := testDraftToken("7")
	var body strings.Builder
	for index := 0; index < 100; index++ {
		attachment := uploadTestAttachment(b, service, adminID, draftToken, fmt.Sprintf("attachment-%03d.txt", index), []byte{'a' + byte(index%26)}, now)
		fmt.Fprintf(&body, "[%d](knowledge-attachment://%s)\n", index, attachment.UUID)
	}
	article, err := service.CreateKnowledge(context.Background(), adminID, draftToken, store.SaveKnowledgeInput{
		Language: "en-US", Category: "General", Title: "One hundred attachments", Body: body.String(), Visible: true,
	}, now)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		rendered, err := service.RenderKnowledgeBody(context.Background(), article.ID, article.Body, false, now)
		if err != nil || strings.Contains(rendered, knowledgeAttachmentScheme) || strings.Count(rendered, "/knowledge-attachments/") != 100 {
			b.Fatalf("rendered attachment body is incomplete: error=%v", err)
		}
	}
	b.ReportMetric(100, "attachments/op")
}
