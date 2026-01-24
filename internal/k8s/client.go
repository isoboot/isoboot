package k8s

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

var (
	machineGVR = schema.GroupVersionResource{
		Group:    "isoboot.io",
		Version:  "v1alpha1",
		Resource: "machines",
	}
	deployGVR = schema.GroupVersionResource{
		Group:    "isoboot.io",
		Version:  "v1alpha1",
		Resource: "deploys",
	}
)

// Machine represents a Machine CRD
type Machine struct {
	Name         string
	MACAddresses []string
}

// Deploy represents a Deploy CRD
type Deploy struct {
	Name   string
	Spec   DeploySpec
	Status DeployStatus
}

type DeploySpec struct {
	MachineRef string
	Target     string
}

type DeployStatus struct {
	Phase       string
	Message     string
	LastUpdated time.Time
}

// Client provides access to Kubernetes resources
type Client struct {
	clientset     *kubernetes.Clientset
	dynamicClient dynamic.Interface
	namespace     string
}

// NewClient creates a new Kubernetes client
func NewClient(namespace string) (*Client, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("get in-cluster config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create clientset: %w", err)
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create dynamic client: %w", err)
	}

	return &Client{
		clientset:     clientset,
		dynamicClient: dynamicClient,
		namespace:     namespace,
	}, nil
}

// GetConfigMap retrieves a ConfigMap by name
func (c *Client) GetConfigMap(ctx context.Context, name string) (*corev1.ConfigMap, error) {
	return c.clientset.CoreV1().ConfigMaps(c.namespace).Get(ctx, name, metav1.GetOptions{})
}

// GetMachine retrieves a Machine by name
func (c *Client) GetMachine(ctx context.Context, name string) (*Machine, error) {
	obj, err := c.dynamicClient.Resource(machineGVR).Namespace(c.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return parseMachine(obj)
}

// ListMachines lists all Machines
func (c *Client) ListMachines(ctx context.Context) ([]*Machine, error) {
	list, err := c.dynamicClient.Resource(machineGVR).Namespace(c.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var machines []*Machine
	for _, item := range list.Items {
		m, err := parseMachine(&item)
		if err != nil {
			continue
		}
		machines = append(machines, m)
	}
	return machines, nil
}

func parseMachine(obj *unstructured.Unstructured) (*Machine, error) {
	spec, ok := obj.Object["spec"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid machine spec")
	}

	macAddrsRaw, ok := spec["macAddresses"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid macAddresses")
	}

	var macAddrs []string
	for _, m := range macAddrsRaw {
		if s, ok := m.(string); ok {
			macAddrs = append(macAddrs, strings.ToLower(s))
		}
	}

	return &Machine{
		Name:         obj.GetName(),
		MACAddresses: macAddrs,
	}, nil
}

// GetDeploy retrieves a Deploy by name
func (c *Client) GetDeploy(ctx context.Context, name string) (*Deploy, error) {
	obj, err := c.dynamicClient.Resource(deployGVR).Namespace(c.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return parseDeploy(obj)
}

// ListDeploys lists all Deploys
func (c *Client) ListDeploys(ctx context.Context) ([]*Deploy, error) {
	list, err := c.dynamicClient.Resource(deployGVR).Namespace(c.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var deploys []*Deploy
	for _, item := range list.Items {
		d, err := parseDeploy(&item)
		if err != nil {
			continue
		}
		deploys = append(deploys, d)
	}
	return deploys, nil
}

func parseDeploy(obj *unstructured.Unstructured) (*Deploy, error) {
	spec, ok := obj.Object["spec"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid deploy spec")
	}

	deploy := &Deploy{
		Name: obj.GetName(),
		Spec: DeploySpec{
			MachineRef: getString(spec, "machineRef"),
			Target:     getString(spec, "target"),
		},
	}

	if status, ok := obj.Object["status"].(map[string]interface{}); ok {
		deploy.Status = DeployStatus{
			Phase:   getString(status, "phase"),
			Message: getString(status, "message"),
		}
	}

	return deploy, nil
}

func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// FindDeployByMAC finds a Deploy that references a Machine with the given MAC address
func (c *Client) FindDeployByMAC(ctx context.Context, mac string) (*Deploy, error) {
	mac = strings.ToLower(mac)

	// List all machines to find one with this MAC
	machines, err := c.ListMachines(ctx)
	if err != nil {
		return nil, fmt.Errorf("list machines: %w", err)
	}

	var matchingMachine *Machine
	for _, m := range machines {
		for _, addr := range m.MACAddresses {
			if strings.ToLower(addr) == mac {
				matchingMachine = m
				break
			}
		}
		if matchingMachine != nil {
			break
		}
	}

	if matchingMachine == nil {
		return nil, nil // No machine with this MAC
	}

	// Find deploy referencing this machine
	deploys, err := c.ListDeploys(ctx)
	if err != nil {
		return nil, fmt.Errorf("list deploys: %w", err)
	}

	for _, d := range deploys {
		if d.Spec.MachineRef == matchingMachine.Name {
			return d, nil
		}
	}

	return nil, nil // No deploy for this machine
}

// UpdateDeployStatus updates the status of a Deploy
func (c *Client) UpdateDeployStatus(ctx context.Context, name, phase, message string) error {
	obj, err := c.dynamicClient.Resource(deployGVR).Namespace(c.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get deploy: %w", err)
	}

	status := map[string]interface{}{
		"phase":       phase,
		"message":     message,
		"lastUpdated": time.Now().UTC().Format(time.RFC3339),
	}
	obj.Object["status"] = status

	_, err = c.dynamicClient.Resource(deployGVR).Namespace(c.namespace).UpdateStatus(ctx, obj, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("update status: %w", err)
	}

	return nil
}
