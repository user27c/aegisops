package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

// Handlers 是 /api/v1 路由的处理器集合。
type Handlers struct {
	k8s       client.Client
	now       func() time.Time
	diagnosis DiagnosisReader
}

// errInvalidLimit 是非法分页参数错误。
var errInvalidLimit = errors.New("limit 必须是 1-500 的整数")

// ListIncidents GET /api/v1/incidents。
// 分页语义（M9.4）：按 namespace 分页 List → 服务端过滤 phase/severity →
// 返回不透明游标；过滤条件变化时游标失效（400）。
func (h *Handlers) ListIncidents(w http.ResponseWriter, r *http.Request) {
	opts, err := parseListOptions(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_QUERY", err.Error())
		return
	}
	var cont string
	var skipMatched int
	if opts.Continue != "" {
		cur, err := decodeCursor(opts.Continue)
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_CURSOR", errInvalidCursor.Error())
			return
		}
		if err := validateCursor(cur, opts); err != nil {
			writeError(w, http.StatusBadRequest, "FILTER_CHANGED", errFilterChanged.Error())
			return
		}
		cont = cur.Continue
		skipMatched = cur.SkipMatched
	}

	page := IncidentPage{Items: make([]IncidentDTO, 0, opts.Limit)}
	scanned := int64(0)
	pageSize := opts.Limit
	if pageSize < 1 {
		pageSize = 100
	}

	for int64(len(page.Items)) < opts.Limit && scanned < maxCursorScan {
		pageStart := cont
		list := &opsv1alpha1.AIOpsIncidentList{}
		clientOpts := []client.ListOption{
			client.Limit(pageSize),
		}
		if opts.Namespace != "" {
			clientOpts = append(clientOpts, client.InNamespace(opts.Namespace))
		}
		if cont != "" {
			clientOpts = append(clientOpts, client.Continue(cont))
		}
		if err := h.k8s.List(r.Context(), list, clientOpts...); err != nil {
			writeError(w, http.StatusInternalServerError, "LIST_FAILED", "列表查询失败")
			return
		}
		scanned += int64(len(list.Items))
		if len(list.Items) == 0 {
			cont = ""
			break
		}
		matched := 0
		collected := 0
		overflow := false
		for idx := range list.Items {
			incident := &list.Items[idx]
			if opts.Phase != "" && string(incident.Status.Phase) != opts.Phase {
				continue
			}
			if opts.Severity != "" && incident.Spec.Severity != opts.Severity {
				continue
			}
			matched++
			if matched <= skipMatched {
				continue
			}
			page.Items = append(page.Items, ToIncidentDTO(incident))
			collected++
			if int64(len(page.Items)) >= opts.Limit {
				overflow = true
				break
			}
		}
		if overflow {
			cont = pageStart
			skipMatched += collected
			break
		}
		cont = list.Continue
		skipMatched = 0
		if cont == "" {
			break
		}
	}

	if cont != "" || skipMatched > 0 {
		page.ContinueToken = encodeCursor(listCursor{
			Version:     cursorVersion,
			Namespace:   opts.Namespace,
			Phase:       opts.Phase,
			Severity:    opts.Severity,
			Continue:    cont,
			SkipMatched: skipMatched,
			FilterHash:  filterHashOf(opts.Namespace, opts.Phase, opts.Severity),
		})
	}
	writeJSON(w, http.StatusOK, page)
}

// GetIncident GET /api/v1/incidents/{namespace}/{name}。
func (h *Handlers) GetIncident(w http.ResponseWriter, r *http.Request) {
	namespace := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	incident := &opsv1alpha1.AIOpsIncident{}
	if err := h.k8s.Get(r.Context(), client.ObjectKey{Namespace: namespace, Name: name}, incident); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "事故不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, "GET_FAILED", "查询失败")
		return
	}
	writeJSON(w, http.StatusOK, ToIncidentDTO(incident))
}

// ListPolicies GET /api/v1/policies。
func (h *Handlers) ListPolicies(w http.ResponseWriter, r *http.Request) {
	list := &opsv1alpha1.RemediationPolicyList{}
	clientOpts := []client.ListOption{}
	if ns := r.URL.Query().Get("namespace"); ns != "" {
		clientOpts = append(clientOpts, client.InNamespace(ns))
	}
	if err := h.k8s.List(r.Context(), list, clientOpts...); err != nil {
		writeError(w, http.StatusInternalServerError, "LIST_FAILED", "策略查询失败")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// writeJSON 写出 JSON。
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// writeError 写出错误响应。
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"code": code, "message": message})
}

// parseListOptions 解析分页与过滤参数。
func parseListOptions(r *http.Request) (ListOptions, error) {
	q := r.URL.Query()
	opts := ListOptions{
		Namespace: q.Get("namespace"),
		Phase:     q.Get("phase"),
		Severity:  q.Get("severity"),
		Continue:  q.Get("continue"),
		Limit:     100,
	}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 1 || n > 500 {
			return opts, errInvalidLimit
		}
		opts.Limit = n
	}
	return opts, nil
}

// ListOptions 是列表查询参数。
type ListOptions struct {
	Namespace string
	Phase     string
	Severity  string
	Limit     int64
	Continue  string
}
