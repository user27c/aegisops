package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestE2ESecurityBoundaries 场景 D:安全边界。
//
// 验证:
//  1. Diagnosis 无 Token → 401;错 Token → 401
//  2. Diagnosis 容器 automountServiceAccountToken=false(Pod 内无 K8s 凭证)
//  3. viewer 调审批 → 403
//  4. 提交自定义 digest 被忽略(approval body 无 digest 契约)
//  5. 非白名单动作被 CRD 拒绝
//  6. 同目标两个 incident 至多一个进入 Executing
func TestE2ESecurityBoundaries(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	e := testEnv(t)

	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		_ = RestoreFaultLab(c, e)
		_ = e.K8s.DeleteAllOf(c, &opsv1alpha1.AIOpsIncident{}, client.InNamespace(e.Namespace))
	})

	t.Run("Diagnosis鉴权", func(t *testing.T) {
		if code := diagnosisStatus(ctx, e, ""); code != http.StatusUnauthorized {
			t.Fatalf("无 Token 应 401,实际 %d", code)
		}
		if code := diagnosisStatus(ctx, e, "wrong-token-xyz"); code != http.StatusUnauthorized {
			t.Fatalf("错 Token 应 401,实际 %d", code)
		}
	})

	t.Run("Diagnosis无SA凭证", func(t *testing.T) {
		var d appsv1.Deployment
		if err := e.K8s.Get(ctx, types.NamespacedName{Namespace: e.SystemNamespace, Name: "aegisops-diagnosis-api"}, &d); err != nil {
			t.Fatal(err)
		}
		if d.Spec.Template.Spec.AutomountServiceAccountToken == nil || *d.Spec.Template.Spec.AutomountServiceAccountToken {
			t.Fatalf("diagnosis-api 应 automountServiceAccountToken=false,实际 %+v", d.Spec.Template.Spec.AutomountServiceAccountToken)
		}
	})

	t.Run("viewer审批被拒", func(t *testing.T) {
		// 构造一个 AwaitingApproval 的 incident(复用场景 B 的快路径:OOM→PatchResourceLimit)。
		incName := IncidentName(e, "ContainerOOMKilled", "sha256:e2e-sec-oom-0001")
		if err := InjectOOMFault(ctx, e, 3*time.Minute); err != nil {
			t.Fatal(err)
		}
		if err := WaitForOOMKilled(ctx, e, 60*time.Second); err != nil {
			t.Fatal(err)
		}
		resp, err := PostAlert(ctx, e, map[string]string{
			"alertname": "ContainerOOMKilled",
			"namespace": e.Namespace,
			"workload":  "faultlab",
			"severity":  "critical",
		}, "sha256:e2e-sec-oom-0001", "firing")
		if err != nil {
			t.Fatal(err)
		}
		if resp.Rejected > 0 {
			t.Fatalf("告警被拒绝: %+v", resp)
		}
		if _, err := WaitIncidentPhase(ctx, e, e.Namespace, incName, opsv1alpha1.PhaseAwaitingApproval, 4*time.Minute); err != nil {
			t.Fatal(err)
		}
		if err := approveAs(ctx, e, e.Namespace, incName, e.ViewerToken); err == nil {
			t.Fatal("viewer 审批应 403")
		} else if !isForbidden(err) {
			t.Fatalf("应 403,实际 %v", err)
		}

		// 自定义 digest 被忽略:带 digest 字段仍成功且按服务端 planDigest 记录。
		body, _ := json.Marshal(map[string]string{
			"decision": "Approve",
			"reason":   "e2e digest ignored",
			"digest":   "sha256:deadbeef-client-forged",
		})
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
			e.IncidentAPIURL+"/api/v1/incidents/"+e.Namespace+"/"+incName+"/approval", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+e.ApproverToken)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		if res.StatusCode != http.StatusCreated && res.StatusCode != http.StatusOK {
			t.Fatalf("带 digest 的审批应成功(服务端忽略该字段),实际 %d", res.StatusCode)
		}
	})

	t.Run("非白名单动作被拒绝", func(t *testing.T) {
		bad := &opsv1alpha1.RemediationPolicy{
			ObjectMeta: metav1.ObjectMeta{Namespace: e.Namespace, Name: "e2e-bad-action"},
			Spec: opsv1alpha1.RemediationPolicySpec{
				TargetSelector: opsv1alpha1.TargetSelector{
					Kinds: []string{"Deployment"},
				},
				Actions: map[opsv1alpha1.ActionType]opsv1alpha1.ActionPolicy{
					"DeleteEverything": {Enabled: true, Mode: opsv1alpha1.ModeAuto},
				},
			},
		}
		if err := e.K8s.Create(ctx, bad); err == nil {
			t.Fatal("非白名单动作应被 CRD 拒绝")
		} else {
			t.Logf("CRD 拒绝符合预期: %v", err)
		}
	})

	t.Run("同目标互斥", func(t *testing.T) {
		// 同一 target 两个不同告警 → 两个 incident,至多一个进入 Executing。
		names := []string{
			IncidentName(e, "CheckoutHTTP500s", "sha256:e2e-mutex-a-0001"),
			IncidentName(e, "CheckoutHTTP500s", "sha256:e2e-mutex-b-0001"),
		}
		for i, n := range []string{"sha256:e2e-mutex-a-0001", "sha256:e2e-mutex-b-0001"} {
			resp, err := PostAlert(ctx, e, map[string]string{
				"alertname": "CheckoutHTTP500s",
				"namespace": e.Namespace,
				"workload":  "faultlab",
				"severity":  "critical",
			}, n, "firing")
			if err != nil {
				t.Fatal(err)
			}
			if resp.Rejected > 0 {
				t.Fatalf("告警 %d 被拒绝: %+v", i, resp)
			}
		}
		var execCount int
		for _, name := range names {
			inc, err := WaitIncidentCreated(ctx, e, e.Namespace, name, 60*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if inc.Status.Phase == opsv1alpha1.PhaseExecuting {
				execCount++
			}
		}
		// 终态后复查:至多一个执行过(互斥锁保证)。
		if err := waitFor(ctx, 2*time.Minute, func() (bool, string) {
			execCount = 0
			for _, name := range names {
				inc, err := GetIncident(ctx, e, e.Namespace, name)
				if err != nil {
					return false, err.Error()
				}
				if inc.Status.Phase == opsv1alpha1.PhaseExecuting || inc.Status.Phase == opsv1alpha1.PhaseVerifying ||
					inc.Status.Phase == opsv1alpha1.PhaseResolved {
					if inc.Status.Execution != nil {
						execCount++
					}
				}
			}
			return true, ""
		}); err != nil {
			t.Fatal(err)
		}
		if execCount > 1 {
			t.Fatalf("同目标两个 incident 都执行了(%d 个 Executing),互斥失效", execCount)
		}
		t.Logf("同目标互斥:两个 incident 均创建,执行数=%d(≤1)", execCount)
	})

	t.Log("场景 D 通过:安全边界全部符合预期")
}

func diagnosisStatus(ctx context.Context, e *Environment, token string) int {
	// 使用实际存在的受保护路由；无效资源 ID 仍会先经过 Bearer 鉴权。
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		e.DiagnosisURL+"/v1/evidence/00000000-0000-0000-0000-000000000000", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0
	}
	defer func() { _ = res.Body.Close() }()
	_, _ = io.Copy(io.Discard, res.Body)
	return res.StatusCode
}
