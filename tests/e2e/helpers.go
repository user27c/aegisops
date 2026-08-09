package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
	alertmanager "github.com/user27c/aegisops/internal/alertmanager"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GatewayResponse 是 alert-gateway webhook 的响应体。
type GatewayResponse struct {
	Accepted     int `json:"accepted"`
	Deduplicated int `json:"deduplicated"`
	Rejected     int `json:"rejected"`
}

// PostAlert 向 gateway 发送一条 Alertmanager webhook。
// labels 至少含 alertname/namespace/workload;fingerprint 用于生成可预测的 incident 名。
func PostAlert(ctx context.Context, e *Environment, labels map[string]string, fingerprint, status string) (GatewayResponse, error) {
	var resp GatewayResponse
	payload := map[string]any{
		"version":  "4",
		"groupKey": "{}",
		"status":   status,
		"alerts": []map[string]any{{
			"status":      status,
			"labels":      labels,
			"annotations": map[string]string{"summary": "e2e alert", "description": "e2e generated"},
			"startsAt":    time.Now().UTC().Format(time.RFC3339),
			"fingerprint": fingerprint,
		}},
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		e.GatewayURL+"/webhooks/alertmanager", bytes.NewReader(body))
	if err != nil {
		return resp, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.WebhookToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return resp, err
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusAccepted {
		return resp, fmt.Errorf("gateway 返回 %d: %s", res.StatusCode, strings.TrimSpace(string(raw)))
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return resp, fmt.Errorf("解析 gateway 响应: %w", err)
	}
	return resp, nil
}

// IncidentName 预测 gateway 生成的 incident 名，直接复用生产指纹算法，
// 避免测试 helper 与 gateway 的复合指纹规则漂移。
func IncidentName(e *Environment, alertName, upstreamFingerprint string) string {
	fingerprint := alertmanager.BuildFingerprint(alertmanager.NormalizedAlert{
		Cluster:             "local-k3s",
		AlertName:           alertName,
		UpstreamFingerprint: upstreamFingerprint,
		Target: opsv1alpha1.TargetReference{
			Namespace: e.Namespace,
			Name:      "faultlab",
		},
	})
	return alertmanager.IncidentName(alertName, fingerprint)
}

// GetIncident 读取 CR。
func GetIncident(ctx context.Context, e *Environment, ns, name string) (*opsv1alpha1.AIOpsIncident, error) {
	var inc opsv1alpha1.AIOpsIncident
	if err := e.K8s.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, &inc); err != nil {
		return nil, err
	}
	return &inc, nil
}

