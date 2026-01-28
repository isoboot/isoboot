package controller

import (
	"context"
	"net/http"

	"github.com/isoboot/isoboot/internal/k8s"
	corev1 "k8s.io/api/core/v1"
)

// HTTPDoer abstracts HTTP request execution for testability.
// *http.Client satisfies this interface.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// KubernetesClient abstracts the Kubernetes operations used by the controller.
// *k8s.Client satisfies this interface implicitly.
type KubernetesClient interface {
	ListProvisions(ctx context.Context) ([]*k8s.Provision, error)
	GetProvision(ctx context.Context, name string) (*k8s.Provision, error)
	UpdateProvisionStatus(ctx context.Context, name, phase, message, ip string) error
	ListProvisionsByMachine(ctx context.Context, machineRef string) ([]*k8s.Provision, error)
	GetMachine(ctx context.Context, name string) (*k8s.Machine, error)
	FindMachineByMAC(ctx context.Context, mac string) (*k8s.Machine, error)
	GetBootTarget(ctx context.Context, name string) (*k8s.BootTarget, error)
	GetResponseTemplate(ctx context.Context, name string) (*k8s.ResponseTemplate, error)
	GetConfigMap(ctx context.Context, name string) (*corev1.ConfigMap, error)
	GetSecret(ctx context.Context, name string) (*corev1.Secret, error)
	GetDiskImage(ctx context.Context, name string) (*k8s.DiskImage, error)
	ListDiskImages(ctx context.Context) ([]*k8s.DiskImage, error)
	UpdateDiskImageStatus(ctx context.Context, name string, status *k8s.DiskImageStatus) error
}
