package httpapi

import (
	"encoding/base64"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/attachments"
	"github.com/Hao-Monster/Xboard-Go/internal/clientcatalog"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func registerKnowledgeAttachmentRoutes(mux *http.ServeMux, prefix string, server *server) {
	// Xboard v2 compatibility surface.
	mux.HandleFunc("POST "+prefix+"/upload/initialize", server.initializeKnowledgeAttachmentUpload)
	mux.HandleFunc("POST "+prefix+"/upload/{uploadUUID}/chunk", server.storeKnowledgeAttachmentChunk)
	mux.HandleFunc("GET "+prefix+"/upload/{uploadUUID}", server.getKnowledgeAttachmentUpload)
	mux.HandleFunc("POST "+prefix+"/upload/{uploadUUID}/complete", server.completeKnowledgeAttachmentUpload)
	mux.HandleFunc("POST "+prefix+"/upload/{uploadUUID}/cancel", server.cancelKnowledgeAttachmentUpload)
	mux.HandleFunc("GET "+prefix+"/fetch", server.listKnowledgeAttachments)
	mux.HandleFunc("POST "+prefix+"/drop", server.dropKnowledgeAttachment)
	mux.HandleFunc("POST "+prefix+"/qr-code", server.knowledgeAttachmentQRCode)

	// Stable modern aliases used by the rewritten frontend.
	mux.HandleFunc("POST "+prefix+"/uploads", server.initializeKnowledgeAttachmentUpload)
	mux.HandleFunc("POST "+prefix+"/uploads/{uploadUUID}/chunks", server.storeKnowledgeAttachmentChunk)
	mux.HandleFunc("GET "+prefix+"/uploads/{uploadUUID}", server.getKnowledgeAttachmentUpload)
	mux.HandleFunc("POST "+prefix+"/uploads/{uploadUUID}/complete", server.completeKnowledgeAttachmentUpload)
	mux.HandleFunc("POST "+prefix+"/uploads/{uploadUUID}/cancel", server.cancelKnowledgeAttachmentUpload)
	mux.HandleFunc("GET "+prefix, server.listKnowledgeAttachments)
	mux.HandleFunc("POST "+prefix+"/{attachmentUUID}/drop", server.dropKnowledgeAttachment)
	mux.HandleFunc("POST "+prefix+"/clone", server.cloneKnowledgeAttachments)
}

func (s *server) initializeKnowledgeAttachmentUpload(w http.ResponseWriter, r *http.Request) {
	if !s.requireAttachments(w) {
		return
	}
	var input struct {
		OriginalName string `json:"original_name"`
		Size         int64  `json:"size"`
		DraftToken   string `json:"draft_token"`
		SHA256       string `json:"sha256"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	session, _ := sessionFromContext(r.Context())
	upload, err := s.attachments.Initialize(r.Context(), session.UserID, attachments.InitializeInput{
		OriginalName: input.OriginalName, Size: input.Size, DraftToken: strings.ToLower(input.DraftToken), SHA256: strings.ToLower(input.SHA256),
	}, s.now())
	if err != nil {
		handleKnowledgeAttachmentError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, knowledgeAttachmentUploadPayload(upload))
}

func (s *server) storeKnowledgeAttachmentChunk(w http.ResponseWriter, r *http.Request) {
	if !s.requireAttachments(w) {
		return
	}
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(s.now().Add(2 * time.Minute))
	_ = controller.SetWriteDeadline(s.now().Add(2 * time.Minute))
	limit := s.attachments.ChunkSize() + 256<<10
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	if err := r.ParseMultipartForm(64 << 10); err != nil {
		writeAPIError(w, http.StatusUnprocessableEntity, "attachment_chunk_invalid", "附件分片格式或大小无效", nil)
		return
	}
	defer r.MultipartForm.RemoveAll()
	index, err := strconv.Atoi(r.FormValue("index"))
	if err != nil || index < 0 {
		writeAPIError(w, http.StatusUnprocessableEntity, "attachment_chunk_invalid", "附件分片编号无效", nil)
		return
	}
	digest := strings.ToLower(strings.TrimSpace(r.FormValue("sha256")))
	file, header, err := r.FormFile("file")
	if err != nil {
		handleMultipartAttachmentError(w, err)
		return
	}
	defer file.Close()
	session, _ := sessionFromContext(r.Context())
	result, err := s.attachments.StoreChunk(r.Context(), session.UserID, r.PathValue("uploadUUID"), index, digest, file, header.Size, s.now())
	if err != nil {
		handleKnowledgeAttachmentError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, map[string]any{
		"accepted_index": index, "idempotent": result.Idempotent,
		"received_chunks": result.ReceivedChunks, "ready_to_complete": result.ReadyToComplete,
	})
}

func (s *server) getKnowledgeAttachmentUpload(w http.ResponseWriter, r *http.Request) {
	if !s.requireAttachments(w) {
		return
	}
	session, _ := sessionFromContext(r.Context())
	upload, err := s.attachments.Status(r.Context(), session.UserID, r.PathValue("uploadUUID"), s.now())
	if err != nil {
		handleKnowledgeAttachmentError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, knowledgeAttachmentUploadPayload(upload))
}

func (s *server) completeKnowledgeAttachmentUpload(w http.ResponseWriter, r *http.Request) {
	if !s.requireAttachments(w) {
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(s.now().Add(30 * time.Minute))
	session, _ := sessionFromContext(r.Context())
	attachment, err := s.attachments.Complete(r.Context(), session.UserID, r.PathValue("uploadUUID"), s.now())
	if err != nil {
		handleKnowledgeAttachmentError(w, err)
		return
	}
	payload, err := s.knowledgeAttachmentPayload(attachment)
	if err != nil {
		handleKnowledgeAttachmentError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, payload)
}

func (s *server) cancelKnowledgeAttachmentUpload(w http.ResponseWriter, r *http.Request) {
	if !s.requireAttachments(w) {
		return
	}
	var input struct {
		DraftToken string `json:"draft_token"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	session, _ := sessionFromContext(r.Context())
	if err := s.attachments.Cancel(r.Context(), session.UserID, r.PathValue("uploadUUID"), strings.ToLower(input.DraftToken), s.now()); err != nil {
		handleKnowledgeAttachmentError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, true)
}

func (s *server) listKnowledgeAttachments(w http.ResponseWriter, r *http.Request) {
	if !s.requireAttachments(w) {
		return
	}
	var knowledgeID *int64
	if value := strings.TrimSpace(r.URL.Query().Get("knowledge_id")); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed < 1 {
			handleKnowledgeAttachmentError(w, attachments.ErrInvalidInput)
			return
		}
		knowledgeID = &parsed
	}
	draftToken := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("draft_token")))
	page, ok := positiveQueryInt(w, r, "page", 1)
	if !ok {
		return
	}
	perPage, ok := positiveQueryInt(w, r, "per_page", 50)
	if !ok {
		return
	}
	if perPage > 100 {
		handleKnowledgeAttachmentError(w, attachments.ErrInvalidInput)
		return
	}
	session, _ := sessionFromContext(r.Context())
	result, err := s.attachments.List(r.Context(), session.UserID, knowledgeID, draftToken, page, perPage)
	if err != nil {
		handleKnowledgeAttachmentError(w, err)
		return
	}
	items := make([]map[string]any, 0, len(result.Items))
	for _, attachment := range result.Items {
		payload, err := s.knowledgeAttachmentPayload(attachment)
		if err != nil {
			handleKnowledgeAttachmentError(w, err)
			return
		}
		items = append(items, payload)
	}
	writeSuccess(w, http.StatusOK, map[string]any{"items": items, "total": result.Total, "page": result.Page, "per_page": result.PerPage})
}

