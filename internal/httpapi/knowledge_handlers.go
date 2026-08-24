package httpapi

import (
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	knowledgecontent "github.com/Hao-Monster/Xboard-Go/internal/knowledge"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const knowledgeSiteName = "Xboard-Go"

type knowledgeResponse struct {
	ID           int64     `json:"id"`
	Language     string    `json:"language"`
	Category     string    `json:"category"`
	Title        string    `json:"title"`
	Body         string    `json:"body,omitempty"`
	SortPosition int       `json:"sort"`
	Visible      bool      `json:"show"`
	Revision     int64     `json:"revision"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	ShareURL     string    `json:"share_url"`
}

func (s *server) listAdminKnowledge(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListKnowledge(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, s.knowledgeResponses(items, false))
}

func (s *server) getAdminKnowledge(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "knowledgeID")
	if !ok {
		return
	}
	item, err := s.store.GetKnowledge(r.Context(), id)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, s.knowledgeResponse(item, true))
}

func (s *server) listKnowledgeCategories(w http.ResponseWriter, r *http.Request) {
	categories, err := s.store.ListKnowledgeCategories(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, categories)
}

func (s *server) createKnowledge(w http.ResponseWriter, r *http.Request) {
	input, _, ok := decodeKnowledgeInput(w, r, false)
	if !ok {
		return
	}
	item, err := s.store.CreateKnowledge(r.Context(), input, s.now())
	if err != nil {
		handleKnowledgeMutationError(w, err)
		return
	}
	writeSuccess(w, http.StatusCreated, s.knowledgeResponse(item, true))
}

func (s *server) updateKnowledge(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "knowledgeID")
	if !ok {
		return
	}
	input, revision, ok := decodeKnowledgeInput(w, r, true)
	if !ok {
		return
	}
	item, err := s.store.UpdateKnowledge(r.Context(), id, revision, input, s.now())
	if err != nil {
		handleKnowledgeMutationError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, s.knowledgeResponse(item, true))
}

func (s *server) setKnowledgeVisibility(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "knowledgeID")
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
	item, err := s.store.SetKnowledgeVisibility(r.Context(), id, input.Revision, input.Show, s.now())
	if err != nil {
		handleKnowledgeMutationError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, s.knowledgeResponse(item, true))
}

func (s *server) reorderKnowledge(w http.ResponseWriter, r *http.Request) {
	var input struct {
		IDs []int64 `json:"ids"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := s.store.ReorderKnowledge(r.Context(), input.IDs, s.now()); err != nil {
		handleKnowledgeMutationError(w, err)
		return
	}
	items, err := s.store.ListKnowledge(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, s.knowledgeResponses(items, false))
}

func (s *server) deleteKnowledge(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "knowledgeID")
	if !ok {
		return
	}
	revision, ok := positiveQueryInt(w, r, "revision", 0)
	if !ok {
		return
	}
	if err := s.store.DeleteKnowledge(r.Context(), id, int64(revision)); err != nil {
		handleKnowledgeMutationError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) listUserKnowledge(w http.ResponseWriter, r *http.Request) {
	language := strings.TrimSpace(r.URL.Query().Get("language"))
	keyword := strings.TrimSpace(r.URL.Query().Get("keyword"))
	items, err := s.store.ListVisibleKnowledge(r.Context(), language, keyword)
	if err != nil {
		if errors.Is(err, store.ErrInvalidInput) {
			writeAPIError(w, http.StatusBadRequest, "invalid_query", "语言或搜索条件无效", nil)
			return
		}
		handleStoreError(w, err)
		return
	}
	viewer, ok := s.knowledgeViewer(w, r)
	if !ok {
		return
	}
	responses := make([]knowledgeResponse, 0, len(items))
	for _, item := range items {
		item.Body = knowledgecontent.UserContent(item.Body, knowledgeSiteName, s.subscriptionURL(viewer.SubscriptionToken), viewer.SubscriptionValid)
		responses = append(responses, s.knowledgeResponse(item, true))
	}
	writeSuccess(w, http.StatusOK, responses)
}

func (s *server) getUserKnowledge(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "knowledgeID")
	if !ok {
		return
	}
	item, err := s.store.GetVisibleKnowledge(r.Context(), id)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	viewer, ok := s.knowledgeViewer(w, r)
	if !ok {
		return
	}
	item.Body = knowledgecontent.UserContent(item.Body, knowledgeSiteName, s.subscriptionURL(viewer.SubscriptionToken), viewer.SubscriptionValid)
	writeSuccess(w, http.StatusOK, s.knowledgeResponse(item, true))
}

