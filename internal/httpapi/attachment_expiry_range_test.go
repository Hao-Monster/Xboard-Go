package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestKnowledgeAttachmentHTTPExpiredAndTamperedSignedRangesCannotReadBytes(t *testing.T) {
	now := fixedNow()
	api, _ := newTestAPIWithAllOptionsAndModifier(t, nil, true, nil, nil, true, nil, nil, func(dependencies *Dependencies) {
		dependencies.Now = func() time.Time { return now }
	})
	admin := loginAdmin(t, api)
	draftToken := strings.Repeat("c", 64)
	content := []byte("abcdefgh")
	digest := attachmentTestDigest(content)

	initialized := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/knowledge-attachments/uploads", fmt.Sprintf(
		`{"original_name":"range.txt","size":8,"draft_token":%q,"sha256":%q}`, draftToken, digest))
	if initialized.Code != http.StatusOK {
		t.Fatalf("initialize status=%d", initialized.Code)
	}
	var initializeResult struct {
		Data map[string]any `json:"data"`
	}
	decodeResponse(t, initialized, &initializeResult)
	uploadUUID, _ := initializeResult.Data["upload_uuid"].(string)
	if uploadUUID == "" {
		t.Fatal("initialize did not return upload UUID")
	}
	for index, chunk := range [][]byte{content[:4], content[4:]} {
		response := attachmentChunkRequest(t, admin, api, uploadUUID, index, chunk, attachmentTestDigest(chunk))
		if response.Code != http.StatusOK {
			t.Fatalf("chunk %d status=%d", index, response.Code)
		}
	}
	completed := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/knowledge-attachments/uploads/"+uploadUUID+"/complete", `{}`)
	if completed.Code != http.StatusOK {
		t.Fatalf("complete status=%d", completed.Code)
	}
	var completeResult struct {
		Data struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	decodeResponse(t, completed, &completeResult)
	parsedURL, err := url.Parse(completeResult.Data.URL)
	if err != nil {
		t.Fatal(err)
	}

	valid := signedRangeHTTPResponse(t, api, parsedURL)
	if valid.Code != http.StatusPartialContent || valid.Body.String() != "bc" ||
		valid.Header().Get("Content-Range") != "bytes 1-2/8" {
		t.Fatalf("valid range code=%d body_len=%d content_range=%q", valid.Code, valid.Body.Len(), valid.Header().Get("Content-Range"))
	}

	now = now.Add(3 * time.Hour)
	expired := signedRangeHTTPResponse(t, api, parsedURL)
	if expired.Code != http.StatusForbidden || expired.Body.String() == "bc" || expired.Header().Get("Content-Range") != "" {
		t.Fatalf("expired range code=%d body_len=%d content_range=%q", expired.Code, expired.Body.Len(), expired.Header().Get("Content-Range"))
	}

	now = fixedNow()
	tamperedURL := *parsedURL
	query := tamperedURL.Query()
	signature := query.Get("signature")
	query.Set("signature", strings.Repeat("0", len(signature)))
	tamperedURL.RawQuery = query.Encode()
	tampered := signedRangeHTTPResponse(t, api, &tamperedURL)
	if tampered.Code != http.StatusForbidden || tampered.Body.String() == "bc" || tampered.Header().Get("Content-Range") != "" {
		t.Fatalf("tampered range code=%d body_len=%d content_range=%q", tampered.Code, tampered.Body.Len(), tampered.Header().Get("Content-Range"))
	}
}

func signedRangeHTTPResponse(t *testing.T, api http.Handler, target *url.URL) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, target.RequestURI(), nil)
	request.Header.Set("Range", "bytes=1-2")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	return response
}
