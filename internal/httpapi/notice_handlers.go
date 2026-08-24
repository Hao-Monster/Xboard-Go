package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const noticePageSize = 5

func (s *server) listAdminNotices(w http.ResponseWriter, r *http.Request) {
	notices, err := s.store.ListNotices(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, notices)
}

func (s *server) listVisibleNotices(w http.ResponseWriter, r *http.Request) {
	page, ok := positiveQueryInt(w, r, "page", 1)
	if !ok {
		return
	}
	notices, total, err := s.store.ListVisibleNotices(r.Context(), page, noticePageSize)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, store.NoticePage{Items: notices, Total: total, Page: page, PageSize: noticePageSize})
}

func (s *server) createNotice(w http.ResponseWriter, r *http.Request) {
	input, _, ok := decodeNoticeInput(w, r, false)
	if !ok {
		return
	}
	notice, err := s.store.CreateNotice(r.Context(), input, s.now())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusCreated, notice)
}

func (s *server) updateNotice(w http.ResponseWriter, r *http.Request) {
	noticeID, ok := pathID(w, r, "noticeID")
	if !ok {
		return
	}
	input, revision, ok := decodeNoticeInput(w, r, true)
	if !ok {
		return
	}
	notice, err := s.store.UpdateNotice(r.Context(), noticeID, revision, input, s.now())
	if err != nil {
		handleNoticeMutationError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, notice)
}

func (s *server) setNoticeVisibility(w http.ResponseWriter, r *http.Request) {
	noticeID, ok := pathID(w, r, "noticeID")
	if !ok {
		return
	}
	var input struct {
		Revision int64 `json:"revision"`
		Show     bool  `json:"show"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	notice, err := s.store.SetNoticeVisibility(r.Context(), noticeID, input.Revision, input.Show, s.now())
	if err != nil {
		handleNoticeMutationError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, notice)
}

func (s *server) reorderNotices(w http.ResponseWriter, r *http.Request) {
	var input struct {
		IDs []int64 `json:"ids"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := s.store.ReorderNotices(r.Context(), input.IDs, s.now()); err != nil {
		handleNoticeMutationError(w, err)
		return
	}
	notices, err := s.store.ListNotices(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, notices)
}

func (s *server) deleteNotice(w http.ResponseWriter, r *http.Request) {
	noticeID, ok := pathID(w, r, "noticeID")
	if !ok {
		return
	}
	revision, ok := positiveQueryInt(w, r, "revision", 0)
	if !ok {
		return
	}
	if err := s.store.DeleteNotice(r.Context(), noticeID, int64(revision)); err != nil {
		handleNoticeMutationError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeNoticeInput(w http.ResponseWriter, r *http.Request, requireRevision bool) (store.SaveNoticeInput, int64, bool) {
	var input struct {
		Revision int64    `json:"revision,omitempty"`
		Title    string   `json:"title"`
		Content  string   `json:"content"`
		ImageURL string   `json:"image_url"`
		Tags     []string `json:"tags"`
		Show     bool     `json:"show"`
	}
	if !decodeJSON(w, r, &input) {
		return store.SaveNoticeInput{}, 0, false
	}
	if requireRevision && input.Revision <= 0 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "revision 必须为正整数", nil)
		return store.SaveNoticeInput{}, 0, false
	}
	return store.SaveNoticeInput{
		Title: input.Title, Content: input.Content, ImageURL: input.ImageURL, Tags: input.Tags, Visible: input.Show,
	}, input.Revision, true
}

func positiveQueryInt(w http.ResponseWriter, r *http.Request, name string, fallback int) (int, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		if fallback > 0 {
			return fallback, true
		}
		writeAPIError(w, http.StatusBadRequest, "invalid_query", name+" 必须为正整数", nil)
		return 0, false
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		writeAPIError(w, http.StatusBadRequest, "invalid_query", name+" 必须为正整数", nil)
		return 0, false
	}
	return value, true
}

func handleNoticeMutationError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrConflict) {
		writeAPIError(w, http.StatusConflict, "notice_conflict", "公告已被其他操作修改，请刷新后重试", nil)
		return
	}
	handleStoreError(w, err)
}
