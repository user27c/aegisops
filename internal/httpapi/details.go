package httpapi

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

// TimelineResponse 是时间线响应；诊断服务不可用时回退 CR 时间线并标记。
type TimelineResponse struct {
	Items              []TimelineEntryDTO `json:"items"`
	DetailsUnavailable bool               `json:"detailsUnavailable"`
	Source             string             `json:"source"` // audit | cr
}

// EvidenceResponse 是证据详情响应；诊断服务不可用时降级并标记。
type EvidenceResponse struct {
	EvidenceDetail
	DetailsUnavailable bool `json:"detailsUnavailable"`
}

// GetIncidentTimeline GET /api/v1/incidents/{ns}/{name}/timeline。
// 优先返回诊断服务审计时间线（含 actor/sequence/hash）；不可用时回退 CR 时间线。
func (h *Handlers) GetIncidentTimeline(w http.ResponseWriter, r *http.Request) {
	incident, err := h.getIncident(r)
	if err != nil {
		writeIncidentErr(w, err)
		return
	}

	resp := TimelineResponse{Items: []TimelineEntryDTO{}, Source: "cr"}
	for _, e := range incident.Status.Timeline {
		resp.Items = append(resp.Items, TimelineEntryDTO{
			Time:    e.Time.Time,
			Type:    e.Type,
			Reason:  e.Reason,
			Message: e.Message,
		})
	}

	if h.diagnosis != nil {
		entries, err := h.diagnosis.GetTimeline(r.Context(), string(incident.UID))
		switch {
		case err == nil:
			for i := range entries {
				entries[i].EventHash = truncateEventHash(entries[i].EventHash)
			}
			resp.Items = entries
			resp.Source = "audit"
			resp.DetailsUnavailable = false
		case errors.Is(err, ErrDiagnosisNotFound):
			// 审计无该事故事件：保留 CR 时间线，不标记不可用。
			resp.Source = "cr"
		default:
			resp.DetailsUnavailable = true
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func truncateEventHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

// GetIncidentEvidence GET /api/v1/incidents/{ns}/{name}/evidence。
// 经诊断服务读取脱敏证据详情；证据缺失时 404，服务不可用时降级。
func (h *Handlers) GetIncidentEvidence(w http.ResponseWriter, r *http.Request) {
	incident, err := h.getIncident(r)
	if err != nil {
		writeIncidentErr(w, err)
		return
	}

	resp := EvidenceResponse{
		EvidenceDetail: EvidenceDetail{Items: []EvidenceItemDetail{}},
	}
	if h.diagnosis == nil {
		resp.DetailsUnavailable = true
		if incident.Status.Evidence != nil {
			resp.ID = incident.Status.Evidence.ID
			resp.Hash = incident.Status.Evidence.Hash
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}
	if incident.Status.Evidence == nil || incident.Status.Evidence.ID == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "该事故尚无证据")
		return
	}
	evidenceID := incident.Status.Evidence.ID
	resp.ID = incident.Status.Evidence.ID
	resp.Hash = incident.Status.Evidence.Hash
	detail, err := h.diagnosis.GetEvidence(r.Context(), evidenceID)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, detail)
	case errors.Is(err, ErrDiagnosisNotFound):
		writeError(w, http.StatusNotFound, "NOT_FOUND", "证据不存在")
	default:
		resp.DetailsUnavailable = true
		writeJSON(w, http.StatusOK, resp)
	}
}

func (h *Handlers) getIncident(r *http.Request) (*opsv1alpha1.AIOpsIncident, error) {
	namespace := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	incident := &opsv1alpha1.AIOpsIncident{}
	if err := h.k8s.Get(r.Context(), client.ObjectKey{Namespace: namespace, Name: name}, incident); err != nil {
		return nil, err
	}
	return incident, nil
}

func writeIncidentErr(w http.ResponseWriter, err error) {
	if apierrors.IsNotFound(err) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "事故不存在")
		return
	}
	writeError(w, http.StatusInternalServerError, "GET_FAILED", "查询失败")
}
