package executor

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

func newExecClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("scheme: %v", err)
	}
	if err := opsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("ops scheme: %v", err)
	}
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&appsv1.Deployment{}).
		WithObjects(objs...).
		Build()
}

func execIncident(action opsv1alpha1.ActionType, params map[string]any) *opsv1alpha1.AIOpsIncident {
	raw, _ := json.Marshal(params)
	i := &opsv1alpha1.AIOpsIncident{
		ObjectMeta: metav1.ObjectMeta{Name: "inc-1", Namespace: "fault-lab", UID: types.UID("uid-1")},
		Spec: opsv1alpha1.AIOpsIncidentSpec{
			TargetRef: opsv1alpha1.TargetReference{
				APIVersion: "apps/v1", Kind: "Deployment",
				Namespace: "fault-lab", Name: "checkout-api", UID: "dep-1",
			},
		},
		Status: opsv1alpha1.AIOpsIncidentStatus{
			Proposal: &opsv1alpha1.ActionProposal{
				Revision:   1,
				Action:     action,
				Parameters: apiextensionsv1.JSON{Raw: raw},
				PlanDigest: "sha256:" + repeat('a', 64),
			},
		},
	}
	return i
}

func repeat(c byte, n int) string {
	out := make([]byte, n)
	for i := range out {
		out[i] = c
	}
	return string(out)
}

func execCtx(c client.Client, i *opsv1alpha1.AIOpsIncident) *Context {
	return &Context{
		Client:   c,
		Incident: i,
		Proposal: *i.Status.Proposal,
		Clock:    func() time.Time { return time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC) },
		Logger:   logr.Discard(),
	}
}

func healthyDeployment() *appsv1.Deployment {
	replicas := int32(2)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "checkout-api", Namespace: "fault-lab", UID: "dep-1",
			Generation:  1,
			Annotations: map[string]string{},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "checkout"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "checkout"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "app",
						Image: "registry/checkout:v2",
						Resources: corev1.ResourceRequirements{
							Limits: corev1.ResourceList{
								corev1.ResourceMemory: resource.MustParse("256Mi"),
							},
						},
					}},
				},
			},
		},
		Status: appsv1.DeploymentStatus{
			ObservedGeneration:  1,
			AvailableReplicas:   2,
			UnavailableReplicas: 0,
		},
	}
}

func TestRestartWorkload_FullCycle(t *testing.T) {
	dep := healthyDeployment()
	c := newExecClient(t, dep)
	i := execIncident(opsv1alpha1.ActionRestartWorkload, map[string]any{"reason": "CrashLoopBackOff"})
	ctx := context.Background()
	action := &RestartWorkloadAction{}
	ec := execCtx(c, i)

	if err := action.Preflight(ctx, ec); err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	snap, err := action.Snapshot(ctx, ec)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	result, err := action.Apply(ctx, ec, snap)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.OperationID != OperationID(i) {
		t.Errorf("OperationID 错误: %s", result.OperationID)
	}

	// 幂等:再次 Apply 跳过。
	var got appsv1.Deployment
	_ = c.Get(ctx, client.ObjectKey{Namespace: "fault-lab", Name: "checkout-api"}, &got)
	if got.Spec.Template.Annotations[RestartAnnotationKey] == "" {
		t.Error("restart 注解未写入")
	}
	if got.Annotations[OperationIDAnnotation] != result.OperationID {
		t.Error("OperationID 注解未写入")
	}
	result2, _ := action.Apply(ctx, ec, snap)
	if result2.Message == "" || result2.OperationID != result.OperationID {
		t.Error("幂等 Apply 应返回原 OperationID")
	}

	// Verify。
	verification, err := action.Verify(ctx, ec, snap)
	if err != nil || !verification.Healthy {
		t.Errorf("Verify 应健康: %v %v", verification, err)
	}

	// Rollback 不支持但必须给出明确说明。
	rb, err := action.Rollback(ctx, ec, snap)
	if err != nil || rb.RolledBack {
		t.Errorf("Restart 不应支持回滚: %v %+v", err, rb)
	}
}

func TestRestartWorkload_PreflightRolloutInProgress(t *testing.T) {
	dep := healthyDeployment()
	dep.Status.ObservedGeneration = 0 // rollout 进行中
	c := newExecClient(t, dep)
	action := &RestartWorkloadAction{}
	if err := action.Preflight(context.Background(), execCtx(c, execIncident(opsv1alpha1.ActionRestartWorkload, map[string]any{}))); err == nil {
		t.Error("rollout 进行中应拒绝重启")
	}
}

