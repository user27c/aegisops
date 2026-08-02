package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

// ApprovalRequest 是审批请求体。
type ApprovalRequest struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

// approvalTTL 是审批有效期（由 Incident API 决定，与 Policy 的 ApprovalTTL 对齐）。
var approvalTTL = 10 * time.Minute

// ApproveIncident POST /api/v1/incidents/{namespace}/{name}/approval。
// 仅 approver 角色可调用（在路由注册处检查）。
// 服务器从 Incident Status 复制 planDigest/proposalRevision，不接受客户端提交。
func (h *Handlers) ApproveIncident(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "未认证")
		return
	}
	if !principal.HasRole(RoleApprover) {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "需要 approver 角色")
		return
	}

	namespace := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	var req ApprovalRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "请求体非法")
		return
	}
	if req.Decision != string(opsv1alpha1.ApprovalApprove) && req.Decision != string(opsv1alpha1.ApprovalReject) {
		writeError(w, http.StatusBadRequest, "INVALID_DECISION", "decision 必须是 Approve/Reject")
		return
	}
	if len(req.Reason) < 4 {
		writeError(w, http.StatusBadRequest, "INVALID_REASON", "reason 至少 4 个字符")
		return
	}

	// 创建前重新 GET Incident：必须 AwaitingApproval 且 proposal 非空。
	incident := &opsv1alpha1.AIOpsIncident{}
	if err := h.k8s.Get(r.Context(), client.ObjectKey{Namespace: namespace, Name: name}, incident); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "事故不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, "GET_FAILED", "查询失败")
		return
	}
	if incident.Status.Phase != opsv1alpha1.PhaseAwaitingApproval {
		writeError(w, http.StatusConflict, "PHASE_CONFLICT", fmt.Sprintf("事故当前处于 %s，不在待审批阶段", incident.Status.Phase))
		return
	}
	if incident.Status.Proposal == nil || incident.Status.Proposal.PlanDigest == "" {
		writeError(w, http.StatusConflict, "NO_PROPOSAL", "事故没有可审批的方案")
		return
	}

	approval, err := h.buildApproval(incident, principal, opsv1alpha1.ApprovalDecision(req.Decision), req.Reason, h.now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "BUILD_FAILED", err.Error())
		return
	}
	if err := h.k8s.Create(r.Context(), approval); err != nil {
		if apierrors.IsAlreadyExists(err) {
			// 同名即同一 digest 的重复提交：幂等返回已有审批。
			existing := &opsv1alpha1.RemediationApproval{}
			if getErr := h.k8s.Get(r.Context(), client.ObjectKey{Namespace: approval.Namespace, Name: approval.Name}, existing); getErr == nil {
				writeJSON(w, http.StatusOK, map[string]string{
					"approvalName": existing.Name,
					"decision":     string(existing.Spec.Decision),
					"idempotent":   "true",
				})
				return
			}
			writeError(w, http.StatusConflict, "APPROVAL_EXISTS", "该事故已有审批，请先撤销")
			return
		}
		writeError(w, http.StatusInternalServerError, "CREATE_FAILED", "创建审批失败")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{
		"approvalName": approval.Name,
		"decision":     string(approval.Spec.Decision),
	})
}

// buildApproval 构造审批 CR：planDigest/proposalRevision 只从 Status 复制。
func (h *Handlers) buildApproval(
	i *opsv1alpha1.AIOpsIncident,
	p Principal,
	decision opsv1alpha1.ApprovalDecision,
	reason string,
	now time.Time,
) (*opsv1alpha1.RemediationApproval, error) {
	if i.Status.Proposal == nil {
		return nil, fmt.Errorf("方案为空")
	}
	// 名称含 UID 短哈希（episode 重建不冲突）与 digest 短哈希
	// （digest 刷新后同一 Incident 可创建新审批，旧审批保留审计）。
	uidShort := string(i.UID)
	if len(uidShort) > 8 {
		uidShort = uidShort[:8]
	}
	digestShort := strings.TrimPrefix(i.Status.Proposal.PlanDigest, "sha256:")
	if len(digestShort) > 12 {
		digestShort = digestShort[:12]
	}
	name := fmt.Sprintf("%s-%s-%s-approval-%d", i.Name, uidShort, digestShort, i.Status.Proposal.Revision)
	return &opsv1alpha1.RemediationApproval{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: i.Namespace,
		},
		Spec: opsv1alpha1.RemediationApprovalSpec{
			IncidentRef: opsv1alpha1.IncidentReference{
				Name:             i.Name,
				UID:              i.UID,
				ProposalRevision: i.Status.Proposal.Revision,
			},
			Decision:   decision,
			PlanDigest: i.Status.Proposal.PlanDigest,
			Actor:      p.Subject,
			Reason:     reason,
			ExpiresAt:  metav1.NewTime(now.Add(approvalTTL)),
		},
	}, nil
}
