package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
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
	Codes                         []invitationCodeResponse `json:"codes"`
	InvitedCount                  int64                    `json:"invited_count"`
	ValidCommission               int64                    `json:"valid_commission"`
	PendingCommission             int64                    `json:"pending_commission"`
	CommissionRate                int                      `json:"commission_rate"`
	CommissionDistributionEnabled bool                     `json:"commission_distribution_enabled"`
	CommissionDistributionRates   []int                    `json:"commission_distribution_rates"`
	AvailableCommission           int64                    `json:"available_commission"`
}

func (s *server) getInvitations(w http.ResponseWriter, r *http.Request) {
	if s.invitationProtector == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "invitation_unavailable", "邀请码服务暂不可用", nil)
		return
	}
	session, _ := sessionFromContext(r.Context())
	response, err := s.readInvitationSummary(r.Context(), session.UserID)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, response)
}

func (s *server) readInvitationSummary(ctx context.Context, userID int64) (invitationSummaryResponse, error) {
	summary, err := s.store.GetInvitationSummary(ctx, userID)
	if err != nil {
		return invitationSummaryResponse{}, err
	}
	codes := make([]invitationCodeResponse, 0, len(summary.Codes))
	for _, stored := range summary.Codes {
		plaintext, err := s.invitationProtector.DecryptCode(stored.OwnerID, stored.CodeCipher)
		if err != nil {
			return invitationSummaryResponse{}, err
		}
		code := string(plaintext)
		for index := range plaintext {
			plaintext[index] = 0
		}
		codes = append(codes, invitationCodeResponse{Code: code, PV: stored.PV, CreatedAt: stored.CreatedAt})
	}
	distributionRates := make([]int, len(summary.CommissionDistributionRates))
	copy(distributionRates, summary.CommissionDistributionRates)
	return invitationSummaryResponse{
		Codes: codes, InvitedCount: summary.InvitedCount, ValidCommission: summary.ValidCommission,
		PendingCommission: summary.PendingCommission, CommissionRate: summary.CommissionRate,
		CommissionDistributionEnabled: summary.CommissionDistributionEnabled,
		CommissionDistributionRates:   distributionRates,
		AvailableCommission:           summary.AvailableCommission,
	}, nil
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
	created, err := s.createInvitationForUser(r.Context(), session.UserID)
	if err != nil {
		writeInvitationCreateError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, created)
}

func (s *server) createInvitationForUser(ctx context.Context, userID int64) (invitationCodeResponse, error) {
	for range 5 {
		code, err := s.invitationProtector.NewCode()
		if err != nil {
			return invitationCodeResponse{}, err
		}
		digest, err := s.invitationProtector.CodeDigest(code)
		if err != nil {
			return invitationCodeResponse{}, err
		}
		ciphertext, err := s.invitationProtector.EncryptCode(userID, code)
		if err != nil {
			return invitationCodeResponse{}, err
		}
		created, err := s.store.CreateInvitationCode(ctx, userID, store.CreateInvitationCodeInput{
			CodeDigest: digest, CodeCipher: ciphertext,
		}, s.now())
		switch {
		case err == nil:
			return invitationCodeResponse{Code: code, PV: created.PV, CreatedAt: created.CreatedAt}, nil
		case errors.Is(err, store.ErrInvitationCodeLimit):
			return invitationCodeResponse{}, err
		case errors.Is(err, store.ErrInvitationCodeCollision):
			continue
		default:
			return invitationCodeResponse{}, err
		}
	}
	return invitationCodeResponse{}, store.ErrInvitationCodeCollision
}

func writeInvitationCreateError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrInvitationCodeLimit):
		writeAPIError(w, http.StatusBadRequest, "invitation_code_limit", "已达到创建数量上限", nil)
	case errors.Is(err, store.ErrInvitationCodeCollision):
		writeAPIError(w, http.StatusServiceUnavailable, "invitation_generation_busy", "邀请码生成繁忙，请稍后重试", nil)
	default:
		handleStoreError(w, err)
	}
}

func (s *server) listCommissionLogs(w http.ResponseWriter, r *http.Request) {
	page, ok := orderQueryInt(w, r, "page", 1, 1_000_000)
	if !ok {
		return
	}
	pageSize, ok := orderQueryInt(w, r, "page_size", 20, 100)
	if !ok {
		return
	}
	session, _ := sessionFromContext(r.Context())
	result, err := s.store.ListCommissionLogs(r.Context(), session.UserID, page, pageSize)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, result)
}