func TestScaleDeployment_FullCycle(t *testing.T) {
	dep := healthyDeployment()
	c := newExecClient(t, dep)
	i := execIncident(opsv1alpha1.ActionScaleDeployment, map[string]any{"replicas": float64(4), "reason": "扩容"})
	ctx := context.Background()
	action := &ScaleDeploymentAction{}
	ec := execCtx(c, i)

	if err := action.Preflight(ctx, ec); err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	snap, err := action.Snapshot(ctx, ec)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if _, err := action.Apply(ctx, ec, snap); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	var got appsv1.Deployment
	_ = c.Get(ctx, client.ObjectKey{Namespace: "fault-lab", Name: "checkout-api"}, &got)
	if *got.Spec.Replicas != 4 {
		t.Errorf("副本数应为 4: %d", *got.Spec.Replicas)
	}

	// 回滚恢复原副本数。
	rb, err := action.Rollback(ctx, ec, snap)
	if err != nil || !rb.RolledBack {
		t.Fatalf("Rollback: %v %+v", err, rb)
	}
	_ = c.Get(ctx, client.ObjectKey{Namespace: "fault-lab", Name: "checkout-api"}, &got)
	if *got.Spec.Replicas != 2 {
		t.Errorf("回滚后副本数应为 2: %d", *got.Spec.Replicas)
	}
}

func TestPatchResource_FullCycle(t *testing.T) {
	dep := healthyDeployment()
	c := newExecClient(t, dep)
	i := execIncident(opsv1alpha1.ActionPatchResourceLimit, map[string]any{"container": "app", "memoryLimit": "512Mi"})
	ctx := context.Background()
	action := &PatchResourceLimitAction{}
	ec := execCtx(c, i)

	if err := action.Preflight(ctx, ec); err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	snap, err := action.Snapshot(ctx, ec)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if _, err := action.Apply(ctx, ec, snap); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	var got appsv1.Deployment
	_ = c.Get(ctx, client.ObjectKey{Namespace: "fault-lab", Name: "checkout-api"}, &got)
	limit := got.Spec.Template.Spec.Containers[0].Resources.Limits[corev1.ResourceMemory]
	if limit.String() != "512Mi" {
		t.Errorf("内存 limit 应为 512Mi: %s", limit.String())
	}

	// 回滚恢复 256Mi。
	rb, err := action.Rollback(ctx, ec, snap)
	if err != nil || !rb.RolledBack {
		t.Fatalf("Rollback: %v %+v", err, rb)
	}
	_ = c.Get(ctx, client.ObjectKey{Namespace: "fault-lab", Name: "checkout-api"}, &got)
	limit = got.Spec.Template.Spec.Containers[0].Resources.Limits[corev1.ResourceMemory]
	if limit.String() != "256Mi" {
		t.Errorf("回滚后内存 limit 应为 256Mi: %s", limit.String())
	}
}

func TestPatchResource_PreflightMissingContainer(t *testing.T) {
	dep := healthyDeployment()
	c := newExecClient(t, dep)
	i := execIncident(opsv1alpha1.ActionPatchResourceLimit, map[string]any{"container": "missing", "memoryLimit": "512Mi"})
	if err := (&PatchResourceLimitAction{}).Preflight(context.Background(), execCtx(c, i)); err == nil {
		t.Error("容器不存在应报错")
	}
}

