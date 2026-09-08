package attachments

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestAttachmentInlineDispositionAllowlist(t *testing.T) {
	service, _, adminID, now := newAttachmentTestService(t, 1<<20)
	cases := []struct {
		name, draft, filename, requested, wantDisposition, wantContentType string
		content                                                            []byte
	}{
		{name: "jpeg-inline", draft: "1", filename: "photo.jpg", requested: "inline", wantDisposition: "inline", wantContentType: "image/jpeg", content: []byte{0xff, 0xd8, 0xff, 0xe0, 0, 0, 0, 0}},
		{name: "png-inline", draft: "2", filename: "photo.png", requested: "inline", wantDisposition: "inline", wantContentType: "image/png", content: []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}},
		{name: "mp4-inline", draft: "3", filename: "video.mp4", requested: "inline", wantDisposition: "inline", wantContentType: "video/mp4", content: []byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 0, 0, 'm', 'p', '4', '2', 0, 0, 0, 0}},
		{name: "webm-inline", draft: "4", filename: "video.webm", requested: "inline", wantDisposition: "inline", wantContentType: "video/webm", content: []byte{0x1a, 0x45, 0xdf, 0xa3, 0x93, 'w', 'e', 'b', 'm'}},
		{name: "text-forced-download", draft: "5", filename: "notes.txt", requested: "inline", wantDisposition: "attachment", wantContentType: "application/octet-stream", content: []byte("plain text")},
		{name: "mismatched-extension-forced-download", draft: "6", filename: "photo.txt", requested: "inline", wantDisposition: "attachment", wantContentType: "application/octet-stream", content: []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}},
		{name: "explicit-download", draft: "7", filename: "photo.jpg", requested: "attachment", wantDisposition: "attachment", wantContentType: "application/octet-stream", content: []byte{0xff, 0xd8, 0xff, 0xe0, 0, 0, 0, 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			attachment := uploadTestAttachment(t, service, adminID, testDraftToken(tc.draft), tc.filename, tc.content, now)
			signed, err := service.SignedURL(attachment, tc.requested, now)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := url.Parse(signed)
			if err != nil {
				t.Fatal(err)
			}
			if got := parsed.Query().Get("disposition"); got != tc.wantDisposition {
				t.Fatalf("signed disposition=%q, want %q", got, tc.wantDisposition)
			}
			authorized, err := service.AuthorizeSigned(context.Background(), attachment.UUID,
				parsed.Query().Get("disposition"), parsed.Query().Get("expires"), parsed.Query().Get("signature"), now)
			if err != nil {
				t.Fatal(err)
			}
			file, opened, err := service.OpenKnown(authorized)
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			service.Serve(response, httptest.NewRequest(http.MethodGet, signed, nil), opened, file, tc.wantDisposition)
			if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), tc.content) {
				t.Fatalf("response code=%d body=%q, want 200 and %q", response.Code, response.Body.Bytes(), tc.content)
			}
			if got := response.Header().Get("Content-Type"); got != tc.wantContentType {
				t.Fatalf("content type=%q, want %q", got, tc.wantContentType)
			}
			if got := response.Header().Get("Content-Disposition"); !strings.HasPrefix(got, tc.wantDisposition+";") {
				t.Fatalf("content disposition=%q, want %q", got, tc.wantDisposition)
			}
		})
	}
}

func TestSignedAttachmentAccessRejectsTamperingAndServesSafeRanges(t *testing.T) {
	service, _, adminID, now := newAttachmentTestService(t, 1<<20)
	attachment := uploadTestAttachment(t, service, adminID, testDraftToken("e"), "手册.txt", []byte("0123456789"), now)
	signed, err := service.SignedURL(attachment, "inline", now)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(signed)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("disposition") != "attachment" {
		t.Fatalf("unsafe inline request must become download: %s", signed)
	}
	authorized, err := service.AuthorizeSigned(context.Background(), attachment.UUID,
		parsed.Query().Get("disposition"), parsed.Query().Get("expires"), parsed.Query().Get("signature"), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AuthorizeSigned(context.Background(), attachment.UUID, "inline",
		parsed.Query().Get("expires"), parsed.Query().Get("signature"), now); err != ErrNotFound {
		t.Fatalf("tampered disposition error=%v", err)
	}
	if _, err := service.AuthorizeSigned(context.Background(), attachment.UUID, "attachment",
		parsed.Query().Get("expires"), parsed.Query().Get("signature"), now.Add(3*time.Hour)); err != ErrNotFound {
		t.Fatalf("expired signature error=%v", err)
	}

	file, _, err := service.OpenKnown(authorized)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, signed, nil)
	request.Header.Set("Range", "bytes=2-5")
	response := httptest.NewRecorder()
	service.Serve(response, request, attachment, file, "attachment")
	if response.Code != http.StatusPartialContent || response.Body.String() != "2345" || response.Header().Get("Content-Range") != "bytes 2-5/10" {
		t.Fatalf("bounded range code=%d body=%q headers=%v", response.Code, response.Body.String(), response.Header())
	}
	for _, header := range []string{"Accept-Ranges", "Vary", "X-Knowledge-Attachment-Size", "ETag", "X-Content-Type-Options", "Content-Security-Policy", "Cross-Origin-Resource-Policy", "Referrer-Policy", "X-Download-Options", "Cache-Control"} {
		if response.Header().Get(header) == "" {
			t.Errorf("missing security header %s", header)
		}
	}
	if response.Header().Get("Vary") != "Range" || response.Header().Get("X-Knowledge-Attachment-Size") != "10" {
		t.Fatalf("range metadata headers=%v", response.Header())
	}
	if disposition := response.Header().Get("Content-Disposition"); strings.ContainsAny(disposition, "\r\n") || !strings.HasPrefix(disposition, "attachment;") {
		t.Fatalf("unsafe content disposition: %q", disposition)
	}

	file, _, err = service.OpenKnown(authorized)
	if err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodGet, signed, nil)
	request.Header.Set("Range", "bytes=0-1,3-4")
	response = httptest.NewRecorder()
	service.Serve(response, request, attachment, file, "attachment")
	if response.Code != http.StatusRequestedRangeNotSatisfiable || response.Header().Get("Content-Range") != "bytes */10" {
		t.Fatalf("multi-range code=%d headers=%v", response.Code, response.Header())
	}
	for _, scenario := range []struct {
		name, method, value, body, contentRange string
		status                                  int
	}{
		{name: "suffix", method: http.MethodGet, value: "bytes=-3", body: "789", contentRange: "bytes 7-9/10", status: http.StatusPartialContent},
		{name: "head", method: http.MethodHead, value: "bytes=2-5", body: "", contentRange: "bytes 2-5/10", status: http.StatusPartialContent},
		{name: "unsatisfied", method: http.MethodGet, value: "bytes=20-30", body: "", contentRange: "bytes */10", status: http.StatusRequestedRangeNotSatisfiable},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			openedFile, openedAttachment, err := service.Open(context.Background(), attachment.UUID)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(scenario.method, signed, nil)
			request.Header.Set("Range", scenario.value)
			response := httptest.NewRecorder()
			service.Serve(response, request, openedAttachment, openedFile, "attachment")
			if response.Code != scenario.status || response.Body.String() != scenario.body || response.Header().Get("Content-Range") != scenario.contentRange {
				t.Fatalf("response code=%d body=%q headers=%v", response.Code, response.Body.String(), response.Header())
			}
		})
	}
}
