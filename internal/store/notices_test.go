package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNoticeLifecyclePreservesVisibilityOrderAndRevision(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)

	hidden, err := database.CreateNotice(ctx, SaveNoticeInput{
		Title: " Hidden maintenance ", Content: "maintenance details", Tags: []string{"ops"},
	}, now)
	if err != nil {
		t.Fatalf("CreateNotice(hidden) error = %v", err)
	}
	first, err := database.CreateNotice(ctx, SaveNoticeInput{
		Title: "First visible", Content: "**first**", ImageURL: "https://cdn.example.test/first.png",
		Tags: []string{" news ", "news", "service"}, Visible: true,
	}, now.Add(time.Second))
	if err != nil {
		t.Fatalf("CreateNotice(first) error = %v", err)
	}
	latest, err := database.CreateNotice(ctx, SaveNoticeInput{
		Title: "Latest visible", Content: "latest", Visible: true,
	}, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("CreateNotice(latest) error = %v", err)
	}

	admin, err := database.ListNotices(ctx)
	if err != nil {
		t.Fatalf("ListNotices() error = %v", err)
	}
	if got := noticeIDs(admin); !reflect.DeepEqual(got, []int64{latest.ID, first.ID, hidden.ID}) {
		t.Fatalf("admin order = %v", got)
	}
	if first.Revision != 1 || first.ImageURL == nil || len(first.Tags) != 2 || first.Tags[0] != "news" {
		t.Fatalf("normalized first notice = %#v", first)
	}

	visible, total, err := database.ListVisibleNotices(ctx, 1, 5)
	if err != nil || total != 2 || !reflect.DeepEqual(noticeIDs(visible), []int64{latest.ID, first.ID}) {
		t.Fatalf("ListVisibleNotices() = (%v, %d, %v)", noticeIDs(visible), total, err)
	}

	updated, err := database.UpdateNotice(ctx, first.ID, first.Revision, SaveNoticeInput{
		Title: "First revised", Content: "revised", Tags: []string{"release"}, Visible: true,
	}, now.Add(3*time.Second))
	if err != nil || updated.Revision != 2 || updated.Title != "First revised" {
		t.Fatalf("UpdateNotice() = (%#v, %v)", updated, err)
	}
	if _, err := database.UpdateNotice(ctx, first.ID, first.Revision, SaveNoticeInput{
		Title: "stale", Content: "stale", Visible: true,
	}, now.Add(4*time.Second)); !errors.Is(err, ErrConflict) {
		t.Fatalf("UpdateNotice(stale) error = %v, want ErrConflict", err)
	}

	shown, err := database.SetNoticeVisibility(ctx, hidden.ID, hidden.Revision, true, now.Add(5*time.Second))
	if err != nil || !shown.Visible || shown.Revision != 2 {
		t.Fatalf("SetNoticeVisibility() = (%#v, %v)", shown, err)
	}
	if err := database.ReorderNotices(ctx, []int64{hidden.ID, first.ID, latest.ID}, now.Add(6*time.Second)); err != nil {
		t.Fatalf("ReorderNotices() error = %v", err)
	}
	reordered, _ := database.ListNotices(ctx)
	if got := noticeIDs(reordered); !reflect.DeepEqual(got, []int64{hidden.ID, first.ID, latest.ID}) {
		t.Fatalf("reordered IDs = %v", got)
	}
	if err := database.ReorderNotices(ctx, []int64{hidden.ID, hidden.ID, latest.ID}, now); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("ReorderNotices(duplicate) error = %v, want ErrInvalidInput", err)
	}
	if err := database.ReorderNotices(ctx, []int64{hidden.ID, first.ID}, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("ReorderNotices(stale set) error = %v, want ErrConflict", err)
	}

	if err := database.DeleteNotice(ctx, latest.ID, latest.Revision); err != nil {
		t.Fatalf("DeleteNotice() error = %v", err)
	}
	if err := database.DeleteNotice(ctx, first.ID, first.Revision); !errors.Is(err, ErrConflict) {
		t.Fatalf("DeleteNotice(stale) error = %v, want ErrConflict", err)
	}
}

func TestVisibleNoticePaginationUsesFixedBoundedPages(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	for index := 1; index <= 7; index++ {
		if _, err := database.CreateNotice(ctx, SaveNoticeInput{
			Title: "Notice " + string(rune('0'+index)), Content: "body", Visible: true,
		}, now.Add(time.Duration(index)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	first, total, err := database.ListVisibleNotices(ctx, 1, 5)
	if err != nil || total != 7 || len(first) != 5 {
		t.Fatalf("first page len=%d total=%d err=%v", len(first), total, err)
	}
	second, total, err := database.ListVisibleNotices(ctx, 2, 5)
	if err != nil || total != 7 || len(second) != 2 {
		t.Fatalf("second page len=%d total=%d err=%v", len(second), total, err)
	}
	if _, _, err := database.ListVisibleNotices(ctx, 0, 5); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("page zero error = %v, want ErrInvalidInput", err)
	}
	if _, _, err := database.ListVisibleNotices(ctx, 1, 6); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("oversized page error = %v, want ErrInvalidInput", err)
	}
	if ^uint(0)>>63 == 1 {
		page := int((int64(^uint64(0)>>1) / 5) + 2)
		if _, _, err := database.ListVisibleNotices(ctx, page, 5); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("overflowing page error = %v, want ErrInvalidInput", err)
		}
	}
}

func TestNoticeValidationRejectsUnboundedOrUnsafeContent(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Now()
	for name, input := range map[string]SaveNoticeInput{
		"empty title":   {Title: " ", Content: "body"},
		"empty content": {Title: "title", Content: " "},
		"long title":    {Title: strings.Repeat("题", 256), Content: "body"},
		"long content":  {Title: "title", Content: strings.Repeat("x", maxNoticeContentBytes+1)},
		"unsafe url":    {Title: "title", Content: "body", ImageURL: "javascript:alert(1)"},
		"credentials":   {Title: "title", Content: "body", ImageURL: "https://user:pass@example.test/image.png"},
		"too many tags": {Title: "title", Content: "body", Tags: make([]string, maxNoticeTags+1)},
		"long tag":      {Title: "title", Content: "body", Tags: []string{strings.Repeat("t", maxNoticeTagRunes+1)}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := database.CreateNotice(ctx, input, now); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("CreateNotice() error = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestNoticeReadPathsUseOrderingIndexes(t *testing.T) {
	database := newTestStore(t)
	for name, query := range map[string]string{
		"admin":   `SELECT id FROM notices ORDER BY sort_position, id DESC`,
		"visible": `SELECT id FROM notices WHERE visible = 1 ORDER BY sort_position, id DESC LIMIT 5 OFFSET 0`,
	} {
		t.Run(name, func(t *testing.T) {
			rows, err := database.db.Query(`EXPLAIN QUERY PLAN ` + query)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var plan strings.Builder
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				plan.WriteString(detail)
			}
			if !strings.Contains(plan.String(), "idx_notices_") || strings.Contains(plan.String(), "USE TEMP B-TREE FOR ORDER BY") {
				t.Fatalf("query plan does not use notice ordering index: %s", plan.String())
			}
		})
	}
}

func noticeIDs(notices []Notice) []int64 {
	ids := make([]int64, len(notices))
	for index, notice := range notices {
		ids[index] = notice.ID
	}
	return ids
}
