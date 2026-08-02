package alertmanager

import (
	"encoding/json"
	"net/http"

	"github.com/go-logr/logr"

	"github.com/user27c/aegisops/internal/observability"
)

// Handler 是 Alertmanager Webhook 的 HTTP 处理链。
type Handler struct {
	service  *Service
	auth     TokenValidator
	logger   logr.Logger
	maxBytes int64
}

// NewHandler 构造 Webhook 处理器。
// 中间件顺序：Request ID → OTel → Recover → Body Limit → Bearer Auth → Handler。
func NewHandler(svc *Service, auth TokenValidator, logger logr.Logger, maxBytes int64) http.Handler {
	h := &Handler{service: svc, auth: auth, logger: logger, maxBytes: maxBytes}

	var mux http.Handler = http.HandlerFunc(h.handleAlertmanager)
	mux = withRequestID(mux)
	mux = observability.OTelHTTPMiddleware("alert-gateway")(mux)
	mux = withRecover(mux, logger)
	mux = withBodyLimit(mux, maxBytes)
	mux = withBearerAuth(mux, auth)

	return mux
}

// handleAlertmanager 处理 POST /webhooks/alertmanager。
func (h *Handler) handleAlertmanager(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "仅支持 POST"})
		return
	}

	hook, err := DecodeWebhook(r.Body, h.maxBytes)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "非法 Webhook: " + err.Error()})
		return
	}

	result, err := h.service.Process(r.Context(), hook)
	if err != nil {
		h.logger.Error(err, "处理 Webhook 失败")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "处理失败"})
		return
	}

	// 部分失败仍返回 202，但 rejected > 0 并写指标。
	writeJSON(w, http.StatusAccepted, result)
}

// writeJSON 写出 JSON 响应。
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