// WaitIncidentPhase 轮询 CR 直到 phase 等于目标或超时;只等待稳定/终态。
func WaitIncidentPhase(ctx context.Context, e *Environment, ns, name string, phase opsv1alpha1.IncidentPhase, timeout time.Duration) (*opsv1alpha1.AIOpsIncident, error) {
	deadline := time.Now().Add(timeout)
	var last *opsv1alpha1.AIOpsIncident
	for time.Now().Before(deadline) {
		inc, err := GetIncident(ctx, e, ns, name)
		if err == nil {
			last = inc
			if inc.Status.Phase == phase {
				return inc, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	if last != nil {
		lastError := ""
		if last.Status.Execution != nil {
			lastError = last.Status.Execution.LastError
		}
		return nil, fmt.Errorf("等待 phase=%s 超时(当前 %s, lastError=%s)", phase, last.Status.Phase, lastError)
	}
	return nil, fmt.Errorf("等待 incident %s/%s 超时(未创建)", ns, name)
}

// WaitIncidentCreated 轮询直到 incident 出现(任意 phase)。
func WaitIncidentCreated(ctx context.Context, e *Environment, ns, name string, timeout time.Duration) (*opsv1alpha1.AIOpsIncident, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		inc, err := GetIncident(ctx, e, ns, name)
		if err == nil {
			return inc, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return nil, fmt.Errorf("incident %s/%s 未在 %s 内创建", ns, name, timeout)
}

// ApproveIncident 使用 approver token 批准 incident。
func ApproveIncident(ctx context.Context, e *Environment, ns, name, reason string) error {
	if len(reason) < 4 {
		reason = "e2e approval"
	}
	return approveAsWithReason(ctx, e, ns, name, e.ApproverToken, reason)
}

// approveAs 使用指定 token 调审批端点(供 403 断言用)。
func approveAs(ctx context.Context, e *Environment, ns, name, token string) error {
	return approveAsWithReason(ctx, e, ns, name, token, "e2e approval")
}

func approveAsWithReason(ctx context.Context, e *Environment, ns, name, token, reason string) error {
	body, _ := json.Marshal(map[string]string{"decision": "Approve", "reason": reason})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		e.IncidentAPIURL+"/api/v1/incidents/"+ns+"/"+name+"/approval", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode == http.StatusCreated || res.StatusCode == http.StatusOK {
		return nil
	}
	return &httpStatusError{Code: res.StatusCode, Body: strings.TrimSpace(string(raw))}
}

type httpStatusError struct {
	Code int
	Body string
}

func (e *httpStatusError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.Code, e.Body) }

func isForbidden(err error) bool {
	se, ok := err.(*httpStatusError)
	return ok && se.Code == http.StatusForbidden
}

// InjectFault 向 fault-lab 注入故障。
func InjectFault(ctx context.Context, e *Environment, kind string, duration time.Duration) error {
	url := fmt.Sprintf("%s/inject?type=%s&duration=%s", e.FaultLabURL, kind, duration.String())
	return postNoBody(ctx, url)
}

// InjectOOMFault 注入 OOM。fault-lab 可能在 HTTP 响应写回前被 cgroup 杀死，
// 因此 EOF 是预期结果；后续以 Kubernetes 证据和 Incident 状态验证故障确实发生。
func InjectOOMFault(ctx context.Context, e *Environment, duration time.Duration) error {
	err := InjectFault(ctx, e, "oom", duration)
	if err != nil && strings.Contains(err.Error(), "EOF") {
		return nil
	}
	return err
}

// WaitForOOMKilled 等待 Kubernetes 将 OOM 结果写入 Pod lastState。
// OOM injector 可能在 HTTP 响应返回前杀死进程；立即发送告警会与状态回写竞态。
func WaitForOOMKilled(ctx context.Context, e *Environment, timeout time.Duration) error {
	return waitFor(ctx, timeout, func() (bool, string) {
		var pods corev1.PodList
		if err := e.K8s.List(ctx, &pods, client.InNamespace(e.Namespace), client.MatchingLabels{
			"app.kubernetes.io/instance": "faultlab",
		}); err != nil {
			return false, err.Error()
		}
		for _, pod := range pods.Items {
			for _, status := range pod.Status.ContainerStatuses {
				terminated := status.LastTerminationState.Terminated
				if terminated != nil && (terminated.Reason == "OOMKilled" || terminated.ExitCode == 137) {
					return true, ""
				}
			}
		}
		return false, "等待 faultlab Pod 记录 OOMKilled"
	})
}

// WaitForImagePullBackOff 等待 Kubernetes 将错误镜像记录为拉取失败。
// 立即发送告警会与 kubelet 写入 ContainerStatus 竞态，导致诊断证据不足。
func WaitForImagePullBackOff(ctx context.Context, e *Environment, timeout time.Duration) error {
	return waitFor(ctx, timeout, func() (bool, string) {
		var pods corev1.PodList
		if err := e.K8s.List(ctx, &pods, client.InNamespace(e.Namespace), client.MatchingLabels{
			"app.kubernetes.io/instance": "faultlab",
		}); err != nil {
			return false, err.Error()
		}
		for _, pod := range pods.Items {
			for _, status := range pod.Status.ContainerStatuses {
				if waiting := status.State.Waiting; waiting != nil &&
					(waiting.Reason == "ImagePullBackOff" || waiting.Reason == "ErrImagePull") {
					return true, ""
				}
			}
		}
		return false, "等待 faultlab Pod 记录 ImagePullBackOff"
	})
}

// RecoverFault 恢复 fault-lab 全部故障。
func RecoverFault(ctx context.Context, e *Environment) error {
	return postNoBody(ctx, e.FaultLabURL+"/cleanup")
}

// SetFaultLabMetricsPath 修改 fault-lab ServiceMonitor 的抓取路径。
// 保留 target、仅让抓取返回 404，确保 Prometheus 产生 up=0；缩容到 0 会直接移除 target。
func SetFaultLabMetricsPath(ctx context.Context, e *Environment, path string) error {
	sm := &unstructured.Unstructured{}
	sm.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "monitoring.coreos.com", Version: "v1", Kind: "ServiceMonitor",
	})
	key := types.NamespacedName{Namespace: e.Namespace, Name: "faultlab"}
	if err := e.K8s.Get(ctx, key, sm); err != nil {
		return fmt.Errorf("读取 faultlab ServiceMonitor: %w", err)
	}
	before := sm.DeepCopy()
	endpoints, found, err := unstructured.NestedSlice(sm.Object, "spec", "endpoints")
	if err != nil {
		return fmt.Errorf("读取 faultlab endpoints: %w", err)
	}
	if !found || len(endpoints) == 0 {
		return fmt.Errorf("faultlab ServiceMonitor 缺少 spec.endpoints")
	}
	endpoint, ok := endpoints[0].(map[string]interface{})
	if !ok {
		return fmt.Errorf("faultlab ServiceMonitor spec.endpoints[0] 类型错误")
	}
	endpoint["path"] = path
	endpoints[0] = endpoint
	if err := unstructured.SetNestedSlice(sm.Object, endpoints, "spec", "endpoints"); err != nil {
		return fmt.Errorf("设置 faultlab metrics path: %w", err)
	}
	return e.K8s.Patch(ctx, sm, client.MergeFrom(before))
}

// RestoreFaultLab 恢复 fault-lab 的抓取路径、副本和业务故障，并等待 HTTP 就绪。
// 测试清理必须等待完成，避免前一场景的缩容/重启级联污染下一场景。
func RestoreFaultLab(ctx context.Context, e *Environment) error {
	// core profile 不安装 Prometheus Operator/ServiceMonitor；清理仍必须继续
	// 恢复 workload 与业务故障，不能因可观测性资源缺失而提前返回。
	if e.Profile != "core" {
		if err := SetFaultLabMetricsPath(ctx, e, "/metrics"); err != nil {
			return err
		}
	}

	var d appsv1.Deployment
	key := types.NamespacedName{Namespace: e.Namespace, Name: "faultlab"}
	if err := e.K8s.Get(ctx, key, &d); err != nil {
		return err
	}
	registry, tag := e.Registry, e.Tag
	if registry == "" {
		registry = "aegisops.local"
	}
	if tag == "" {
		tag = "dev"
	}
	wantImage := fmt.Sprintf("%s/fault-lab:%s", registry, tag)
	if len(d.Spec.Template.Spec.Containers) == 0 {
		return fmt.Errorf("faultlab Deployment 没有容器")
	}
	before := d.DeepCopy()
	container := &d.Spec.Template.Spec.Containers[0]
	wantResources := corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("200m"),
			corev1.ResourceMemory: resource.MustParse("256Mi"),
		},
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("10m"),
			corev1.ResourceMemory: resource.MustParse("32Mi"),
		},
	}
	needsPatch := container.Image != wantImage || d.Spec.Replicas == nil || *d.Spec.Replicas != 1
	if container.Resources.Limits.Cpu().Cmp(*wantResources.Limits.Cpu()) != 0 ||
		container.Resources.Limits.Memory().Cmp(*wantResources.Limits.Memory()) != 0 ||
		container.Resources.Requests.Cpu().Cmp(*wantResources.Requests.Cpu()) != 0 ||
		container.Resources.Requests.Memory().Cmp(*wantResources.Requests.Memory()) != 0 {
		needsPatch = true
	}
	for _, key := range []string{"ops.aegis.io/operation-id", "ops.aegis.io/last-action-at"} {
		if _, ok := d.Annotations[key]; ok {
			needsPatch = true
			delete(d.Annotations, key)
		}
	}
	for _, key := range []string{"ops.aegis.io/restarted-at", "ops.aegis.io/e2e-restart"} {
		if _, ok := d.Spec.Template.Annotations[key]; ok {
			needsPatch = true
			delete(d.Spec.Template.Annotations, key)
		}
	}
	if needsPatch {
		patch := client.MergeFrom(before)
		one := int32(1)
		d.Spec.Replicas = &one
		container.Image = wantImage
		container.Resources = wantResources
		if err := e.K8s.Patch(ctx, &d, patch); err != nil {
			return err
		}
	}
	if err := waitFor(ctx, 3*time.Minute, func() (bool, string) {
		var current appsv1.Deployment
		if err := e.K8s.Get(ctx, key, &current); err != nil {
			return false, err.Error()
		}
		if current.Status.ObservedGeneration < current.Generation ||
			current.Status.UpdatedReplicas < 1 ||
			current.Status.AvailableReplicas < 1 || current.Status.ReadyReplicas < 1 {
			return false, fmt.Sprintf("faultlab 未完成 rollout observed=%d generation=%d updated=%d available=%d ready=%d", current.Status.ObservedGeneration, current.Generation, current.Status.UpdatedReplicas, current.Status.AvailableReplicas, current.Status.ReadyReplicas)
		}
		return true, ""
	}); err != nil {
		return err
	}
	if err := RecoverFault(ctx, e); err != nil {
		return err
	}
	return waitFor(ctx, 2*time.Minute, func() (bool, string) {
		if CheckoutHealthy(ctx, e) {
			return true, ""
		}
		return false, "faultlab /checkout 尚未恢复"
	})
}

