package httpapi

import "net/http"

// Verification and hashing share the registration/reset/batch slot pool.
// Acquire once around a synchronous credential operation, not once per hash:
// login may need to upgrade a hash and retry a concurrent state change.
func (s *server) beginPasswordWork(w http.ResponseWriter, legacy bool) (func(), bool) {
	release, ok := s.beginPasswordHash()
	if !ok {
		w.Header().Set("Retry-After", "1")
		if legacy {
			writeLegacyOrderFail(w, http.StatusTooManyRequests, "密码服务繁忙，请稍后重试")
		} else {
			writeAPIError(w, http.StatusTooManyRequests, "password_work_busy", "密码服务繁忙，请稍后重试", nil)
		}
	}
	return release, ok
}