func (s *server) transferCommission(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Amount int64 `json:"amount"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Amount < 1 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "划转金额必须大于 0", map[string]string{"amount": "必须大于 0"})
		return
	}
	session, _ := sessionFromContext(r.Context())
	result, err := s.store.TransferCommission(r.Context(), session.UserID, input.Amount, s.now())
	if err != nil {
		writeCommissionTransferError(w, err, false)
		return
	}
	writeSuccess(w, http.StatusOK, result)
}

func (s *server) legacyGetInvitations(w http.ResponseWriter, r *http.Request) {
	if s.invitationProtector == nil {
		writeLegacyInviteFailure(w, http.StatusServiceUnavailable, "邀请码服务暂不可用")
		return
	}
	session, _ := sessionFromContext(r.Context())
	response, err := s.readInvitationSummary(r.Context(), session.UserID)
	if err != nil {
		writeLegacyInviteFailure(w, http.StatusInternalServerError, "邀请码请求失败")
		return
	}
	codes := make([]map[string]any, 0, len(response.Codes))
	for _, code := range response.Codes {
		codes = append(codes, map[string]any{
			"code": code.Code, "pv": code.PV, "status": 0,
			"created_at": code.CreatedAt.Unix(), "updated_at": code.CreatedAt.Unix(),
		})
	}
	writeLegacySuccess(w, http.StatusOK, map[string]any{
		"codes": codes,
		"stat": []any{response.InvitedCount, response.ValidCommission, response.PendingCommission,
			response.CommissionRate, response.AvailableCommission},
	})
}

func (s *server) legacyListCommissionLogs(w http.ResponseWriter, r *http.Request) {
	page, ok := orderQueryInt(w, r, "current", 1, 1_000_000)
	if !ok {
		return
	}
	pageSize, ok := orderQueryInt(w, r, "page_size", 10, 100)
	if !ok {
		return
	}
	session, _ := sessionFromContext(r.Context())
	result, err := s.store.ListCommissionLogs(r.Context(), session.UserID, page, pageSize)
	if err != nil {
		writeLegacyInviteFailure(w, http.StatusInternalServerError, "佣金记录请求失败")
		return
	}
	data := make([]map[string]any, 0, len(result.Items))
	for _, item := range result.Items {
		data = append(data, map[string]any{
			"id": item.ID, "order_amount": item.OrderAmount, "trade_no": item.TradeNo,
			"get_amount": item.GetAmount, "created_at": item.CreatedAt.Unix(),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data, "total": result.Total})
}

func (s *server) legacyCreateInvitation(w http.ResponseWriter, r *http.Request) {
	if s.invitationProtector == nil {
		writeLegacyInviteFailure(w, http.StatusServiceUnavailable, "邀请码服务暂不可用")
		return
	}
	session, _ := sessionFromContext(r.Context())
	if session.CredentialKind != store.CredentialKindAccessToken {
		writeLegacyInviteFailure(w, http.StatusForbidden, "旧版邀请码生成仅支持访问令牌")
		return
	}
	if _, err := s.createInvitationForUser(r.Context(), session.UserID); err != nil {
		if errors.Is(err, store.ErrInvitationCodeLimit) {
			writeLegacyInviteFailure(w, http.StatusBadRequest, "已达到创建数量上限")
			return
		}
		writeLegacyInviteFailure(w, http.StatusServiceUnavailable, "邀请码生成繁忙，请稍后重试")
		return
	}
	writeLegacySuccess(w, http.StatusOK, true)
}

func (s *server) legacyTransferCommission(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := r.ParseForm(); err != nil || len(r.PostForm) != 1 {
		writeLegacyInviteFailure(w, http.StatusUnprocessableEntity, "划转金额参数错误")
		return
	}
	amount, err := strconv.ParseInt(strings.TrimSpace(r.PostForm.Get("transfer_amount")), 10, 64)
	if err != nil || amount < 1 {
		writeLegacyInviteFailure(w, http.StatusUnprocessableEntity, "划转金额参数错误")
		return
	}
	session, _ := sessionFromContext(r.Context())
	if _, err := s.store.TransferCommission(r.Context(), session.UserID, amount, s.now()); err != nil {
		writeCommissionTransferError(w, err, true)
		return
	}
	writeLegacySuccess(w, http.StatusOK, true)
}

func writeCommissionTransferError(w http.ResponseWriter, err error, legacy bool) {
	if errors.Is(err, store.ErrInsufficientCommission) {
		if legacy {
			writeLegacyInviteFailure(w, http.StatusBadRequest, "佣金余额不足")
		} else {
			writeAPIError(w, http.StatusConflict, "insufficient_commission", "佣金余额不足", nil)
		}
		return
	}
	if legacy {
		writeLegacyInviteFailure(w, http.StatusBadRequest, "划转失败")
		return
	}
	handleStoreError(w, err)
}

func writeLegacyInviteFailure(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"status": "fail", "message": message, "data": nil, "error": nil})
}

func (s *server) recordInvitationView(w http.ResponseWriter, r *http.Request) {
	s.recordInvitationViewResponse(w, r, false)
}

func (s *server) legacyRecordInvitationView(w http.ResponseWriter, r *http.Request) {
	s.recordInvitationViewResponse(w, r, true)
}

func (s *server) recordInvitationViewResponse(w http.ResponseWriter, r *http.Request, legacy bool) {
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
	if legacy {
		writeLegacySuccess(w, http.StatusOK, true)
		return
	}
	writeSuccess(w, http.StatusOK, true)
}