func (s *server) dropKnowledgeAttachment(w http.ResponseWriter, r *http.Request) {
	if !s.requireAttachments(w) {
		return
	}
	var input struct {
		UUID       string `json:"uuid"`
		DraftToken string `json:"draft_token"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.UUID == "" {
		input.UUID = r.PathValue("attachmentUUID")
	}
	session, _ := sessionFromContext(r.Context())
	if err := s.attachments.DropDraft(r.Context(), session.UserID, input.UUID, strings.ToLower(input.DraftToken)); err != nil {
		handleKnowledgeAttachmentError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, true)
}

func (s *server) knowledgeAttachmentQRCode(w http.ResponseWriter, r *http.Request) {
	if !s.requireAttachments(w) {
		return
	}
	var input struct {
		URL string `json:"url"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	parsed, err := url.Parse(input.URL)
	if err != nil || len(input.URL) > 2048 || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		handleKnowledgeAttachmentError(w, attachments.ErrInvalidInput)
		return
	}
	dataURL, err := clientcatalog.QRDataURL(input.URL)
	if err != nil {
		handleKnowledgeAttachmentError(w, attachments.ErrInvalidInput)
		return
	}
	encoded := strings.TrimPrefix(dataURL, "data:image/svg+xml;base64,")
	svg, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		handleKnowledgeAttachmentError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, map[string]string{"svg": string(svg)})
}

