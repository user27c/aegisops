package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

func TestPredicate_IgnoresStatusOnlyUpdate(t *testing.T) {
	oldInc := firingIncident()
	oldInc.Generation = 1
	oldInc.Status.Phase = opsv1alpha1.PhaseCollectingEvidence
	newInc := oldInc.DeepCopy()
	// 只有 Phase 之外的状态变化（热循环场景）。
	newInc.Status.Verification = &opsv1alpha1.VerificationSummary{State: "Pending"}

	p := incidentPredicate()
	if p.Update(event.UpdateEvent{ObjectOld: oldInc, ObjectNew: newInc}) {
		t.Error("无关 Status 更新不应触发 Reconcile")
	}
}

func TestPredicate_TriggersOnGenerationChange(t *testing.T) {
	oldInc := firingIncident()
	oldInc.Generation = 1
	newInc := oldInc.DeepCopy()
	newInc.Generation = 2
	newInc.Spec.Severity = "warning"

	p := incidentPredicate()
	if !p.Update(event.UpdateEvent{ObjectOld: oldInc, ObjectNew: newInc}) {
		t.Error("Generation 变化应触发 Reconcile")
	}
}

func TestPredicate_TriggersOnSourceStatusChange(t *testing.T) {
	oldInc := firingIncident()
	oldInc.Generation = 1
	newInc := oldInc.DeepCopy()
	newInc.Spec.SourceStatus = "resolved"

	p := incidentPredicate()
	if !p.Update(event.UpdateEvent{ObjectOld: oldInc, ObjectNew: newInc}) {
		t.Error("SourceStatus 变化应触发 Reconcile")
	}
}

func TestPredicate_TriggersOnPhaseChange(t *testing.T) {
	oldInc := firingIncident()
	oldInc.Generation = 1
	newInc := oldInc.DeepCopy()
	newInc.Status.Phase = opsv1alpha1.PhaseCollectingEvidence

	p := incidentPredicate()
	if !p.Update(event.UpdateEvent{ObjectOld: oldInc, ObjectNew: newInc}) {
		t.Error("Phase 变化（状态机推进）应触发 Reconcile")
	}
}

func TestPredicate_IgnoresTimelineOnlyChange(t *testing.T) {
	oldInc := firingIncident()
	oldInc.Generation = 1
	oldInc.Status.Phase = opsv1alpha1.PhaseCollectingEvidence
	newInc := oldInc.DeepCopy()
	// 只追加时间线（无关 Status 变化）。
	newInc.Status.Timeline = append(newInc.Status.Timeline, opsv1alpha1.TimelineEntry{
		Type: "Heartbeat",
	})

	p := incidentPredicate()
	if p.Update(event.UpdateEvent{ObjectOld: oldInc, ObjectNew: newInc}) {
		t.Error("无关 Status 更新不应触发 Reconcile")
	}
}

func TestPredicate_TriggersOnDeletion(t *testing.T) {
	oldInc := firingIncident()
	oldInc.Generation = 1
	newInc := oldInc.DeepCopy()
	now := metav1.Now()
	newInc.DeletionTimestamp = &now

	p := incidentPredicate()
	if !p.Update(event.UpdateEvent{ObjectOld: oldInc, ObjectNew: newInc}) {
		t.Error("删除时间戳变化应触发 Reconcile")
	}
}

func TestPredicate_CreateDeleteAlways(t *testing.T) {
	p := incidentPredicate()
	incident := firingIncident()
	if !p.Create(event.CreateEvent{Object: incident}) {
		t.Error("创建应触发")
	}
	if !p.Delete(event.DeleteEvent{Object: incident}) {
		t.Error("删除应触发")
	}
	// 非 Incident 类型对象：保守触发。
	cm := &corev1.ConfigMap{}
	if !p.Update(event.UpdateEvent{ObjectOld: cm, ObjectNew: incident}) {
		t.Error("非 Incident 类型更新应保守触发")
	}
}
