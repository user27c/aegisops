package e2e

import (
	"context"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// pingCluster 通过列出 namespace 验证 context 指向的集群可达。
func pingCluster(cfg *rest.Config) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return err
	}
	_, err = cs.CoreV1().Namespaces().List(ctx, metav1.ListOptions{Limit: 1})
	return err
}