func TestRollbackDeployment_FullCycle(t *testing.T) {
	dep := healthyDeployment()
	// 两个 ReplicaSet:rev 1 和 rev 2。
	rs1 := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "checkout-api-rs1", Namespace: "fault-lab",
			Annotations:     map[string]string{"deployment.kubernetes.io/revision": "1"},
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(dep, appsv1.SchemeGroupVersion.WithKind("Deployment"))},
		},
		Spec: appsv1.ReplicaSetSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "registry/checkout:v1"}}},
			},
		},
	}
	rs2 := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "checkout-api-rs2", Namespace: "fault-lab",
			Annotations:     map[string]string{"deployment.kubernetes.io/revision": "2"},
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(dep, appsv1.SchemeGroupVersion.WithKind("Deployment"))},
		},
		Spec: appsv1.ReplicaSetSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "registry/checkout:v2"}}},
			},
		},
	}
	c := newExecClient(t, dep, rs1, rs2)
	i := execIncident(opsv1alpha1.ActionRollbackDeployment, map[string]any{"targetRevision": float64(1), "reason": "回滚"})
	ctx := context.Background()
	action := &RollbackDeploymentAction{}
	ec := execCtx(c, i)

	if err := action.Preflight(ctx, ec); err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	snap, err := action.Snapshot(ctx, ec)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if _, err := action.Apply(ctx, ec, snap); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	var got appsv1.Deployment
	_ = c.Get(ctx, client.ObjectKey{Namespace: "fault-lab", Name: "checkout-api"}, &got)
	if got.Spec.Template.Spec.Containers[0].Image != "registry/checkout:v1" {
		t.Errorf("应回滚到 v1 镜像: %s", got.Spec.Template.Spec.Containers[0].Image)
	}

	// 恢复原 template(v2)。
	rb, err := action.Rollback(ctx, ec, snap)
	if err != nil || !rb.RolledBack {
		t.Fatalf("Rollback: %v %+v", err, rb)
	}
	_ = c.Get(ctx, client.ObjectKey{Namespace: "fault-lab", Name: "checkout-api"}, &got)
	if got.Spec.Template.Spec.Containers[0].Image != "registry/checkout:v2" {
		t.Errorf("回滚后应恢复 v2 镜像: %s", got.Spec.Template.Spec.Containers[0].Image)
	}
}

func TestRollbackDeployment_PreflightMissingRevision(t *testing.T) {
	dep := healthyDeployment()
	c := newExecClient(t, dep)
	i := execIncident(opsv1alpha1.ActionRollbackDeployment, map[string]any{"targetRevision": float64(9)})
	if err := (&RollbackDeploymentAction{}).Preflight(context.Background(), execCtx(c, i)); err == nil {
		t.Error("不存在的 revision 应报错")
	}
}

func TestRestoreConfigMap_FullCycle(t *testing.T) {
	immutable := true
	backup := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "checkout-config-backup", Namespace: "fault-lab"},
		Immutable:  &immutable,
		Data:       map[string]string{"app.yaml": "good-config"},
	}
	target := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "checkout-config", Namespace: "fault-lab"},
		Data:       map[string]string{"app.yaml": "bad-config"},
	}
	c := newExecClient(t, backup, target)
	i := execIncident(opsv1alpha1.ActionRestoreConfigMap, map[string]any{
		"targetConfigMap": "checkout-config", "backupConfigMap": "checkout-config-backup",
	})
	ctx := context.Background()
	action := &RestoreConfigMapAction{}
	ec := execCtx(c, i)

	if err := action.Preflight(ctx, ec); err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	snap, err := action.Snapshot(ctx, ec)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if _, err := action.Apply(ctx, ec, snap); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	var got corev1.ConfigMap
	_ = c.Get(ctx, client.ObjectKey{Namespace: "fault-lab", Name: "checkout-config"}, &got)
	if got.Data["app.yaml"] != "good-config" {
		t.Errorf("应恢复为 good-config: %s", got.Data["app.yaml"])
	}

	// Verify:数据一致。
	v, err := action.Verify(ctx, ec, snap)
	if err != nil || !v.Healthy {
		t.Errorf("Verify 应健康: %v %v", v, err)
	}

	// 回滚恢复 bad-config。
	rb, err := action.Rollback(ctx, ec, snap)
	if err != nil || !rb.RolledBack {
		t.Fatalf("Rollback: %v %+v", err, rb)
	}
	_ = c.Get(ctx, client.ObjectKey{Namespace: "fault-lab", Name: "checkout-config"}, &got)
	if got.Data["app.yaml"] != "bad-config" {
		t.Errorf("回滚后应恢复 bad-config: %s", got.Data["app.yaml"])
	}
}

func TestRestoreConfigMap_PreflightBackupNotImmutable(t *testing.T) {
	backup := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "checkout-config-backup", Namespace: "fault-lab"},
		Data:       map[string]string{"a": "b"},
	}
	c := newExecClient(t, backup)
	i := execIncident(opsv1alpha1.ActionRestoreConfigMap, map[string]any{
		"targetConfigMap": "checkout-config", "backupConfigMap": "checkout-config-backup",
	})
	if err := (&RestoreConfigMapAction{}).Preflight(context.Background(), execCtx(c, i)); err == nil {
		t.Error("备份非 immutable 应报错")
	}
}

