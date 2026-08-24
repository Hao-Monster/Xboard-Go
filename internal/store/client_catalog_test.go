package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestClientCatalogOverridesReplaceAtomicallyWithRevision(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	initial, err := database.GetClientCatalogConfig(ctx)
	if err != nil || initial.Revision != 1 || len(initial.Links) != 0 {
		t.Fatalf("GetClientCatalogConfig(initial) = (%#v, %v)", initial, err)
	}

	now := time.Date(2026, 8, 24, 15, 0, 0, 0, time.UTC)
	links := []ClientCatalogOverride{
		{ClientID: "karing", Platform: "android", Action: "direct", URL: "https://downloads.example.test/karing.apk"},
		{ClientID: "karing", Platform: "android", Action: "tutorial", URL: "/guide/12/karing"},
	}
	saved, err := database.ReplaceClientCatalogOverrides(ctx, initial.Revision, links, now)
	if err != nil || saved.Revision != 2 || !reflect.DeepEqual(saved.Links, links) || !saved.UpdatedAt.Equal(now) {
		t.Fatalf("ReplaceClientCatalogOverrides() = (%#v, %v)", saved, err)
	}
	if _, err := database.ReplaceClientCatalogOverrides(ctx, initial.Revision, nil, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale replacement error = %v, want ErrConflict", err)
	}
	current, err := database.GetClientCatalogConfig(ctx)
	if err != nil || !reflect.DeepEqual(current, saved) {
		t.Fatalf("config changed after stale replacement: (%#v, %v)", current, err)
	}
}

func TestClientCatalogOverridesRejectDuplicatesAndUnboundedFields(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Now()
	valid := ClientCatalogOverride{ClientID: "karing", Platform: "android", Action: "direct", URL: "https://example.test/app"}
	for name, links := range map[string][]ClientCatalogOverride{
		"duplicate": {valid, valid},
		"platform":  {{ClientID: "karing", Platform: "beos", Action: "direct", URL: valid.URL}},
		"action":    {{ClientID: "karing", Platform: "android", Action: "shell", URL: valid.URL}},
		"long url":  {{ClientID: "karing", Platform: "android", Action: "direct", URL: "https://example.test/" + strings.Repeat("x", 2049)}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := database.ReplaceClientCatalogOverrides(ctx, 1, links, now); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("error = %v, want ErrInvalidInput", err)
			}
		})
	}
}