func (s *server) cloneKnowledgeAttachments(w http.ResponseWriter, r *http.Request) {
	if !s.requireAttachments(w) {
		return
	}
	var input struct {
		SourceKnowledgeID int64    `json:"source_knowledge_id"`
		SourceUUIDs       []string `json:"source_uuids"`
		DraftToken        string   `json:"draft_token"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	session, _ := sessionFromContext(r.Context())
	clones, err := s.attachments.CloneForDraft(r.Context(), session.UserID, input.SourceKnowledgeID, input.SourceUUIDs, strings.ToLower(input.DraftToken), s.now())
	if err != nil {
		handleKnowledgeAttachmentError(w, err)
		return
	}
	items := make([]map[string]any, 0, len(clones))
	for _, clone := range clones {
		payload, err := s.knowledgeAttachmentPayload(clone.Attachment)
		if err != nil {
			handleKnowledgeAttachmentError(w, err)
			return
		}
		items = append(items, map[string]any{"source_uuid": clone.SourceUUID, "attachment": payload})
	}
	writeSuccess(w, http.StatusOK, map[string]any{"items": items})
}

func (s *server) readSignedKnowledgeAttachment(w http.ResponseWriter, r *http.Request) {
	if !s.requireAttachments(w) {
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(s.now().Add(30 * time.Minute))
	if !exactAttachmentSignatureQuery(r.URL.Query()) {
		writeAPIError(w, http.StatusForbidden, "attachment_signature_invalid", "附件链接无效或已过期", nil)
		return
	}
	attachment, err := s.attachments.AuthorizeSigned(r.Context(), r.PathValue("attachmentUUID"),
		r.URL.Query().Get("disposition"), r.URL.Query().Get("expires"), r.URL.Query().Get("signature"), s.now())
	if err != nil {
		writeAPIError(w, http.StatusForbidden, "attachment_signature_invalid", "附件链接无效或已过期", nil)
		return
	}
	file, attachment, err := s.attachments.OpenKnown(attachment)
	if errors.Is(err, attachments.ErrNotFound) {
		writeAPIError(w, http.StatusNotFound, "attachment_not_found", "附件不存在", nil)
		return
	}
	if errors.Is(err, attachments.ErrConflict) {
		writeAPIError(w, http.StatusGone, "attachment_integrity_failed", "附件不可用", nil)
		return
	}
	if err != nil {
		handleKnowledgeAttachmentError(w, err)
		return
	}
	s.attachments.Serve(w, r, attachment, file, r.URL.Query().Get("disposition"))
}

func exactAttachmentSignatureQuery(query url.Values) bool {
	if len(query) != 3 {
		return false
	}
	for _, key := range []string{"disposition", "expires", "signature"} {
		if len(query[key]) != 1 {
			return false
		}
	}
	return true
}

func (s *server) readPublicKnowledgeAttachment(w http.ResponseWriter, r *http.Request) {
	if !s.requireAttachments(w) {
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(s.now().Add(30 * time.Minute))
	file, attachment, err := s.attachments.OpenPublic(r.Context(), r.PathValue("attachmentUUID"))
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "attachment_not_found", "附件不存在", nil)
		return
	}
	s.attachments.Serve(w, r, attachment, file, "inline")
}

func (s *server) requireAttachments(w http.ResponseWriter) bool {
	if s.attachments == nil {
		writeAPIError(w, http.StatusNotFound, "attachment_not_configured", "附件功能未启用", nil)
		return false
	}
	return true
}

func knowledgeAttachmentUploadPayload(upload store.KnowledgeAttachmentUpload) map[string]any {
	return map[string]any{
		"upload_uuid": upload.UUID, "original_name": upload.OriginalName, "declared_size": upload.DeclaredSize,
		"chunk_size": upload.ChunkSize, "total_chunks": upload.TotalChunks, "received_chunks": upload.ReceivedChunks,
		"uploaded_chunks": upload.UploadedChunks, "status": upload.Status, "expires_at": upload.ExpiresAt.Unix(),
	}
}

func (s *server) knowledgeAttachmentPayload(attachment store.KnowledgeAttachment) (map[string]any, error) {
	attachmentURL, err := s.attachments.SignedURL(attachment, "inline", s.now())
	if err != nil {
		return nil, err
	}
	parsed, _ := url.Parse(attachmentURL)
	disposition := parsed.Query().Get("disposition")
	return map[string]any{
		"uuid": attachment.UUID, "knowledge_id": attachment.KnowledgeID, "original_name": attachment.OriginalName,
		"mime_type": attachment.MIMEType, "extension": attachment.Extension, "size": attachment.Size,
		"sha256": attachment.SHA256, "status": attachment.Status, "disposition": disposition,
		"url": attachmentURL, "placeholder": "knowledge-attachment://" + attachment.UUID,
		"created_at": attachment.CreatedAt.Unix(),
	}, nil
}

func handleMultipartAttachmentError(w http.ResponseWriter, err error) {
	if errors.Is(err, http.ErrMissingFile) || errors.Is(err, multipart.ErrMessageTooLarge) {
		writeAPIError(w, http.StatusUnprocessableEntity, "attachment_chunk_invalid", "附件分片缺失或过大", nil)
		return
	}
	handleKnowledgeAttachmentError(w, err)
}

func handleKnowledgeAttachmentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, attachments.ErrInvalidInput):
		writeAPIError(w, http.StatusUnprocessableEntity, "attachment_validation_failed", "附件参数无效", nil)
	case errors.Is(err, attachments.ErrHashMismatch):
		writeAPIError(w, http.StatusUnprocessableEntity, "attachment_hash_mismatch", "附件内容校验失败，请重新上传", nil)
	case errors.Is(err, attachments.ErrQuotaExceeded):
		writeAPIError(w, http.StatusUnprocessableEntity, "attachment_quota_exceeded", "附件存储配额不足", nil)
	case errors.Is(err, attachments.ErrConflict):
		writeAPIError(w, http.StatusConflict, "attachment_conflict", "附件状态冲突", nil)
	case errors.Is(err, attachments.ErrExpired):
		writeAPIError(w, http.StatusConflict, "attachment_upload_expired", "附件上传已过期", nil)
	case errors.Is(err, attachments.ErrNotFound):
		writeAPIError(w, http.StatusNotFound, "attachment_not_found", "附件不存在", nil)
	default:
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
	}
}

func (s *server) auditLegacyKnowledgeAttachmentMutations(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}
		recorder := &responseStatusRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		session, ok := sessionFromContext(r.Context())
		if !ok {
			return
		}
		action := r.URL.Path
		if index := strings.Index(action, "/knowledge/attachment/"); index >= 0 {
			action = action[index+len("/knowledge/attachment/"):]
		}
		s.recordAdminAudit(r.Context(), session, r.Method, fmt.Sprintf("/api/v2/{secure_admin}/knowledge/attachment/%s", action), recorder.statusCode())
	})
}