func TestRegistry_DuplicateAndMissing(t *testing.T) {
	r, err := DefaultRegistry()
	if err != nil {
		t.Fatalf("DefaultRegistry: %v", err)
	}
	if len(r.Names()) != 5 {
		t.Errorf("应注册 5 个动作: %v", r.Names())
	}
	if err := r.Register(&RestartWorkloadAction{}); err == nil {
		t.Error("重复注册应报错")
	}
	if _, err := r.Get("Unknown"); err == nil {
		t.Error("未注册动作应报错")
	}
}

func TestRestartWorkload_PreflightUnavailable(t *testing.T) {
	dep := healthyDeployment()
	dep.Status.UnavailableReplicas = 1
	c := newExecClient(t, dep)
	if err := (&RestartWorkloadAction{}).Preflight(context.Background(), execCtx(c, execIncident(opsv1alpha1.ActionRestartWorkload, map[string]any{}))); err == nil {
		t.Error("存在不可用副本应拒绝重启")
	}
}

func TestRestartWorkload_VerifyUnhealthy(t *testing.T) {
	dep := healthyDeployment()
	dep.Status.AvailableReplicas = 1
	c := newExecClient(t, dep)
	i := execIncident(opsv1alpha1.ActionRestartWorkload, map[string]any{})
	snap, _ := (&RestartWorkloadAction{}).Snapshot(context.Background(), execCtx(c, i))
	v, err := (&RestartWorkloadAction{}).Verify(context.Background(), execCtx(c, i), snap)
	if err != nil || v.Healthy {
		t.Errorf("副本不足应不健康: %+v %v", v, err)
	}
}

func TestScaleDeployment_HPARejects(t *testing.T) {
	dep := healthyDeployment()
	c := newExecClient(t, dep)
	// 用 unstructured 注入 HPA。
	hpa := map[string]any{
		"apiVersion": "autoscaling/v2",
		"kind":       "HorizontalPodAutoscaler",
		"metadata":   map[string]any{"name": "checkout-hpa", "namespace": "fault-lab"},
		"spec": map[string]any{
			"scaleTargetRef": map[string]any{"kind": "Deployment", "name": "checkout-api"},
		},
	}
	raw, _ := json.Marshal(hpa)
	var u unstructured.Unstructured
	_ = json.Unmarshal(raw, &u.Object)
	if err := c.Create(context.Background(), &u); err != nil {
		t.Fatalf("创建 HPA: %v", err)
	}
	i := execIncident(opsv1alpha1.ActionScaleDeployment, map[string]any{"replicas": float64(4)})
	if err := (&ScaleDeploymentAction{}).Preflight(context.Background(), execCtx(c, i)); err == nil {
		t.Error("HPA 管理下应拒绝直接扩容")
	}
}

func TestScaleDeployment_BadParams(t *testing.T) {
	dep := healthyDeployment()
	c := newExecClient(t, dep)
	// replicas 缺失 → 类型断言 panic 防护。
	i := execIncident(opsv1alpha1.ActionScaleDeployment, map[string]any{})
	ctx := context.Background()
	action := &ScaleDeploymentAction{}
	ec := execCtx(c, i)
	snap, _ := action.Snapshot(ctx, ec)
	if _, err := action.Apply(ctx, ec, snap); err == nil {
		t.Error("缺 replicas 参数应报错")
	}
}

func TestRestoreConfigMap_PreflightBackupMissing(t *testing.T) {
	c := newExecClient(t)
	i := execIncident(opsv1alpha1.ActionRestoreConfigMap, map[string]any{
		"targetConfigMap": "checkout-config", "backupConfigMap": "missing-backup",
	})
	if err := (&RestoreConfigMapAction{}).Preflight(context.Background(), execCtx(c, i)); err == nil {
		t.Error("备份不存在应报错")
	}
}

func TestRestoreConfigMap_VerifyMismatch(t *testing.T) {
	immutable := true
	backup := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "checkout-config-backup", Namespace: "fault-lab"},
		Immutable:  &immutable,
		Data:       map[string]string{"a": "good"},
	}
	target := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "checkout-config", Namespace: "fault-lab"},
		Data:       map[string]string{"a": "bad"},
	}
	c := newExecClient(t, backup, target)
	i := execIncident(opsv1alpha1.ActionRestoreConfigMap, map[string]any{
		"targetConfigMap": "checkout-config", "backupConfigMap": "checkout-config-backup",
	})
	ctx := context.Background()
	action := &RestoreConfigMapAction{}
	ec := execCtx(c, i)
	snap, _ := action.Snapshot(ctx, ec)
	v, err := action.Verify(ctx, ec, snap)
	if err != nil || v.Healthy {
		t.Errorf("数据不一致应不健康: %+v %v", v, err)
	}
}

