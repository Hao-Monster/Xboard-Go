package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

type invitationCodeResponse struct {
	Code      string    `json:"code"`
	PV        int64     `json:"pv"`
	CreatedAt time.Time `json:"created_at"`
}

type invitationSummaryResponse struct {
	Codes        []invitationCodeResponse `json:"codes"`
	InvitedCount int64                    `json:"invited_count"`
}

func (s *server) getInvitations(w http.ResponseWriter, r *http.Request) {
	if s.invitationProtector == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "invitation_unavailable", "邀请码服务暂不可用", nil)
		return
	}
	session, _ := sessionFromContext(r.Context())
	summary, err := s.store.GetInvitationSummary(r.Context(), session.UserID)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	codes := make([]invitationCodeResponse, 0, len(summary.Codes))
	for _, stored := range summary.Codes {
		plaintext, err := s.invitationProtector.DecryptCode(stored.OwnerID, stored.CodeCipher)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "invitation_decryption_failed", "邀请码数据无法解密", nil)
			return
		}
		code := string(plaintext)
		for index := range plaintext {
			plaintext[index] = 0
		}
		codes = append(codes, invitationCodeResponse{Code: code, PV: stored.PV, CreatedAt: stored.CreatedAt})
	}
	writeSuccess(w, http.StatusOK, invitationSummaryResponse{Codes: codes, InvitedCount: summary.InvitedCount})
}

func (s *server) createInvitation(w http.ResponseWriter, r *http.Request) {
	if !decodeJSON(w, r, &struct{}{}) {
		return
	}
	if s.invitationProtector == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "invitation_unavailable", "邀请码服务暂不可用", nil)
		return
	}
	session, _ := sessionFromContext(r.Context())
	for range 5 {
		code, err := s.invitationProtector.NewCode()
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
			return
		}
		digest, err := s.invitationProtector.CodeDigest(code)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
			return
		}
		ciphertext, err := s.invitationProtector.EncryptCode(session.UserID, code)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
			return
		}
		created, err := s.store.CreateInvitationCode(r.Context(), session.UserID, store.CreateInvitationCodeInput{
			CodeDigest: digest, CodeCipher: ciphertext,
		}, s.now())
		switch {
		case err == nil:
			writeSuccess(w, http.StatusOK, invitationCodeResponse{Code: code, PV: created.PV, CreatedAt: created.CreatedAt})
			return
		case errors.Is(err, store.ErrInvitationCodeLimit):
			writeAPIError(w, http.StatusBadRequest, "invitation_code_limit", "已达到创建数量上限", nil)
			return
		case errors.Is(err, store.ErrInvitationCodeCollision):
			continue
		default:
			handleStoreError(w, err)
			return
		}
	}
	writeAPIError(w, http.StatusServiceUnavailable, "invitation_generation_busy", "邀请码生成繁忙，请稍后重试", nil)
}

func (s *server) recordInvitationView(w http.ResponseWriter, r *http.Request) {
	if !s.invitationViewRequests.take(requestIP(r), s.now()) {
		w.Header().Set("Retry-After", "900")
		writeAPIError(w, http.StatusTooManyRequests, "invitation_view_rate_limited", "请求过于频繁，请稍后重试", nil)
		return
	}
	var input struct {
		InvitationCode string `json:"invite_code"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if s.invitationProtector != nil && security.ValidInvitationCode(input.InvitationCode) {
		digest, err := s.invitationProtector.CodeDigest(input.InvitationCode)
		if err == nil {
			if err := s.store.IncrementInvitationCodeView(r.Context(), digest, s.now()); err != nil {
				handleStoreError(w, err)
				return
			}
		}
	}
	writeSuccess(w, http.StatusOK, true)
}
