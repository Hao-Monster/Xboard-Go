package attachments

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestCloneForDraftCreatesVerifiedIndependentObjects(t *testing.T) {
	service, _, adminID, now := newAttachmentTestService(t, 1<<20)
	sourceDraft := testDraftToken("f")
	content := []byte("clone-source")
	source := uploadTestAttachment(t, service, adminID, sourceDraft, "资料.txt", content, now)
	article, err := service.CreateKnowledge(context.Background(), adminID, sourceDraft, store.SaveKnowledgeInput{
		Language: "zh-CN", Category: "clone", Title: "source",
		Body: fmt.Sprintf("[source](knowledge-attachment://%s)", source.UUID), Visible: true,
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	clones, err := service.CloneForDraft(context.Background(), adminID, article.ID, []string{source.UUID}, testDraftToken("1"), now.Add(2*time.Minute))
	if err != nil || len(clones) != 1 {
		t.Fatalf("CloneForDraft() clones=%#v error=%v", clones, err)
	}
	clone := clones[0]
	if clone.SourceUUID != source.UUID || clone.Attachment.UUID == source.UUID || clone.Attachment.StoragePath == source.StoragePath || clone.Attachment.KnowledgeID != nil {
		t.Fatalf("clone is not independent: %#v", clone)
	}
	reader, _, err := service.Open(context.Background(), clone.Attachment.UUID)
	if err != nil {
		t.Fatal(err)
	}
	clonedContent, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || string(clonedContent) != string(content) {
		t.Fatalf("cloned content=%q error=%v", clonedContent, err)
	}
}

func TestCloneRejectsQuotaBeforeCopyingAnyObject(t *testing.T) {
	service, _, adminID, now := newAttachmentTestService(t, 12)
	sourceToken := testDraftToken("a")
	source := uploadTestAttachment(t, service, adminID, sourceToken, "source.bin", []byte("12345678"), now)
	article, err := service.CreateKnowledge(context.Background(), adminID, sourceToken, store.SaveKnowledgeInput{
		Language: "zh-CN", Category: "clone", Title: "source", Body: fmt.Sprintf("[source](knowledge-attachment://%s)", source.UUID), Visible: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	targetToken := testDraftToken("b")
	if _, err := service.CloneForDraft(context.Background(), adminID, article.ID, []string{source.UUID}, targetToken, now); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("CloneForDraft() error=%v, want ErrQuotaExceeded", err)
	}
	page, err := service.List(context.Background(), adminID, nil, targetToken, 1, 100)
	if err != nil || page.Total != 0 {
		t.Fatalf("target draft page=%#v error=%v", page, err)
	}
	files := 0
	if err := filepath.WalkDir(filepath.Join(service.root, "files"), func(_ string, entry os.DirEntry, walkErr error) error {
		if walkErr == nil && !entry.IsDir() {
			files++
		}
		return walkErr
	}); err != nil {
		t.Fatal(err)
	}
	if files != 1 {
		t.Fatalf("stored file count=%d, want only the source object", files)
	}
}