func TestRollbackDeployment_RollbackMissingTemplate(t *testing.T) {
	dep := healthyDeployment()
	c := newExecClient(t, dep)
	i := execIncident(opsv1alpha1.ActionRollbackDeployment, map[string]any{"targetRevision": float64(1)})
	action := &RollbackDeploymentAction{}
	rb, err := action.Rollback(context.Background(), execCtx(c, i), Snapshot{Action: action.Type(), Payload: map[string]any{}})
	if err == nil || rb.RolledBack {
		t.Errorf("缺 template 快照应报错: %+v %v", rb, err)
	}
}

func TestOperationID_Stable(t *testing.T) {
	i1 := execIncident(opsv1alpha1.ActionRestartWorkload, map[string]any{})
	i2 := execIncident(opsv1alpha1.ActionRestartWorkload, map[string]any{})
	if OperationID(i1) != OperationID(i2) {
		t.Error("相同 incident+digest 的 OperationID 应一致")
	}
	i2.Status.Proposal.PlanDigest = "sha256:" + repeat('b', 64)
	if OperationID(i1) == OperationID(i2) {
		t.Error("digest 变化后 OperationID 应不同")
	}
}

func TestVerifyHealthyPaths(t *testing.T) {
	ctx := context.Background()
	// PatchResource Verify:健康 Deployment。
	dep := healthyDeployment()
	dep.Status.ObservedGeneration = 1
	c := newExecClient(t, dep)
	i := execIncident(opsv1alpha1.ActionPatchResourceLimit, map[string]any{"container": "app", "memoryLimit": "512Mi"})
	ec := execCtx(c, i)
	action := &PatchResourceLimitAction{}
	snap, _ := action.Snapshot(ctx, ec)
	v, err := action.Verify(ctx, ec, snap)
	if err != nil || !v.Healthy {
		t.Errorf("PatchResource Verify 应健康: %+v %v", v, err)
	}
	// rollout 进行中 → 不健康。
	var latest appsv1.Deployment
	_ = c.Get(ctx, client.ObjectKey{Namespace: "fault-lab", Name: "checkout-api"}, &latest)
	before := latest.DeepCopy()
	latest.Status.ObservedGeneration = 0
	_ = c.Status().Patch(ctx, &latest, client.MergeFrom(before))
	v, _ = action.Verify(ctx, ec, snap)
	if v.Healthy {
		t.Error("rollout 进行中应不健康")
	}

	// Scale Verify。
	i2 := execIncident(opsv1alpha1.ActionScaleDeployment, map[string]any{"replicas": float64(4)})
	ec2 := execCtx(c, i2)
	scaleAction := &ScaleDeploymentAction{}
	snap2, _ := scaleAction.Snapshot(ctx, ec2)
	v2, err := scaleAction.Verify(ctx, ec2, snap2)
	if err != nil || !v2.Healthy {
		t.Errorf("Scale Verify 应健康: %+v %v", v2, err)
	}

	// 恢复 observedGeneration（前面子测试改过）。
	var restore appsv1.Deployment
	_ = c.Get(ctx, client.ObjectKey{Namespace: "fault-lab", Name: "checkout-api"}, &restore)
	restoreBefore := restore.DeepCopy()
	restore.Status.ObservedGeneration = 1
	_ = c.Status().Patch(ctx, &restore, client.MergeFrom(restoreBefore))

	// Rollback Verify。
	rs1 := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "checkout-api-rs1", Namespace: "fault-lab",
			Annotations:     map[string]string{"deployment.kubernetes.io/revision": "1"},
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(dep, appsv1.SchemeGroupVersion.WithKind("Deployment"))},
		},
	}
	_ = c.Create(ctx, rs1)
	i3 := execIncident(opsv1alpha1.ActionRollbackDeployment, map[string]any{"targetRevision": float64(1)})
	ec3 := execCtx(c, i3)
	rbAction := &RollbackDeploymentAction{}
	snap3, _ := rbAction.Snapshot(ctx, ec3)
	v3, err := rbAction.Verify(ctx, ec3, snap3)
	if err != nil || !v3.Healthy {
		t.Errorf("Rollback Verify 应健康: %+v %v", v3, err)
	}
	var latest2 appsv1.Deployment
	_ = c.Get(ctx, client.ObjectKey{Namespace: "fault-lab", Name: "checkout-api"}, &latest2)
	before2 := latest2.DeepCopy()
	latest2.Status.UnavailableReplicas = 1
	_ = c.Status().Patch(ctx, &latest2, client.MergeFrom(before2))
	v3, _ = rbAction.Verify(ctx, ec3, snap3)
	if v3.Healthy {
		t.Error("不可用副本应不健康")
	}
}

