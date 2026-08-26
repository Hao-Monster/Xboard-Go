package attachments

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestKnowledgeBindingsAreStrictAtomicAndRestorable(t *testing.T) {
	service, database, adminID, now := newAttachmentTestService(t, 1<<20)
	draftToken := testDraftToken("c")
	attachment := uploadTestAttachment(t, service, adminID, draftToken, "说明.txt", []byte("bound content"), now)
	body := fmt.Sprintf("[下载](knowledge-attachment://%s)", attachment.UUID)
	article, err := service.CreateKnowledge(context.Background(), adminID, draftToken, store.SaveKnowledgeInput{
		Language: "zh-CN", Category: "使用", Title: "附件", Body: body, Visible: true,
	}, now.Add(time.Minute))
	if err != nil || article.Body != body {
		t.Fatalf("CreateKnowledge() article=%#v error=%v", article, err)
	}
	bound, err := database.GetReadyKnowledgeAttachment(context.Background(), attachment.UUID)
	if err != nil || bound.KnowledgeID == nil || *bound.KnowledgeID != article.ID || bound.DraftTokenHash != nil {
		t.Fatalf("bound attachment=%#v error=%v", bound, err)
	}

	updated, err := service.UpdateKnowledge(context.Background(), adminID, "", article.ID, article.Revision, store.SaveKnowledgeInput{
		Language: article.Language, Category: article.Category, Title: article.Title, Body: "附件已移除", Visible: true,
	}, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.GetReadyKnowledgeAttachment(context.Background(), attachment.UUID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("removed attachment error=%v, want not found", err)
	}
	updated, err = service.UpdateKnowledge(context.Background(), adminID, "", article.ID, updated.Revision, store.SaveKnowledgeInput{
		Language: article.Language, Category: article.Category, Title: article.Title, Body: body, Visible: true,
	}, now.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("restore attachment: %v", err)
	}
	if _, err := database.GetReadyKnowledgeAttachment(context.Background(), attachment.UUID); err != nil {
		t.Fatalf("restored attachment: %v", err)
	}

	other, err := database.CreateAdminUser(context.Background(), store.CreateAdminUserInput{
		Email: "other-attachment-admin@example.test", PasswordHash: "hash", IsAdmin: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateKnowledge(context.Background(), other.ID, testDraftToken("d"), store.SaveKnowledgeInput{
		Language: "zh-CN", Category: "越权", Title: "越权", Body: body, Visible: true,
	}, now.Add(4*time.Minute)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("cross-article binding error=%v, want ErrInvalidInput", err)
	}
	items, err := database.ListKnowledge(context.Background())
	if err != nil || len(items) != 1 {
		t.Fatalf("rejected binding must roll back article: count=%d error=%v", len(items), err)
	}
}

func TestKnowledgeReferenceParserRejectsMalformedScheme(t *testing.T) {
	valid := "A Knowledge-Attachment://550E8400-E29B-41D4-A716-446655440000 B"
	normalized, references, err := ParseKnowledgeReferences(valid, 100)
	if err != nil || normalized != "A knowledge-attachment://550e8400-e29b-41d4-a716-446655440000 B" || len(references) != 1 {
		t.Fatalf("ParseKnowledgeReferences(valid) normalized=%q refs=%v error=%v", normalized, references, err)
	}
	for _, body := range []string{
		"knowledge-attachment://", "knowledge-attachment://not-a-uuid",
		"knowledge-attachment://550e8400-e29b-41d4-a716-446655440000extra",
		"knowledge-attachment://550e8400-e29b-41d4-a716-44665544000",
		"knowledge-attachment://550e8400-e29b-01d4-a716-446655440000",
		"knowledge-attachment://550e8400-e29b-41d4-4716-446655440000",
		"knowledge-attachment://550e8400-e29b-41d4-a716-446655440000,",
	} {
		if _, _, err := ParseKnowledgeReferences(body, 100); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("ParseKnowledgeReferences(%q) error=%v", body, err)
		}
	}
}

func TestKnowledgeReferenceParserRepairsLegacyNestedLinks(t *testing.T) {
	const identifier = "550e8400-e29b-41d4-a716-446655440000"
	normalized, references, err := ParseKnowledgeReferences(`[download]([download](knowledge-attachment://`+identifier+`))`, 100)
	if err != nil {
		t.Fatal(err)
	}
	if normalized != `[download](knowledge-attachment://`+identifier+`)` || len(references) != 1 || references[0] != identifier {
		t.Fatalf("normalized=%q references=%#v", normalized, references)
	}
}

func TestKnowledgeSaveBlocksActiveUploadAndCannotRestoreExpiredDraft(t *testing.T) {
	service, _, adminID, now := newAttachmentTestService(t, 1<<20)
	activeToken := testDraftToken("9")
	if _, err := service.Initialize(context.Background(), adminID, InitializeInput{
		OriginalName: "active.bin", Size: 4, DraftToken: activeToken,
	}, now); err != nil {
		t.Fatal(err)
	}
	input := store.SaveKnowledgeInput{Language: "zh-CN", Category: "state", Title: "state", Body: "no attachment", Visible: true}
	if _, err := service.CreateKnowledge(context.Background(), adminID, activeToken, input, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("CreateKnowledge(active upload) error=%v, want ErrConflict", err)
	}
	if _, err := service.CreateKnowledge(context.Background(), adminID, activeToken, input, now.Add(25*time.Hour)); err != nil {
		t.Fatalf("CreateKnowledge(expired upload) error=%v", err)
	}

	expiredToken := testDraftToken("0")
	draft := uploadTestAttachment(t, service, adminID, expiredToken, "expired.txt", []byte("expired"), now)
	if _, err := service.Cleanup(context.Background(), now.Add(25*time.Hour), 100); err != nil {
		t.Fatal(err)
	}
	input.Title = "expired draft"
	input.Body = fmt.Sprintf("[expired](knowledge-attachment://%s)", draft.UUID)
	if _, err := service.CreateKnowledge(context.Background(), adminID, expiredToken, input, now.Add(25*time.Hour)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("CreateKnowledge(expired draft) error=%v, want ErrInvalidInput", err)
	}
}

func uploadTestAttachment(t testing.TB, service *Service, adminID int64, draftToken, name string, content []byte, now time.Time) Attachment {
	t.Helper()
	upload, err := service.Initialize(context.Background(), adminID, InitializeInput{
		OriginalName: name, Size: int64(len(content)), DraftToken: draftToken, SHA256: hexSHA256(content),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < upload.TotalChunks; index++ {
		start := index * int(upload.ChunkSize)
		end := min(start+int(upload.ChunkSize), len(content))
		chunk := content[start:end]
		if _, err := service.StoreChunk(context.Background(), adminID, upload.UUID, index, hexSHA256(chunk), bytes.NewReader(chunk), int64(len(chunk)), now); err != nil {
			t.Fatal(err)
		}
	}
	attachment, err := service.Complete(context.Background(), adminID, upload.UUID, now)
	if err != nil {
		t.Fatal(err)
	}
	return attachment
}
