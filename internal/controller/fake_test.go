package controller

import (
	"context"
	"fmt"
	"sync"

	"github.com/isoboot/isoboot/internal/k8s"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// fakeK8sClient implements KubernetesClient with in-memory data for testing.
type fakeK8sClient struct {
	mu                  sync.Mutex
	provisions          map[string]*k8s.Provision
	machines            map[string]*k8s.Machine
	bootTargets         map[string]*k8s.BootTarget
	bootMedias          map[string]*k8s.BootMedia
	responseTemplates   map[string]*k8s.ResponseTemplate
	configMaps          map[string]*corev1.ConfigMap
	secrets             map[string]*corev1.Secret
	bootMediaStatuses   map[string]*k8s.BootMediaStatus
	provisionStatuses   map[string]k8s.ProvisionStatus
}

func newFakeK8sClient() *fakeK8sClient {
	return &fakeK8sClient{
		provisions:        make(map[string]*k8s.Provision),
		machines:          make(map[string]*k8s.Machine),
		bootTargets:       make(map[string]*k8s.BootTarget),
		bootMedias:        make(map[string]*k8s.BootMedia),
		responseTemplates: make(map[string]*k8s.ResponseTemplate),
		configMaps:        make(map[string]*corev1.ConfigMap),
		secrets:           make(map[string]*corev1.Secret),
		bootMediaStatuses: make(map[string]*k8s.BootMediaStatus),
		provisionStatuses: make(map[string]k8s.ProvisionStatus),
	}
}

func (f *fakeK8sClient) ListProvisions(_ context.Context) ([]*k8s.Provision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var result []*k8s.Provision
	for _, p := range f.provisions {
		// Apply any recorded status updates
		if s, ok := f.provisionStatuses[p.Name]; ok {
			cp := *p
			cp.Status = s
			result = append(result, &cp)
		} else {
			result = append(result, p)
		}
	}
	return result, nil
}

func (f *fakeK8sClient) GetProvision(_ context.Context, name string) (*k8s.Provision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.provisions[name]
	if !ok {
		return nil, fmt.Errorf("provision %q not found", name)
	}
	cp := *p
	if s, ok := f.provisionStatuses[name]; ok {
		cp.Status = s
	}
	return &cp, nil
}

func (f *fakeK8sClient) UpdateProvisionStatus(_ context.Context, name, phase, message, ip string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.provisions[name]; !ok {
		return fmt.Errorf("provision %q not found", name)
	}
	f.provisionStatuses[name] = k8s.ProvisionStatus{
		Phase:   phase,
		Message: message,
		IP:      ip,
	}
	return nil
}

func (f *fakeK8sClient) ListProvisionsByMachine(_ context.Context, machineRef string) ([]*k8s.Provision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var result []*k8s.Provision
	for _, p := range f.provisions {
		if p.Spec.MachineRef == machineRef {
			cp := *p
			if s, ok := f.provisionStatuses[p.Name]; ok {
				cp.Status = s
			}
			result = append(result, &cp)
		}
	}
	return result, nil
}

func (f *fakeK8sClient) GetMachine(_ context.Context, name string) (*k8s.Machine, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.machines[name]
	if !ok {
		return nil, fmt.Errorf("machine %q not found", name)
	}
	return m, nil
}

func (f *fakeK8sClient) FindMachineByMAC(_ context.Context, mac string) (*k8s.Machine, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.machines {
		if m.MAC == mac {
			return m, nil
		}
	}
	return nil, nil
}

func (f *fakeK8sClient) GetBootTarget(_ context.Context, name string) (*k8s.BootTarget, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	bt, ok := f.bootTargets[name]
	if !ok {
		return nil, fmt.Errorf("boottarget %q not found", name)
	}
	return bt, nil
}

func (f *fakeK8sClient) ListBootTargets(_ context.Context) ([]*k8s.BootTarget, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var result []*k8s.BootTarget
	for _, bt := range f.bootTargets {
		result = append(result, bt)
	}
	return result, nil
}

func (f *fakeK8sClient) GetBootMedia(_ context.Context, name string) (*k8s.BootMedia, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	bm, ok := f.bootMedias[name]
	if !ok {
		return nil, fmt.Errorf("bootmedia %q not found", name)
	}
	cp := *bm
	if s, ok := f.bootMediaStatuses[name]; ok {
		cp.Status = *s
	}
	return &cp, nil
}

func (f *fakeK8sClient) ListBootMedias(_ context.Context) ([]*k8s.BootMedia, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var result []*k8s.BootMedia
	for _, bm := range f.bootMedias {
		cp := *bm
		if s, ok := f.bootMediaStatuses[bm.Name]; ok {
			cp.Status = *s
		}
		result = append(result, &cp)
	}
	return result, nil
}

func (f *fakeK8sClient) UpdateBootMediaStatus(_ context.Context, name string, status *k8s.BootMediaStatus) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.bootMedias[name]; !ok {
		return fmt.Errorf("bootmedia %q not found", name)
	}
	f.bootMediaStatuses[name] = status
	return nil
}

func (f *fakeK8sClient) GetResponseTemplate(_ context.Context, name string) (*k8s.ResponseTemplate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rt, ok := f.responseTemplates[name]
	if !ok {
		return nil, fmt.Errorf("responsetemplate %q not found", name)
	}
	return rt, nil
}

func (f *fakeK8sClient) GetConfigMap(_ context.Context, name string) (*corev1.ConfigMap, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cm, ok := f.configMaps[name]
	if !ok {
		return nil, fmt.Errorf("configmap %q not found", name)
	}
	return cm, nil
}

func (f *fakeK8sClient) GetSecret(_ context.Context, name string) (*corev1.Secret, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.secrets[name]
	if !ok {
		return nil, fmt.Errorf("secret %q not found", name)
	}
	return s, nil
}

// helper to get the current provision status recorded by the fake
func (f *fakeK8sClient) getProvisionStatus(name string) (k8s.ProvisionStatus, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.provisionStatuses[name]
	return s, ok
}

// helper to get the current boot media status recorded by the fake
func (f *fakeK8sClient) getBootMediaStatus(name string) (*k8s.BootMediaStatus, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.bootMediaStatuses[name]
	return s, ok
}

// newConfigMap is a helper to create a corev1.ConfigMap for testing
func newConfigMap(name string, data map[string]string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Data:       data,
	}
}

// newSecret is a helper to create a corev1.Secret for testing
func newSecret(name string, data map[string][]byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Data:       data,
	}
}
