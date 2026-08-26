package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestKnowledgeAttachmentHTTPUploadBindSignedAndPublicRead(t *testing.T) {
	api, database := newTestAPIWithAttachments(t)
	admin := loginAdmin(t, api)
	draftToken := strings.Repeat("a", 64)
	content := []byte("abcdefgh")
	wholeDigest := attachmentTestDigest(content)

	unsafe := admin.request(t, api, http.MethodPost, "/api/v1/admin/knowledge-attachments/uploads", fmt.Sprintf(`{
		"original_name":"../escape.bin","size":8,"draft_token":%q,"sha256":%q
	}`, draftToken, wholeDigest))
	if unsafe.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unsafe name status=%d body=%s", unsafe.Code, unsafe.Body)
	}

	initialized := admin.request(t, api, http.MethodPost, "/api/v1/admin/knowledge-attachments/uploads", fmt.Sprintf(`{
		"original_name":"guide.txt","size":8,"draft_token":%q,"sha256":%q
	}`, draftToken, wholeDigest))
	if initialized.Code != http.StatusOK {
		t.Fatalf("initialize status=%d body=%s", initialized.Code, initialized.Body)
	}
	var initializeResult struct {
		Data map[string]any `json:"data"`
	}
	decodeResponse(t, initialized, &initializeResult)
	uploadUUID, _ := initializeResult.Data["upload_uuid"].(string)
	_, numericExpiry := initializeResult.Data["expires_at"].(float64)
	if uploadUUID == "" || !numericExpiry || initializeResult.Data["temporary_path"] != nil || initializeResult.Data["draft_token"] != nil || initializeResult.Data["expected_sha256"] != nil {
		t.Fatalf("unsafe initialize payload=%v", initializeResult.Data)
	}

	for index, chunk := range [][]byte{content[:4], content[4:]} {
		response := attachmentChunkRequest(t, admin, api, uploadUUID, index, chunk, attachmentTestDigest(chunk))
		if response.Code != http.StatusOK {
			t.Fatalf("chunk %d status=%d body=%s", index, response.Code, response.Body)
		}
	}
	idempotent := attachmentChunkRequest(t, admin, api, uploadUUID, 0, content[:4], attachmentTestDigest(content[:4]))
	if idempotent.Code != http.StatusOK || !strings.Contains(idempotent.Body.String(), `"idempotent":true`) {
		t.Fatalf("idempotent chunk status=%d body=%s", idempotent.Code, idempotent.Body)
	}

	completed := admin.request(t, api, http.MethodPost, "/api/v1/admin/knowledge-attachments/uploads/"+uploadUUID+"/complete", `{}`)
	if completed.Code != http.StatusOK {
		t.Fatalf("complete status=%d body=%s", completed.Code, completed.Body)
	}
	var completeResult struct {
		Data struct {
			UUID, URL, Placeholder, Disposition string
			CreatedAt                           int64           `json:"created_at"`
			UpdatedAt                           json.RawMessage `json:"updated_at"`
		} `json:"data"`
	}
	decodeResponse(t, completed, &completeResult)
	if completeResult.Data.UUID != uploadUUID || completeResult.Data.Disposition != "attachment" || completeResult.Data.Placeholder != "knowledge-attachment://"+uploadUUID ||
		completeResult.Data.CreatedAt == 0 || completeResult.Data.UpdatedAt != nil {
		t.Fatalf("complete payload=%+v", completeResult.Data)
	}

	parsedURL, err := url.Parse(completeResult.Data.URL)
	if err != nil {
		t.Fatal(err)
	}
	rangeRequest := httptest.NewRequest(http.MethodGet, parsedURL.RequestURI(), nil)
	rangeRequest.Header.Set("Range", "bytes=2-5")
	rangeResponse := httptest.NewRecorder()
	api.ServeHTTP(rangeResponse, rangeRequest)
	if rangeResponse.Code != http.StatusPartialContent || rangeResponse.Body.String() != "cdef" || rangeResponse.Header().Get("Content-Range") != "bytes 2-5/8" {
		t.Fatalf("signed range status=%d body=%q headers=%v", rangeResponse.Code, rangeResponse.Body.String(), rangeResponse.Header())
	}
	tampered := *parsedURL
	query := tampered.Query()
	query.Set("disposition", "inline")
	tampered.RawQuery = query.Encode()
	tamperedResponse := httptest.NewRecorder()
	api.ServeHTTP(tamperedResponse, httptest.NewRequest(http.MethodGet, tampered.RequestURI(), nil))
	if tamperedResponse.Code != http.StatusForbidden {
		t.Fatalf("tampered status=%d body=%s", tamperedResponse.Code, tamperedResponse.Body)
	}
	extra := *parsedURL
	extraQuery := extra.Query()
	extraQuery.Set("tracking", "not-signed")
	extra.RawQuery = extraQuery.Encode()
	extraResponse := httptest.NewRecorder()
	api.ServeHTTP(extraResponse, httptest.NewRequest(http.MethodGet, extra.RequestURI(), nil))
	if extraResponse.Code != http.StatusForbidden {
		t.Fatalf("unsigned query status=%d body=%s", extraResponse.Code, extraResponse.Body)
	}

	article := admin.request(t, api, http.MethodPost, "/api/v1/admin/knowledge", fmt.Sprintf(`{
		"language":"zh-CN","category":"guide","title":"附件指南","body":"[下载](%s)","show":true,"draft_token":%q
	}`, completeResult.Data.Placeholder, draftToken))
	if article.Code != http.StatusCreated {
		t.Fatalf("bind article status=%d body=%s", article.Code, article.Body)
	}
	var articleResult struct {
		Data struct {
			ID   int64  `json:"id"`
			Body string `json:"body"`
		} `json:"data"`
	}
	decodeResponse(t, article, &articleResult)
	if articleResult.Data.Body != "[下载](knowledge-attachment://"+uploadUUID+")" {
		t.Fatalf("administrator create response must retain editable placeholder: %+v", articleResult.Data)
	}
	adminDetail := admin.request(t, api, http.MethodGet, fmt.Sprintf("/api/v1/admin/knowledge/%d", articleResult.Data.ID), "")
	if adminDetail.Code != http.StatusOK || !strings.Contains(adminDetail.Body.String(), "knowledge-attachment://"+uploadUUID) ||
		strings.Contains(adminDetail.Body.String(), "/knowledge-attachments/"+uploadUUID) {
		t.Fatalf("administrator detail must retain editable placeholder: status=%d body=%s", adminDetail.Code, adminDetail.Body)
	}
	viewer := createKnowledgeTestUser(t, database, "attachment-viewer@example.test", "attachment-viewer-password-123", 1, false)
	viewerClient := loginAs(t, api, viewer.email, viewer.password)
	viewerDetail := viewerClient.request(t, api, http.MethodGet, fmt.Sprintf("/api/v1/knowledge/%d", articleResult.Data.ID), "")
	if viewerDetail.Code != http.StatusOK || !strings.Contains(viewerDetail.Body.String(), "/knowledge-attachments/"+uploadUUID) ||
		strings.Contains(viewerDetail.Body.String(), "knowledge-attachment://") {
		t.Fatalf("user detail must contain a signed URL instead of the editable placeholder: status=%d body=%s", viewerDetail.Code, viewerDetail.Body)
	}
	cloneToken := strings.Repeat("b", 64)
	cloned := admin.request(t, api, http.MethodPost, "/api/v1/admin/knowledge-attachments/clone", fmt.Sprintf(`{
		"source_knowledge_id":%d,"source_uuids":[%q],"draft_token":%q
	}`, articleResult.Data.ID, uploadUUID, cloneToken))
	if cloned.Code != http.StatusOK || !strings.Contains(cloned.Body.String(), `"source_uuid":"`+uploadUUID+`"`) {
		t.Fatalf("clone status=%d body=%s", cloned.Code, cloned.Body)
	}
	qrCode := admin.request(t, api, http.MethodPost, "/api/v1/admin/knowledge-attachments/qr-code", fmt.Sprintf(`{"url":%q}`, completeResult.Data.URL))
	var qrResult struct {
		Data struct {
			SVG string `json:"svg"`
		} `json:"data"`
	}
	decodeResponse(t, qrCode, &qrResult)
	if qrCode.Code != http.StatusOK || !strings.Contains(qrResult.Data.SVG, "<svg") {
		t.Fatalf("QR status=%d body=%s", qrCode.Code, qrCode.Body)
	}
	publicResponse := httptest.NewRecorder()
	api.ServeHTTP(publicResponse, httptest.NewRequest(http.MethodGet, "/guide-attachments/"+uploadUUID, nil))
	if publicResponse.Code != http.StatusOK || publicResponse.Body.String() != string(content) {
		t.Fatalf("public attachment status=%d body=%q", publicResponse.Code, publicResponse.Body.String())
	}
	publicArticle := httptest.NewRecorder()
	api.ServeHTTP(publicArticle, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/guide/%d/article", articleResult.Data.ID), nil))
	if publicArticle.Code != http.StatusOK || !strings.Contains(publicArticle.Body.String(), "/guide-attachments/"+uploadUUID) ||
		strings.Contains(publicArticle.Body.String(), "knowledge-attachment://") {
		t.Fatalf("public article must contain the stable public attachment URL: status=%d body=%s", publicArticle.Code, publicArticle.Body)
	}

	legacy := loginLegacyBearer(t, api, "admin@example.test", "admin-password-123")
	legacyPrefix := "/api/v2/admin/knowledge/attachment"
	legacyFetch := bearerRequest(api, http.MethodGet, fmt.Sprintf("%s/fetch?knowledge_id=%d&page=1&per_page=100", legacyPrefix, articleResult.Data.ID), legacy.Authorization, "")
	if legacyFetch.Code != http.StatusOK || !strings.Contains(legacyFetch.Body.String(), uploadUUID) {
		t.Fatalf("legacy fetch status=%d body=%s", legacyFetch.Code, legacyFetch.Body)
	}
	legacyAll := bearerRequest(api, http.MethodGet, legacyPrefix+"/fetch", legacy.Authorization, "")
	if legacyAll.Code != http.StatusOK || !strings.Contains(legacyAll.Body.String(), uploadUUID) || !strings.Contains(legacyAll.Body.String(), `"per_page":50`) {
		t.Fatalf("legacy unfiltered fetch status=%d body=%s", legacyAll.Code, legacyAll.Body)
	}
	legacyCloneToken := strings.Repeat("c", 64)
	legacyClone := bearerRequest(api, http.MethodPost, legacyPrefix+"/clone", legacy.Authorization, fmt.Sprintf(`{
		"source_knowledge_id":%d,"source_uuids":[%q],"draft_token":%q
	}`, articleResult.Data.ID, uploadUUID, legacyCloneToken))
	var legacyCloneResult struct {
		Data struct {
			Items []struct {
				Attachment struct {
					UUID string `json:"uuid"`
				} `json:"attachment"`
			} `json:"items"`
		} `json:"data"`
	}
	decodeResponse(t, legacyClone, &legacyCloneResult)
	if legacyClone.Code != http.StatusOK || len(legacyCloneResult.Data.Items) != 1 {
		t.Fatalf("legacy clone status=%d body=%s", legacyClone.Code, legacyClone.Body)
	}
	legacyDrop := bearerRequest(api, http.MethodPost, legacyPrefix+"/drop", legacy.Authorization, fmt.Sprintf(`{"uuid":%q,"draft_token":%q}`, legacyCloneResult.Data.Items[0].Attachment.UUID, legacyCloneToken))
	if legacyDrop.Code != http.StatusOK {
		t.Fatalf("legacy drop status=%d body=%s", legacyDrop.Code, legacyDrop.Body)
	}
	legacyQR := bearerRequest(api, http.MethodPost, legacyPrefix+"/qr-code", legacy.Authorization, fmt.Sprintf(`{"url":%q}`, completeResult.Data.URL))
	if legacyQR.Code != http.StatusOK || !strings.Contains(legacyQR.Body.String(), `"svg"`) {
		t.Fatalf("legacy QR status=%d body=%s", legacyQR.Code, legacyQR.Body)
	}
	legacyExternalQR := bearerRequest(api, http.MethodPost, legacyPrefix+"/qr-code", legacy.Authorization, `{"url":"https://downloads.example.test/client?id=1"}`)
	if legacyExternalQR.Code != http.StatusOK || !strings.Contains(legacyExternalQR.Body.String(), `"svg"`) {
		t.Fatalf("legacy external QR status=%d body=%s", legacyExternalQR.Code, legacyExternalQR.Body)
	}
	legacyUnsafeQR := bearerRequest(api, http.MethodPost, legacyPrefix+"/qr-code", legacy.Authorization, `{"url":"javascript:alert(1)"}`)
	if legacyUnsafeQR.Code != http.StatusUnprocessableEntity {
		t.Fatalf("legacy unsafe QR status=%d body=%s", legacyUnsafeQR.Code, legacyUnsafeQR.Body)
	}
	legacyUploadToken := strings.Repeat("d", 64)
	legacyInitialize := bearerRequest(api, http.MethodPost, legacyPrefix+"/upload/initialize", legacy.Authorization, fmt.Sprintf(`{"original_name":"cancel.bin","size":1,"draft_token":%q}`, legacyUploadToken))
	var legacyUploadResult struct {
		Data struct {
			UUID string `json:"upload_uuid"`
		} `json:"data"`
	}
	decodeResponse(t, legacyInitialize, &legacyUploadResult)
	if legacyInitialize.Code != http.StatusOK || legacyUploadResult.Data.UUID == "" {
		t.Fatalf("legacy initialize status=%d body=%s", legacyInitialize.Code, legacyInitialize.Body)
	}
	legacyStatus := bearerRequest(api, http.MethodGet, legacyPrefix+"/upload/"+legacyUploadResult.Data.UUID, legacy.Authorization, "")
	if legacyStatus.Code != http.StatusOK || !strings.Contains(legacyStatus.Body.String(), `"status":"initialized"`) {
		t.Fatalf("legacy status=%d body=%s", legacyStatus.Code, legacyStatus.Body)
	}
	legacyCancel := bearerRequest(api, http.MethodPost, legacyPrefix+"/upload/"+legacyUploadResult.Data.UUID+"/cancel", legacy.Authorization, fmt.Sprintf(`{"draft_token":%q}`, legacyUploadToken))
	if legacyCancel.Code != http.StatusOK {
		t.Fatalf("legacy cancel status=%d body=%s", legacyCancel.Code, legacyCancel.Body)
	}
}

func attachmentChunkRequest(t *testing.T, client testClient, api http.Handler, uploadUUID string, index int, content []byte, digest string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("index", fmt.Sprintf("%d", index))
	_ = writer.WriteField("sha256", digest)
	part, err := writer.CreateFormFile("file", fmt.Sprintf("%d.part", index))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/knowledge-attachments/uploads/"+uploadUUID+"/chunks", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("X-CSRF-Token", client.csrf)
	client.addCookies(request)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	return response
}

func attachmentTestDigest(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