func (s *server) knowledgeViewer(w http.ResponseWriter, r *http.Request) (store.KnowledgeViewer, bool) {
	session, ok := sessionFromContext(r.Context())
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "unauthenticated", "请先登录", nil)
		return store.KnowledgeViewer{}, false
	}
	viewer, err := s.store.GetKnowledgeViewer(r.Context(), session.UserID, s.now())
	if err != nil {
		handleStoreError(w, err)
		return store.KnowledgeViewer{}, false
	}
	return viewer, true
}

func (s *server) publicKnowledge(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "knowledgeID")
	if !ok {
		return
	}
	item, err := s.store.GetVisibleKnowledge(r.Context(), id)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	canonical := s.knowledgeShareURL(item)
	tail := r.PathValue("tail")
	if tail == "content" {
		s.publicKnowledgeContent(w, r, item, canonical)
		return
	}
	if tail == "" || tail != knowledgecontent.Slug(item.Title) {
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, canonical, http.StatusFound)
		return
	}

	body := knowledgecontent.PublicContent(item.Body, knowledgeSiteName, s.panelURL+"/#/login")
	document, err := knowledgecontent.RenderPublic(body)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "knowledge_render_failed", "知识文章暂时无法显示", nil)
		return
	}
	navigation, err := s.store.ListVisibleKnowledgeNavigation(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	page := publicKnowledgePageData{
		AppName: knowledgeSiteName, Article: item, Body: template.HTML(document.HTML), TOC: document.TOC,
		CanonicalURL: canonical, Articles: s.publicKnowledgeNavigation(navigation, item.ID),
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: https: http:; style-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'")
	if err := publicKnowledgePage.Execute(w, page); err != nil {
		s.logger.Error("render public knowledge page", "knowledge_id", item.ID, "error", err)
	}
}

func (s *server) publicKnowledgeContent(w http.ResponseWriter, r *http.Request, item store.Knowledge, canonical string) {
	body := knowledgecontent.PublicContent(item.Body, knowledgeSiteName, s.panelURL+"/#/login")
	document, err := knowledgecontent.RenderPublic(body)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "knowledge_render_failed", "知识文章暂时无法显示", nil)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeSuccess(w, http.StatusOK, map[string]any{
		"id": item.ID, "title": item.Title, "updated_at": item.UpdatedAt.Format("2006-01-02 15:04"),
		"body": document.HTML, "toc": document.TOC, "share_url": canonical, "page_title": item.Title + " - " + knowledgeSiteName,
	})
}

func decodeKnowledgeInput(w http.ResponseWriter, r *http.Request, requireRevision bool) (store.SaveKnowledgeInput, int64, bool) {
	var input struct {
		Revision int64  `json:"revision,omitempty"`
		Language string `json:"language"`
		Category string `json:"category"`
		Title    string `json:"title"`
		Body     string `json:"body"`
		Show     bool   `json:"show"`
	}
	if !decodeJSON(w, r, &input) {
		return store.SaveKnowledgeInput{}, 0, false
	}
	if requireRevision && input.Revision < 1 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "revision 必须为正整数", nil)
		return store.SaveKnowledgeInput{}, 0, false
	}
	return store.SaveKnowledgeInput{Language: input.Language, Category: input.Category, Title: input.Title, Body: input.Body, Visible: input.Show}, input.Revision, true
}

