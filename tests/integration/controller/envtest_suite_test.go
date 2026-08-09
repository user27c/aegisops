// Package controllerintegration verifies CRD behavior against a real API server.
package controllerintegration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

var (
	testEnv    *envtest.Environment
	testClient client.Client
)

func TestMain(m *testing.M) {
	scheme := k8sruntime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		fmt.Fprintln(os.Stderr, "添加 Kubernetes scheme 失败:", err)
		os.Exit(1)
	}
	if err := opsv1alpha1.AddToScheme(scheme); err != nil {
		fmt.Fprintln(os.Stderr, "添加 AegisOps scheme 失败:", err)
		os.Exit(1)
	}

	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join(repositoryRoot(), "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := testEnv.Start()
	if err != nil {
		fmt.Fprintln(os.Stderr, "启动 envtest control plane 失败:", err)
		os.Exit(1)
	}
	testClient, err = client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		fmt.Fprintln(os.Stderr, "创建 envtest client 失败:", err)
		_ = testEnv.Stop()
		os.Exit(1)
	}

	code := m.Run()
	if err := testEnv.Stop(); err != nil {
		fmt.Fprintln(os.Stderr, "停止 envtest control plane 失败:", err)
		code = 1
	}
	os.Exit(code)
}

func TestRemediationPolicyCRDRejectsUnknownAction(t *testing.T) {
	ctx := context.Background()
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "envtest-policy"}}
	if err := testClient.Create(ctx, namespace); err != nil {
		t.Fatalf("创建测试 Namespace: %v", err)
	}
	t.Cleanup(func() { _ = testClient.Delete(context.Background(), namespace) })

	valid := remediationPolicy(namespace.Name, opsv1alpha1.ActionRestartWorkload)
	if err := testClient.Create(ctx, valid); err != nil {
		t.Fatalf("合法 RemediationPolicy 应被 API server 接受: %v", err)
	}

	invalid := remediationPolicy(namespace.Name, opsv1alpha1.ActionType("DeleteNamespace"))
	invalid.Name = "invalid-action"
	if err := testClient.Create(ctx, invalid); err == nil {
		t.Fatal("未知动作应被 CRD CEL 校验拒绝")
	}
}

func remediationPolicy(namespace string, action opsv1alpha1.ActionType) *opsv1alpha1.RemediationPolicy {
	return &opsv1alpha1.RemediationPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "valid-action", Namespace: namespace},
		Spec: opsv1alpha1.RemediationPolicySpec{
			TargetSelector: opsv1alpha1.TargetSelector{Kinds: []string{"Deployment"}},
			Actions: map[opsv1alpha1.ActionType]opsv1alpha1.ActionPolicy{
				action: {Enabled: true, Mode: opsv1alpha1.ModeAuto},
			},
		},
	}
}

func repositoryRoot() string {
	_, file, _, ok := goruntime.Caller(0)
	if !ok {
		panic("无法定位 envtest suite 源文件")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}
