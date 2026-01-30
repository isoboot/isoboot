package typed

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Client provides access to Kubernetes resources using a typed controller-runtime client.
type Client struct {
	client.Client
	namespace string
}

// NewClient creates a new Kubernetes client from in-cluster config.
func NewClient(namespace string) (*Client, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("get in-cluster config: %w", err)
	}

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = AddToScheme(scheme)

	cl, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("create client: %w", err)
	}

	return &Client{Client: cl, namespace: namespace}, nil
}

// NewClientFromClient creates a Client wrapping an existing client.Client (for testing).
func NewClientFromClient(cl client.Client, namespace string) *Client {
	return &Client{Client: cl, namespace: namespace}
}

// Key returns an ObjectKey for the given name in this client's namespace.
func (c *Client) Key(name string) client.ObjectKey {
	return client.ObjectKey{Namespace: c.namespace, Name: name}
}

// Namespace returns the configured namespace.
func (c *Client) Namespace() string { return c.namespace }

// normalizeMAC converts a MAC address to canonical format (lowercase, dash-separated).
// Returns empty string if MAC contains colons (invalid format).
func normalizeMAC(mac string) string {
	if strings.Contains(mac, ":") {
		return "" // reject colon format
	}
	return strings.ToLower(mac)
}

// FindMachineByMAC finds a Machine by MAC address.
// MAC must be dash-separated (e.g., aa-bb-cc-dd-ee-ff).
func (c *Client) FindMachineByMAC(ctx context.Context, mac string) (*Machine, error) {
	normalizedMAC := normalizeMAC(mac)
	if normalizedMAC == "" {
		return nil, nil // Invalid MAC format (contains colons)
	}

	var list MachineList
	if err := c.List(ctx, &list, client.InNamespace(c.namespace)); err != nil {
		return nil, fmt.Errorf("list machines: %w", err)
	}

	for i := range list.Items {
		if normalizeMAC(list.Items[i].Spec.MAC) == normalizedMAC {
			return &list.Items[i], nil
		}
	}

	return nil, nil // No machine with this MAC
}

// FindProvisionByMAC finds a Provision that references a Machine with the given MAC address.
// MAC must be dash-separated (e.g., aa-bb-cc-dd-ee-ff).
// phase filters by status phase (empty string matches any phase).
func (c *Client) FindProvisionByMAC(ctx context.Context, mac string, phase string) (*Provision, error) {
	normalizedMAC := normalizeMAC(mac)
	if normalizedMAC == "" {
		return nil, nil // Invalid MAC format (contains colons)
	}

	// Build map of MAC -> machine name
	var machineList MachineList
	if err := c.List(ctx, &machineList, client.InNamespace(c.namespace)); err != nil {
		return nil, fmt.Errorf("list machines: %w", err)
	}

	macToMachine := make(map[string]string)
	for _, m := range machineList.Items {
		if machineMAC := normalizeMAC(m.Spec.MAC); machineMAC != "" {
			macToMachine[machineMAC] = m.Name
		}
	}

	machineName, ok := macToMachine[normalizedMAC]
	if !ok {
		return nil, nil // No machine with this MAC
	}

	// Find provision referencing this machine with matching phase
	var provisionList ProvisionList
	if err := c.List(ctx, &provisionList, client.InNamespace(c.namespace)); err != nil {
		return nil, fmt.Errorf("list provisions: %w", err)
	}

	for i := range provisionList.Items {
		p := &provisionList.Items[i]
		if p.Spec.MachineRef == machineName {
			if phase != "" {
				provisionPhase := p.Status.Phase
				if provisionPhase == "" {
					provisionPhase = "Pending"
				}
				if provisionPhase != phase {
					continue
				}
			}
			return p, nil
		}
	}

	return nil, nil // No matching provision for this machine
}

// FindProvisionByHostname finds a Provision that references the given hostname (machine name).
// phase filters by status phase (empty string matches any phase).
func (c *Client) FindProvisionByHostname(ctx context.Context, hostname string, phase string) (*Provision, error) {
	var provisionList ProvisionList
	if err := c.List(ctx, &provisionList, client.InNamespace(c.namespace)); err != nil {
		return nil, fmt.Errorf("list provisions: %w", err)
	}

	for i := range provisionList.Items {
		p := &provisionList.Items[i]
		if p.Spec.MachineRef == hostname {
			if phase != "" {
				provisionPhase := p.Status.Phase
				if provisionPhase == "" {
					provisionPhase = "Pending"
				}
				if provisionPhase != phase {
					continue
				}
			}
			return p, nil
		}
	}

	return nil, nil // No matching provision for this hostname
}

// ListProvisionsByMachine returns all Provisions referencing a Machine.
func (c *Client) ListProvisionsByMachine(ctx context.Context, machineRef string) ([]*Provision, error) {
	var provisionList ProvisionList
	if err := c.List(ctx, &provisionList, client.InNamespace(c.namespace)); err != nil {
		return nil, fmt.Errorf("list provisions: %w", err)
	}

	var result []*Provision
	for i := range provisionList.Items {
		if provisionList.Items[i].Spec.MachineRef == machineRef {
			result = append(result, &provisionList.Items[i])
		}
	}
	return result, nil
}

// UpdateProvisionStatus updates the status of a Provision.
// Pass empty string for ip to leave the existing IP unchanged.
func (c *Client) UpdateProvisionStatus(ctx context.Context, name, phase, message, ip string) error {
	var provision Provision
	if err := c.Get(ctx, c.Key(name), &provision); err != nil {
		return fmt.Errorf("get provision: %w", err)
	}

	// Preserve existing IP if not provided
	if ip == "" {
		ip = provision.Status.IP
	}

	provision.Status = ProvisionStatus{
		Phase:       phase,
		Message:     message,
		LastUpdated: metav1.Now(),
		IP:          ip,
	}

	if err := c.Status().Update(ctx, &provision); err != nil {
		return fmt.Errorf("update status: %w", err)
	}
	return nil
}

// UpdateBootMediaStatus updates the status of a BootMedia.
func (c *Client) UpdateBootMediaStatus(ctx context.Context, name string, status *BootMediaStatus) error {
	if status == nil {
		return fmt.Errorf("status cannot be nil")
	}

	var bm BootMedia
	if err := c.Get(ctx, c.Key(name), &bm); err != nil {
		return fmt.Errorf("get bootmedia: %w", err)
	}

	bm.Status = *status

	if err := c.Status().Update(ctx, &bm); err != nil {
		return fmt.Errorf("update status: %w", err)
	}
	return nil
}