func (s *server) knowledgeResponse(item store.Knowledge, includeBody bool) knowledgeResponse {
	response := knowledgeResponse{
		ID: item.ID, Language: item.Language, Category: item.Category, Title: item.Title,
		SortPosition: item.SortPosition, Visible: item.Visible, Revision: item.Revision,
		CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt, ShareURL: s.knowledgeShareURL(item),
	}
	if includeBody {
		response.Body = item.Body
	}
	return response
}

func (s *server) knowledgeResponses(items []store.Knowledge, includeBody bool) []knowledgeResponse {
	responses := make([]knowledgeResponse, 0, len(items))
	for _, item := range items {
		responses = append(responses, s.knowledgeResponse(item, includeBody))
	}
	return responses
}

func (s *server) knowledgeShareURL(item store.Knowledge) string {
	return fmt.Sprintf("%s/guide/%d/%s", s.panelURL, item.ID, knowledgecontent.Slug(item.Title))
}

func (s *server) subscriptionURL(token string) string {
	return s.panelURL + "/api/v1/client/subscribe?token=" + url.QueryEscape(token)
}

func handleKnowledgeMutationError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrConflict) {
		writeAPIError(w, http.StatusConflict, "knowledge_conflict", "知识文章已被其他操作修改，请刷新后重试", nil)
		return
	}
	if errors.Is(err, store.ErrInvalidInput) {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "请检查知识文章字段", nil)
		return
	}
	handleStoreError(w, err)
}

type publicKnowledgeNavigationItem struct {
	Category string
	Title    string
	URL      string
	Current  bool
}

func (s *server) publicKnowledgeNavigation(items []store.Knowledge, currentID int64) []publicKnowledgeNavigationItem {
	result := make([]publicKnowledgeNavigationItem, 0, len(items))
	for _, item := range items {
		result = append(result, publicKnowledgeNavigationItem{
			Category: item.Category, Title: item.Title, URL: s.knowledgeShareURL(item), Current: item.ID == currentID,
		})
	}
	return result
}

type publicKnowledgePageData struct {
	AppName      string
	Article      store.Knowledge
	Body         template.HTML
	TOC          []knowledgecontent.TOCEntry
	CanonicalURL string
	Articles     []publicKnowledgeNavigationItem
}

var publicKnowledgePage = template.Must(template.New("public-knowledge").Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Article.Title}} - {{.AppName}}</title><link rel="canonical" href="{{.CanonicalURL}}"><link rel="stylesheet" href="/public-knowledge.css"></head>
<body><header class="public-knowledge-header"><a href="/" class="public-knowledge-brand">{{.AppName}}</a><span>使用指南</span></header>
<div class="public-knowledge-layout"><aside class="public-knowledge-articles" aria-label="文章列表"><h2>知识库</h2><nav>{{range .Articles}}<a href="{{.URL}}"{{if .Current}} aria-current="page"{{end}}><small>{{.Category}}</small>{{.Title}}</a>{{else}}<p>暂无文章</p>{{end}}</nav></aside>
<main class="public-knowledge-main"><article><header><p class="public-knowledge-category">{{.Article.Category}} · {{.Article.Language}}</p><h1>{{.Article.Title}}</h1><time datetime="{{.Article.UpdatedAt.Format "2006-01-02T15:04:05Z07:00"}}">更新于 {{.Article.UpdatedAt.Format "2006-01-02 15:04"}}</time></header><div class="public-knowledge-body">{{.Body}}</div></article></main>
<aside class="public-knowledge-toc" aria-label="本文目录"><h2>本文目录</h2><nav>{{range .TOC}}<a class="toc-level-{{.Level}}" href="#{{.ID}}">{{.Title}}</a>{{else}}<p>暂无目录</p>{{end}}</nav></aside></div></body></html>`))
