package k8s

import (
	"context"
	"fmt"
	"log"
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
	diskImageGVR = schema.GroupVersionResource{
		Group:    "isoboot.io",
		Version:  "v1alpha1",
		Resource: "diskimages",
	}
	bootTargetGVR = schema.GroupVersionResource{
		Group:    "isoboot.io",
		Version:  "v1alpha1",
		Resource: "boottargets",
	}
	responseTemplateGVR = schema.GroupVersionResource{
		Group:    "isoboot.io",
		Version:  "v1alpha1",
		Resource: "responsetemplates",
	}
)

// Machine represents a Machine CRD
type Machine struct {
	Name string
	MAC  string
}

// Deploy represents a Deploy CRD
type Deploy struct {
	Name   string
	Spec   DeploySpec
	Status DeployStatus
}

type DeploySpec struct {
	MachineRef          string
	BootTargetRef       string
	ResponseTemplateRef string
	ConfigMaps          []string
	Secrets             []string
}

type DeployStatus struct {
	Phase       string
	Message     string
	LastUpdated time.Time
}

// DiskImage represents a DiskImage CRD
type DiskImage struct {
	Name     string
	ISO      string
	Firmware string
	Status   DiskImageStatus
}

// DiskImageStatus represents the status of a DiskImage
type DiskImageStatus struct {
	Phase    string // Pending, Downloading, Complete, Failed
	Progress int    // 0-100
	Message  string
	ISO      *DiskImageVerification
	Firmware *DiskImageVerification
}

// DiskImageVerification represents verification status for a file
type DiskImageVerification struct {
	FileSizeMatch string // pending, processing, verified, failed
	DigestSha256  string // pending, processing, verified, failed, not_found
	DigestSha512  string // pending, processing, verified, failed, not_found
	DigestMd5     string // pending, processing, verified, failed, not_found
}

// BootTarget represents a BootTarget CRD
type BootTarget struct {
	Name         string
	DiskImageRef string
	Template     string
}

// ResponseTemplate represents a ResponseTemplate CRD
type ResponseTemplate struct {
	Name  string
	Files map[string]string
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

	mac, ok := spec["mac"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid mac")
	}

	return &Machine{
		Name: obj.GetName(),
		MAC:  strings.ToLower(mac),
	}, nil
}

