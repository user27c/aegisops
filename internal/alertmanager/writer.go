package alertmanager

import (
	"context"
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/clock"
	"sigs.k8s.io/controller-runtime/pkg/client"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

// IncidentLabelFingerprint 是 Incident 上的指纹标签。
const IncidentLabelFingerprint = "ops.aegis.io/fingerprint"

// maxEpisodeAttempts 是同一指纹终态后再现时尝试的后缀数上限。
const maxEpisodeAttempts = 10

// KubernetesWriter 通过 controller-runtime client 写入 AIOpsIncident。
type KubernetesWriter struct {
	client client.Client
	clock  clock.Clock
}

// NewKubernetesWriter 创建 K8s Incident 写入器。
func NewKubernetesWriter(c client.Client, clk clock.Clock) *KubernetesWriter {
	return &KubernetesWriter{client: c, clock: clk}
}

// KubernetesResolver 通过 K8s API 解析目标 UID。
type KubernetesResolver struct {
	client client.Client
}

// NewKubernetesResolver 创建 K8s 目标解析器。
func NewKubernetesResolver(c client.Client) *KubernetesResolver {
	return &KubernetesResolver{client: c}
}

// ResolveTargetUID 查询目标 Deployment 并返回 UID。
func (r *KubernetesResolver) ResolveTargetUID(ctx context.Context, ref opsv1alpha1.TargetReference) (types.UID, error) {
	var dep appsv1.Deployment
	if err := r.client.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}, &dep); err != nil {
		return "", err
	}
	return dep.UID, nil
}

// UpsertFromAlert 按指纹去重写入 Incident。
//
// 规则：
//   - 同名 Incident 不存在 → 创建（Detected）。
//   - 存在且未终结 → 更新 LastReceivedAt/SourceStatus；resolved 写 ResolvedAt，不碰 Phase。
//   - 存在且已终结 + firing → 以 name-1、name-2… 创建新 episode。
//   - 存在且已终结 + resolved → 补写 ResolvedAt（迟到 resolved）。
func (w *KubernetesWriter) UpsertFromAlert(ctx context.Context, a NormalizedAlert) (UpsertResult, error) {
	fingerprint := BuildFingerprint(a)
	baseName := IncidentName(a.AlertName, fingerprint)

	// 1. 基础名称已存在 → 更新或让路（终态 + firing 时返回 found=false 走新 episode）。
	if res, found, err := w.upsertExisting(ctx, a, baseName); err != nil {
		return UpsertResult{}, err
	} else if found {
		return res, nil
	}

	// 2. 尝试创建；AlreadyExists（并发竞态或终态后重演）则递增后缀。
	for attempt := 0; attempt < maxEpisodeAttempts; attempt++ {
		name := baseName
		if attempt > 0 {
			name = fmt.Sprintf("%s-%d", baseName, attempt)
		}

		incident := newIncident(w.clock, a, name, fingerprint)
		err := w.client.Create(ctx, incident)
		if err == nil {
			w.setDetectedPhase(ctx, incident)
			return UpsertResult{IncidentName: name, Created: true}, nil
		}
		if !apierrors.IsAlreadyExists(err) {
			return UpsertResult{}, err
		}

		// 并发竞态：对方刚创建 → 尝试更新；终态 + firing → 继续递增。
		if res, found, err := w.upsertExisting(ctx, a, name); err != nil {
			return UpsertResult{}, err
		} else if found {
			return res, nil
		}
	}
	return UpsertResult{}, fmt.Errorf("指纹 %s 的 episode 后缀已用完", fingerprint)
}

// upsertExisting 尝试更新已有同名 Incident；NotFound 或"终态+新firing"时返回 found=false。
func (w *KubernetesWriter) upsertExisting(ctx context.Context, a NormalizedAlert, name string) (UpsertResult, bool, error) {
	var existing opsv1alpha1.AIOpsIncident
	err := w.client.Get(ctx, client.ObjectKey{Namespace: a.Target.Namespace, Name: name}, &existing)
	if apierrors.IsNotFound(err) {
		return UpsertResult{}, false, nil
	}
	if err != nil {
		return UpsertResult{}, false, err
	}

	// 终态后新 firing → 新 episode（由调用方递增后缀）。
	if existing.IsTerminal() && a.Status == StatusFiring {
		return UpsertResult{}, false, nil
	}

	now := w.clock.Now()
	patch := client.MergeFrom(existing.DeepCopy())
	existing.Spec.LastReceivedAt = metav1.NewTime(now)
	if a.Status == StatusResolved {
		existing.Spec.SourceStatus = StatusResolved
		if existing.Spec.ResolvedAt == nil {
			t := metav1.NewTime(now)
			existing.Spec.ResolvedAt = &t
		}
	} else if existing.Spec.SourceStatus == "" {
		existing.Spec.SourceStatus = StatusFiring
	}
	if err := w.client.Patch(ctx, &existing, patch); err != nil {
		return UpsertResult{}, false, err
	}

	res := UpsertResult{IncidentName: name, Updated: true}
	if a.Status == StatusResolved {
		res.Created = false
	}
	return res, true, nil
}

// setDetectedPhase 创建后把 Phase 置为 Detected（status subresource 独立更新）。
// 失败不阻塞：Controller 会自行推进 Phase。
func (w *KubernetesWriter) setDetectedPhase(ctx context.Context, incident *opsv1alpha1.AIOpsIncident) {
	patch := client.MergeFrom(incident.DeepCopy())
	incident.Status.Phase = opsv1alpha1.PhaseDetected
	incident.Status.ObservedGeneration = incident.Generation
	if err := w.client.Status().Patch(ctx, incident, patch); err != nil {
		_ = err // 创建已成功；Phase 留空由 Controller 补齐
	}
}

// newIncident 构造初始 Incident。
func newIncident(clk clock.Clock, a NormalizedAlert, name, fingerprint string) *opsv1alpha1.AIOpsIncident {
	startedAt := a.StartsAt
	if startedAt.IsZero() {
		startedAt = clk.Now()
	}
	lastReceived := clk.Now()
	if a.StartsAt.After(lastReceived) {
		lastReceived = a.StartsAt
	}

	labels := make(map[string]string, len(a.Labels)+1)
	for k, v := range a.Labels {
		labels[k] = v
	}
	// K8s label 值上限 63 字节；SHA256 hex 为 64 字节，截断保留。
	fpHex := strings.TrimPrefix(fingerprint, "sha256:")
	if len(fpHex) > 63 {
		fpHex = fpHex[:63]
	}
	labels[IncidentLabelFingerprint] = fpHex

	return &opsv1alpha1.AIOpsIncident{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: a.Target.Namespace,
			Labels:    labels,
		},
		Spec: opsv1alpha1.AIOpsIncidentSpec{
			Fingerprint:       fingerprint,
			Cluster:           a.Cluster,
			AlertName:         a.AlertName,
			Severity:          a.Severity,
			SourceStatus:      a.Status,
			TargetRef:         a.Target,
			StartedAt:         metav1.NewTime(startedAt),
			LastReceivedAt:    metav1.NewTime(lastReceived),
			CommonLabels:      a.Labels,
			CommonAnnotations: a.Annotations,
		},
	}
}
