package controller

import (
	"github.com/isoboot/isoboot/internal/k8s/typed"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = typed.AddToScheme(s)
	return s
}

func newTestTypedClient(objs ...client.Object) *typed.Client {
	cl := fake.NewClientBuilder().
		WithScheme(testScheme()).
		WithObjects(objs...).
		WithStatusSubresource(&typed.Provision{}, &typed.BootMedia{}).
		Build()
	return typed.NewClientFromClient(cl, "default")
}

// newTypedConfigMap is a helper to create a corev1.ConfigMap for testing
func newTypedConfigMap(name string, data map[string]string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Data:       data,
	}
}

// newTypedSecret is a helper to create a corev1.Secret for testing
func newTypedSecret(name string, data map[string][]byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Data:       data,
	}
}