// CheckoutHealthy 探测 fault-lab /checkout 是否 200。
func CheckoutHealthy(ctx context.Context, e *Environment) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.FaultLabURL+"/checkout", nil)
	if err != nil {
		return false
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = res.Body.Close() }()
	return res.StatusCode == http.StatusOK
}

// WaitFaultLabHealthy waits for the service and its local E2E port-forward to
// recover after a preceding rollout. Tests must not mistake this expected
// transition for an authorization or remediation failure.
func WaitFaultLabHealthy(ctx context.Context, e *Environment, timeout time.Duration) error {
	return waitFor(ctx, timeout, func() (bool, string) {
		if CheckoutHealthy(ctx, e) {
			return true, ""
		}
		return false, "faultlab /checkout 或 E2E port-forward 尚未恢复"
	})
}

func postNoBody(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusAccepted {
		raw, _ := io.ReadAll(res.Body)
		return fmt.Errorf("POST %s 返回 %d: %s", url, res.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

// DeploymentState 是 E2E 关心的 Deployment 状态摘要。
type DeploymentState struct {
	Replicas            int32
	AvailableReplicas   int32
	UnavailableReplicas int32
	Annotations         map[string]string // Deployment metadata
	TemplateAnnotations map[string]string
	ResourceVersion     string
}

// DeploymentSnapshot 读取 Deployment 当前状态。
func DeploymentSnapshot(ctx context.Context, e *Environment, ns, name string) (DeploymentState, error) {
	var d appsv1.Deployment
	if err := e.K8s.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, &d); err != nil {
		return DeploymentState{}, err
	}
	return DeploymentState{
		Replicas:            d.Status.Replicas,
		AvailableReplicas:   d.Status.AvailableReplicas,
		UnavailableReplicas: d.Status.UnavailableReplicas,
		Annotations:         d.Annotations,
		TemplateAnnotations: d.Spec.Template.Annotations,
		ResourceVersion:     d.ResourceVersion,
	}, nil
}

// TimelineEntry 对应 incident-api timeline 单条。
type TimelineEntry struct {
	Time      string `json:"time"`
	Type      string `json:"type"`
	Reason    string `json:"reason,omitempty"`
	Message   string `json:"message,omitempty"`
	Actor     string `json:"actor,omitempty"`
	Sequence  int    `json:"sequence,omitempty"`
	EventHash string `json:"eventHash,omitempty"`
}

// TimelineResponse 对应 GET /api/v1/incidents/{ns}/{name}/timeline。
type TimelineResponse struct {
	Items              []TimelineEntry `json:"items"`
	DetailsUnavailable bool            `json:"detailsUnavailable"`
	Source             string          `json:"source"`
}

// QueryAuditTimeline 查询 incident 时间线。
func QueryAuditTimeline(ctx context.Context, e *Environment, ns, name string) (TimelineResponse, error) {
	var tr TimelineResponse
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		e.IncidentAPIURL+"/api/v1/incidents/"+ns+"/"+name+"/timeline", nil)
	if err != nil {
		return tr, err
	}
	req.Header.Set("Authorization", "Bearer "+e.ViewerToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return tr, err
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return tr, fmt.Errorf("timeline 返回 %d: %s", res.StatusCode, strings.TrimSpace(string(raw)))
	}
	if err := json.Unmarshal(raw, &tr); err != nil {
		return tr, err
	}
	return tr, nil
}

// MailHogMessage 是 MailHog API 的消息摘要。
type MailHogMessage struct {
	ID  string `json:"ID"`
	Raw struct {
		Data string `json:"Data"`
	} `json:"Raw"`
}

// AssertEmailReceived 轮询 MailHog 直到出现 subject 含 prefix 的邮件。
func AssertEmailReceived(ctx context.Context, e *Environment, subjectPrefix string, timeout time.Duration) error {
	return assertMailHogMessage(ctx, e, timeout, func(raw string) bool {
		return strings.Contains(strings.ToUpper(raw), strings.ToUpper(subjectPrefix))
	})
}

// AssertAlertEmailReceived 轮询 MailHog 直到收到当前 run 的指定状态告警邮件。
// 同时匹配 namespace 和 alertname，避免共享 Alertmanager 中其他 run 的邮件造成假阳性。
func AssertAlertEmailReceived(ctx context.Context, e *Environment, status string, timeout time.Duration) error {
	return assertMailHogMessage(ctx, e, timeout, func(raw string) bool {
		upper := strings.ToUpper(raw)
		return strings.Contains(upper, strings.ToUpper(status)) &&
			strings.Contains(raw, e.Namespace) &&
			strings.Contains(raw, "AegisOpsTargetDown")
	})
}

func assertMailHogMessage(ctx context.Context, e *Environment, timeout time.Duration, match func(string) bool) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, e.MailHogURL+"/api/v2/messages", nil)
		res, err := http.DefaultClient.Do(req)
		if err == nil {
			var page struct {
				Items []MailHogMessage `json:"items"`
			}
			raw, _ := io.ReadAll(res.Body)
			_ = res.Body.Close()
			if json.Unmarshal(raw, &page) == nil {
				for _, m := range page.Items {
					if match(m.Raw.Data) {
						return nil
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	return fmt.Errorf("MailHog 未收到匹配的邮件")
}

// ClearMailHog 清空历史邮件，避免重复运行 E2E 时旧消息造成假阳性。
func ClearMailHog(ctx context.Context, e *Environment) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, e.MailHogURL+"/api/v1/messages", nil)
	if err != nil {
		return err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(res.Body)
		return fmt.Errorf("清空 MailHog 返回 %d: %s", res.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

// DumpDiagnostics 将 incident 与 timeline 落盘到 dir(供失败时收集)。
func DumpDiagnostics(ctx context.Context, e *Environment, dir string, ns, name string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	inc, err := GetIncident(ctx, e, ns, name)
	if err == nil {
		raw, _ := json.MarshalIndent(inc, "", "  ")
		_ = os.WriteFile(filepath.Join(dir, "incident.json"), raw, 0o600)
	}
	tr, err := QueryAuditTimeline(ctx, e, ns, name)
	if err == nil {
		raw, _ := json.MarshalIndent(tr, "", "  ")
		_ = os.WriteFile(filepath.Join(dir, "timeline.json"), raw, 0o600)
	}
	return nil
}

// waitFor 通用轮询:fn 返回 (成功, 描述)。
func waitFor(ctx context.Context, timeout time.Duration, fn func() (bool, string)) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ok, desc := fn()
		if ok {
			return nil
		}
		_ = desc
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("等待条件超时(%s)", timeout)
}

var _ = metav1.Now

// httpGet 发起 GET 请求返回响应体(供邮件正文 dump)。
func httpGet(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	return string(raw), nil
}
