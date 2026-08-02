package targetlock

import (
	coordinationv1 "k8s.io/api/coordination/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
)

type runtimeScheme = runtime.Scheme

var runtimeSchemeInstance = func() *runtimeScheme {
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(coordinationv1.AddToScheme(s))
	return s
}()
