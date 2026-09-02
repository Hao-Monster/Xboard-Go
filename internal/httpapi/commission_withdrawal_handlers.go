package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	appsettings "github.com/Hao-Monster/Xboard-Go/internal/settings"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func (s *server) getCommissionWithdrawalPolicy(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	policy, err := s.store.GetCommissionWithdrawalPolicy(r.Context(), session.UserID)
	if err != nil {
		writeCommissionWithdrawalError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, policy)
}

func (s *server) listCommissionWithdrawals(w http.ResponseWriter, r *http.Request) {
	page, ok := orderQueryInt(w, r, "page", 1, 1_000_000)
	if !ok {
		return
	}
	pageSize, ok := orderQueryInt(w, r, "page_size", 20, 100)
	if !ok {
		return
	}
	session, _ := sessionFromContext(r.Context())
	result, err := s.store.ListCommissionWithdrawals(r.Context(), session.UserID, page, pageSize)
	if err != nil {
		writeCommissionWithdrawalError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, result)
}

func (s *server) createCommissionWithdrawal(w http.ResponseWriter, r *http.Request) {
	var input struct {
		IdempotencyKey string `json:"idempotency_key"`
		Method         string `json:"method"`
		Account        string `json:"account"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	input.Method = strings.TrimSpace(input.Method)
	input.Account = strings.TrimSpace(input.Account)
	if len(input.IdempotencyKey) < 16 || len(input.IdempotencyKey) > 128 || len(input.Method) < 1 || len(input.Method) > 128 ||
		!utf8.ValidString(input.Account) || len(input.Account) < 1 || len(input.Account) > 320 || strings.IndexFunc(input.Account, unicode.IsControl) >= 0 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "提现方式、账户或幂等键无效", nil)
		return
	}
	if s.settingsCipher == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "withdrawal_encryption_unavailable", "提现账户加密服务暂不可用", nil)
		return
	}
	plaintext := []byte(input.Account)
	ciphertext, encryptErr := s.settingsCipher.EncryptFor(appsettings.CommissionWithdrawalAccountPurpose, plaintext)
	fingerprint, fingerprintErr := s.settingsCipher.FingerprintFor(appsettings.CommissionWithdrawalAccountPurpose, plaintext)
	for index := range plaintext {
		plaintext[index] = 0
	}
	if encryptErr != nil || fingerprintErr != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "withdrawal_encryption_unavailable", "提现账户加密服务暂不可用", nil)
		return
	}
	session, _ := sessionFromContext(r.Context())
	created, err := s.store.CreateCommissionWithdrawal(r.Context(), session.UserID, store.CreateCommissionWithdrawalInput{
		IdempotencyKey: input.IdempotencyKey, Method: input.Method, AccountCipher: ciphertext,
		AccountFingerprint: fingerprint, AccountMasked: maskWithdrawalAccount(input.Account),
	}, s.now())
	if err != nil {
		writeCommissionWithdrawalError(w, err)
		return
	}
	writeSuccess(w, http.StatusCreated, created)
}

func (s *server) listAdminCommissionWithdrawals(w http.ResponseWriter, r *http.Request) {
	page, ok := orderQueryInt(w, r, "page", 1, 1_000_000)
	if !ok {
		return
	}
	pageSize, ok := orderQueryInt(w, r, "page_size", 50, 100)
	if !ok {
		return
	}
	result, err := s.store.ListAdminCommissionWithdrawals(r.Context(), r.URL.Query().Get("status"), page, pageSize)
	if err != nil {
		writeCommissionWithdrawalError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, result)
}

func (s *server) revealAdminCommissionWithdrawalAccount(w http.ResponseWriter, r *http.Request) {
	withdrawalID, ok := pathID(w, r, "withdrawalID")
	if !ok {
		return
	}
	if s.settingsCipher == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "withdrawal_encryption_unavailable", "提现账户加密服务暂不可用", nil)
		return
	}
	ciphertext, err := s.store.GetCommissionWithdrawalAccountCipher(r.Context(), withdrawalID)
	if err != nil {
		writeCommissionWithdrawalError(w, err)
		return
	}
	plaintext, err := s.settingsCipher.DecryptFor(appsettings.CommissionWithdrawalAccountPurpose, ciphertext)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "withdrawal_account_unavailable", "提现账户无法解密", nil)
		return
	}
	defer func() {
		for index := range plaintext {
			plaintext[index] = 0
		}
	}()
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	writeSuccess(w, http.StatusOK, map[string]string{"account": string(plaintext)})
}

func (s *server) approveCommissionWithdrawal(w http.ResponseWriter, r *http.Request) {
	s.transitionCommissionWithdrawal(w, r, "approve")
}

func (s *server) rejectCommissionWithdrawal(w http.ResponseWriter, r *http.Request) {
	s.transitionCommissionWithdrawal(w, r, "reject")
}

func (s *server) payCommissionWithdrawal(w http.ResponseWriter, r *http.Request) {
	s.transitionCommissionWithdrawal(w, r, "pay")
}

func (s *server) transitionCommissionWithdrawal(w http.ResponseWriter, r *http.Request, action string) {
	withdrawalID, ok := pathID(w, r, "withdrawalID")
	if !ok {
		return
	}
	var input struct {
		Revision          int64  `json:"revision"`
		Reason            string `json:"reason"`
		ExternalReference string `json:"external_reference"`
		Confirm           bool   `json:"confirm"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Revision < 1 || !input.Confirm {
		writeAPIError(w, http.StatusUnprocessableEntity, "confirmation_required", "需要当前 revision 和二次确认", nil)
		return
	}
	session, _ := sessionFromContext(r.Context())
	var result store.CommissionWithdrawal
	var err error
	switch action {
	case "approve":
		result, err = s.store.ApproveCommissionWithdrawal(r.Context(), session.UserID, withdrawalID, input.Revision, s.now())
	case "reject":
		result, err = s.store.RejectCommissionWithdrawal(r.Context(), session.UserID, withdrawalID, input.Revision, input.Reason, s.now())
	case "pay":
		result, err = s.store.PayCommissionWithdrawal(r.Context(), session.UserID, withdrawalID, input.Revision, input.ExternalReference, s.now())
	}
	if err != nil {
		writeCommissionWithdrawalError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, result)
}

func writeCommissionWithdrawalError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrCommissionWithdrawalActive):
		writeAPIError(w, http.StatusConflict, "withdrawal_active", "已有待处理的提现申请", nil)
	case errors.Is(err, store.ErrCommissionWithdrawalMethod):
		writeAPIError(w, http.StatusUnprocessableEntity, "withdrawal_method_not_allowed", "提现方式不在允许列表中", nil)
	case errors.Is(err, store.ErrCommissionWithdrawalLimit):
		writeAPIError(w, http.StatusConflict, "withdrawal_below_minimum", "可提现佣金未达到最低金额", nil)
	case errors.Is(err, store.ErrInsufficientCommission):
		writeAPIError(w, http.StatusConflict, "insufficient_commission", "没有可提现佣金", nil)
	case errors.Is(err, store.ErrCommissionWithdrawalState):
		writeAPIError(w, http.StatusConflict, "withdrawal_state_conflict", "提现状态不允许此操作", nil)
	default:
		handleStoreError(w, err)
	}
}

func maskWithdrawalAccount(value string) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= 4 {
		return strings.Repeat("*", len(runes))
	}
	return "****" + string(runes[len(runes)-4:])
}