// GetDiskImage retrieves a DiskImage by name
func (c *Client) GetDiskImage(ctx context.Context, name string) (*DiskImage, error) {
	obj, err := c.dynamicClient.Resource(diskImageGVR).Namespace(c.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return parseDiskImage(obj)
}

func parseDiskImage(obj *unstructured.Unstructured) (*DiskImage, error) {
	spec, ok := obj.Object["spec"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid diskimage spec")
	}

	di := &DiskImage{
		Name:     obj.GetName(),
		ISO:      getString(spec, "iso"),
		Firmware: getString(spec, "firmware"),
	}

	// Parse status if present
	if status, ok := obj.Object["status"].(map[string]interface{}); ok {
		di.Status = DiskImageStatus{
			Phase:    getString(status, "phase"),
			Progress: getInt(status, "progress"),
			Message:  getString(status, "message"),
		}
		if isoStatus, ok := status["iso"].(map[string]interface{}); ok {
			di.Status.ISO = &DiskImageVerification{
				FileSizeMatch: getString(isoStatus, "fileSizeMatch"),
				DigestSha256:  getString(isoStatus, "digestSha256"),
				DigestSha512:  getString(isoStatus, "digestSha512"),
				DigestMd5:     getString(isoStatus, "digestMd5"),
			}
		}
		if fwStatus, ok := status["firmware"].(map[string]interface{}); ok {
			di.Status.Firmware = &DiskImageVerification{
				FileSizeMatch: getString(fwStatus, "fileSizeMatch"),
				DigestSha256:  getString(fwStatus, "digestSha256"),
				DigestSha512:  getString(fwStatus, "digestSha512"),
				DigestMd5:     getString(fwStatus, "digestMd5"),
			}
		}
	}

	return di, nil
}

// GetBootTarget retrieves a BootTarget by name
func (c *Client) GetBootTarget(ctx context.Context, name string) (*BootTarget, error) {
	obj, err := c.dynamicClient.Resource(bootTargetGVR).Namespace(c.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return parseBootTarget(obj)
}

func parseBootTarget(obj *unstructured.Unstructured) (*BootTarget, error) {
	spec, ok := obj.Object["spec"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid boottarget spec")
	}

	return &BootTarget{
		Name:         obj.GetName(),
		DiskImageRef: getString(spec, "diskImageRef"),
		Template:     getString(spec, "template"),
	}, nil
}

// GetResponseTemplate retrieves a ResponseTemplate by name
func (c *Client) GetResponseTemplate(ctx context.Context, name string) (*ResponseTemplate, error) {
	obj, err := c.dynamicClient.Resource(responseTemplateGVR).Namespace(c.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return parseResponseTemplate(obj)
}

func parseResponseTemplate(obj *unstructured.Unstructured) (*ResponseTemplate, error) {
	spec, ok := obj.Object["spec"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid responsetemplate spec")
	}

	return &ResponseTemplate{
		Name:  obj.GetName(),
		Files: getStringMap(spec, "files"),
	}, nil
}

// GetSecret retrieves a Secret by name
func (c *Client) GetSecret(ctx context.Context, name string) (*corev1.Secret, error) {
	return c.clientset.CoreV1().Secrets(c.namespace).Get(ctx, name, metav1.GetOptions{})
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

	// Support both bootTargetRef (new) and target (legacy) for backward compatibility
	bootTargetRef := getString(spec, "bootTargetRef")
	if bootTargetRef == "" {
		bootTargetRef = getString(spec, "target")
	}

	deploy := &Deploy{
		Name: obj.GetName(),
		Spec: DeploySpec{
			MachineRef:          getString(spec, "machineRef"),
			BootTargetRef:       bootTargetRef,
			ResponseTemplateRef: getString(spec, "responseTemplateRef"),
			ConfigMaps:          getStringSlice(spec, "configMaps"),
			Secrets:             getStringSlice(spec, "secrets"),
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

func getInt(m map[string]interface{}, key string) int {
	switch v := m[key].(type) {
	case int:
		return v
	case int32:
		return int(v)
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return 0
}

func getStringSlice(m map[string]interface{}, key string) []string {
	v, ok := m[key].([]interface{})
	if !ok {
		return nil
	}
	result := make([]string, 0, len(v))
	for _, item := range v {
		if s, ok := item.(string); ok {
			result = append(result, s)
		}
	}
	return result
}

func getStringMap(m map[string]interface{}, key string) map[string]string {
	v, ok := m[key].(map[string]interface{})
	if !ok {
		return nil
	}
	result := make(map[string]string, len(v))
	for k, val := range v {
		if s, ok := val.(string); ok {
			result[k] = s
		}
	}
	return result
}

// normalizeMAC converts a MAC address to canonical format (lowercase, dash-separated)
// Returns empty string if MAC contains colons (invalid format)
func normalizeMAC(mac string) string {
	if strings.Contains(mac, ":") {
		return "" // reject colon format
	}
	return strings.ToLower(mac)
}

// FindDeployByMAC finds a Deploy that references a Machine with the given MAC address
// MAC must be dash-separated (e.g., aa-bb-cc-dd-ee-ff)
// phase filters by status phase (empty string matches any phase)
func (c *Client) FindDeployByMAC(ctx context.Context, mac string, phase string) (*Deploy, error) {
	normalizedMAC := normalizeMAC(mac)
	if normalizedMAC == "" {
		return nil, nil // Invalid MAC format (contains colons)
	}

	// Build map of machine name -> MAC for O(n) lookup
	machines, err := c.ListMachines(ctx)
	if err != nil {
		return nil, fmt.Errorf("list machines: %w", err)
	}

	macToMachine := make(map[string]string) // MAC -> machine name
	for _, m := range machines {
		machineMAC := normalizeMAC(m.MAC)
		if machineMAC != "" {
			macToMachine[machineMAC] = m.Name
		}
	}

	// Find machine name for this MAC
	machineName, ok := macToMachine[normalizedMAC]
	if !ok {
		return nil, nil // No machine with this MAC
	}

	// Find deploy referencing this machine with matching phase
	deploys, err := c.ListDeploys(ctx)
	if err != nil {
		return nil, fmt.Errorf("list deploys: %w", err)
	}

	for _, d := range deploys {
		if d.Spec.MachineRef == machineName {
			// Filter by phase if specified
			if phase != "" {
				// Empty status.phase is treated as "Pending"
				deployPhase := d.Status.Phase
				if deployPhase == "" {
					deployPhase = "Pending"
				}
				if deployPhase != phase {
					continue
				}
			}
			return d, nil
		}
	}

	return nil, nil // No matching deploy for this machine
}

// FindDeployByHostname finds a Deploy that references the given hostname (machine name)
// phase filters by status phase (empty string matches any phase)
func (c *Client) FindDeployByHostname(ctx context.Context, hostname string, phase string) (*Deploy, error) {
	// Find deploy referencing this machine (hostname = machine name) with matching phase
	deploys, err := c.ListDeploys(ctx)
	if err != nil {
		return nil, fmt.Errorf("list deploys: %w", err)
	}

	for _, d := range deploys {
		if d.Spec.MachineRef == hostname {
			// Filter by phase if specified
			if phase != "" {
				// Empty status.phase is treated as "Pending"
				deployPhase := d.Status.Phase
				if deployPhase == "" {
					deployPhase = "Pending"
				}
				if deployPhase != phase {
					continue
				}
			}
			return d, nil
		}
	}

	return nil, nil // No matching deploy for this hostname
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

// ListDiskImages lists all DiskImages
func (c *Client) ListDiskImages(ctx context.Context) ([]*DiskImage, error) {
	list, err := c.dynamicClient.Resource(diskImageGVR).Namespace(c.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var diskImages []*DiskImage
	for _, item := range list.Items {
		di, err := parseDiskImage(&item)
		if err != nil {
			log.Printf("k8s: failed to parse DiskImage %s: %v", item.GetName(), err)
			continue
		}
		diskImages = append(diskImages, di)
	}
	return diskImages, nil
}

// UpdateDiskImageStatus updates the status of a DiskImage.
// Note: This performs a full replacement of the status subresource.
// Callers should provide the complete desired status; any fields not
// included (e.g., ISO/Firmware set to nil) will be cleared.
func (c *Client) UpdateDiskImageStatus(ctx context.Context, name string, status *DiskImageStatus) error {
	if status == nil {
		return fmt.Errorf("status cannot be nil")
	}

	obj, err := c.dynamicClient.Resource(diskImageGVR).Namespace(c.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get diskimage: %w", err)
	}

	statusMap := map[string]interface{}{
		"phase":    status.Phase,
		"progress": status.Progress,
		"message":  status.Message,
	}

	if status.ISO != nil {
		statusMap["iso"] = verificationToMap(status.ISO)
	}

	if status.Firmware != nil {
		statusMap["firmware"] = verificationToMap(status.Firmware)
	}

	obj.Object["status"] = statusMap

	_, err = c.dynamicClient.Resource(diskImageGVR).Namespace(c.namespace).UpdateStatus(ctx, obj, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("update status: %w", err)
	}

	return nil
}

// verificationToMap converts a DiskImageVerification to a map for status updates
func verificationToMap(v *DiskImageVerification) map[string]interface{} {
	return map[string]interface{}{
		"fileSizeMatch": v.FileSizeMatch,
		"digestSha256":  v.DigestSha256,
		"digestSha512":  v.DigestSha512,
		"digestMd5":     v.DigestMd5,
	}
}