func TestConfigMapBinaryCompare(t *testing.T) {
	if !configMapBinaryEqual(map[string][]byte{"a": []byte("x")}, map[string][]byte{"a": []byte("x")}) {
		t.Error("相同 binaryData 应相等")
	}
	if configMapBinaryEqual(map[string][]byte{"a": []byte("x")}, map[string][]byte{"a": []byte("y")}) {
		t.Error("不同 binaryData 不应相等")
	}
	if configMapBinaryEqual(map[string][]byte{"a": []byte("x")}, map[string][]byte{}) {
		t.Error("长度不同不应相等")
	}
}

func TestScale_RollbackMissingReplicas(t *testing.T) {
	dep := healthyDeployment()
	c := newExecClient(t, dep)
	i := execIncident(opsv1alpha1.ActionScaleDeployment, map[string]any{"replicas": float64(4)})
	action := &ScaleDeploymentAction{}
	rb, err := action.Rollback(context.Background(), execCtx(c, i), Snapshot{Action: action.Type(), Payload: map[string]any{}})
	if err == nil || rb.RolledBack {
		t.Errorf("缺 replicas 快照应报错: %+v %v", rb, err)
	}
}

func TestRestoreConfigMap_PreflightSameName(t *testing.T) {
	c := newExecClient(t)
	i := execIncident(opsv1alpha1.ActionRestoreConfigMap, map[string]any{
		"targetConfigMap": "same", "backupConfigMap": "same",
	})
	if err := (&RestoreConfigMapAction{}).Preflight(context.Background(), execCtx(c, i)); err == nil {
		t.Error("同名 ConfigMap 应报错")
	}
}

func TestRollback_RollbackMissingTemplateAsError(t *testing.T) {
	dep := healthyDeployment()
	c := newExecClient(t, dep)
	i := execIncident(opsv1alpha1.ActionRollbackDeployment, map[string]any{"targetRevision": float64(1)})
	action := &RollbackDeploymentAction{}
	// 非法 JSON 快照。
	rb, err := action.Rollback(context.Background(), execCtx(c, i),
		Snapshot{Action: action.Type(), Payload: map[string]any{"template": "{bad"}})
	if err == nil || rb.RolledBack {
		t.Errorf("非法 template 应报错: %+v %v", rb, err)
	}
}

// TestPatchResource_SnapshotMissingContainer 覆盖 Snapshot 容器不存在错误分支。
func TestPatchResource_SnapshotMissingContainer(t *testing.T) {
	dep := healthyDeployment()
	c := newExecClient(t, dep)
	i := execIncident(opsv1alpha1.ActionPatchResourceLimit, map[string]any{"container": "missing", "memoryLimit": "512Mi"})
	action := &PatchResourceLimitAction{}
	if _, err := action.Snapshot(context.Background(), execCtx(c, i)); err == nil {
		t.Error("Snapshot 容器不存在应报错")
	}
}

// TestPatchResource_ApplyIdempotent 覆盖 Apply 已执行跳过分支(幂等)。
func TestPatchResource_ApplyIdempotent(t *testing.T) {
	dep := healthyDeployment()
	c := newExecClient(t, dep)
	i := execIncident(opsv1alpha1.ActionPatchResourceLimit, map[string]any{"container": "app", "memoryLimit": "512Mi"})
	ctx := context.Background()
	action := &PatchResourceLimitAction{}
	ec := execCtx(c, i)
	snap, err := action.Snapshot(ctx, ec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := action.Apply(ctx, ec, snap); err != nil {
		t.Fatal(err)
	}
	// 第二次 Apply:OperationID 注解已存在 → 跳过。
	res, err := action.Apply(ctx, ec, snap)
	if err != nil || res.Message == "" || res.OperationID == "" {
		t.Errorf("重复 Apply 应跳过(幂等): %v %+v", err, res)
	}
	var dep2 appsv1.Deployment
	_ = c.Get(ctx, client.ObjectKey{Namespace: "fault-lab", Name: "checkout-api"}, &dep2)
	if got := dep2.Spec.Template.Spec.Containers[0].Resources.Limits[corev1.ResourceMemory]; got.String() != "512Mi" {
		t.Errorf("幂等 Apply 不应重复修改: %s", got.String())
	}
}
