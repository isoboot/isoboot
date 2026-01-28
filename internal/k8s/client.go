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
	provisionGVR = schema.GroupVersionResource{
		Group:    "isoboot.io",
		Version:  "v1alpha1",
		Resource: "provisions",
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
	Name      string
	MAC       string
	MachineId string // Optional systemd machine-id (32 hex chars)
}

// Provision represents a Provision CRD
type Provision struct {
	Name   string
	Spec   ProvisionSpec
	Status ProvisionStatus
}

type ProvisionSpec struct {
	MachineRef          string
	BootTargetRef       string
	ResponseTemplateRef string
	ConfigMaps          []string
	Secrets             []string
	MachineId           string // Optional systemd machine-id (32 hex chars)
}

type ProvisionStatus struct {
	Phase       string
	Message     string
	LastUpdated time.Time
	IP          string
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
}

// BootTarget represents a BootTarget CRD
type BootTarget struct {
	Name                string
	DiskImageRef        string
	IncludeFirmwarePath string
	Template            string
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
		Name:      obj.GetName(),
		MAC:       strings.ToLower(mac),
		MachineId: getString(spec, "machineId"),
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
			}
		}
		if fwStatus, ok := status["firmware"].(map[string]interface{}); ok {
			di.Status.Firmware = &DiskImageVerification{
				FileSizeMatch: getString(fwStatus, "fileSizeMatch"),
				DigestSha256:  getString(fwStatus, "digestSha256"),
				DigestSha512:  getString(fwStatus, "digestSha512"),
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

	diskImageRef := getString(spec, "diskImageRef")
	if diskImageRef == "" {
		return nil, fmt.Errorf("diskImageRef is required")
	}

	return &BootTarget{
		Name:                obj.GetName(),
		DiskImageRef:        diskImageRef,
		IncludeFirmwarePath: getString(spec, "includeFirmwarePath"),
		Template:            getString(spec, "template"),
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

// GetProvision retrieves a Provision by name
func (c *Client) GetProvision(ctx context.Context, name string) (*Provision, error) {
	obj, err := c.dynamicClient.Resource(provisionGVR).Namespace(c.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return parseProvision(obj)
}

// ListProvisions lists all Provisions
func (c *Client) ListProvisions(ctx context.Context) ([]*Provision, error) {
	list, err := c.dynamicClient.Resource(provisionGVR).Namespace(c.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var provisions []*Provision
	for _, item := range list.Items {
		p, err := parseProvision(&item)
		if err != nil {
			continue
		}
		provisions = append(provisions, p)
	}
	return provisions, nil
}

func parseProvision(obj *unstructured.Unstructured) (*Provision, error) {
	spec, ok := obj.Object["spec"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid provision spec")
	}

	// Support both bootTargetRef (new) and target (legacy) for backward compatibility
	bootTargetRef := getString(spec, "bootTargetRef")
	if bootTargetRef == "" {
		bootTargetRef = getString(spec, "target")
	}

	provision := &Provision{
		Name: obj.GetName(),
		Spec: ProvisionSpec{
			MachineRef:          getString(spec, "machineRef"),
			BootTargetRef:       bootTargetRef,
			ResponseTemplateRef: getString(spec, "responseTemplateRef"),
			ConfigMaps:          getStringSlice(spec, "configMaps"),
			Secrets:             getStringSlice(spec, "secrets"),
			MachineId:           getString(spec, "machineId"),
		},
	}

	if status, ok := obj.Object["status"].(map[string]interface{}); ok {
		provision.Status = ProvisionStatus{
			Phase:   getString(status, "phase"),
			Message: getString(status, "message"),
			IP:      getString(status, "ip"),
		}
	}

	return provision, nil
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

// FindProvisionByMAC finds a Provision that references a Machine with the given MAC address
// MAC must be dash-separated (e.g., aa-bb-cc-dd-ee-ff)
// phase filters by status phase (empty string matches any phase)
func (c *Client) FindProvisionByMAC(ctx context.Context, mac string, phase string) (*Provision, error) {
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

	// Find provision referencing this machine with matching phase
	provisions, err := c.ListProvisions(ctx)
	if err != nil {
		return nil, fmt.Errorf("list provisions: %w", err)
	}

	for _, p := range provisions {
		if p.Spec.MachineRef == machineName {
			// Filter by phase if specified
			if phase != "" {
				// Empty status.phase is treated as "Pending"
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

// FindProvisionByHostname finds a Provision that references the given hostname (machine name)
// phase filters by status phase (empty string matches any phase)
func (c *Client) FindProvisionByHostname(ctx context.Context, hostname string, phase string) (*Provision, error) {
	// Find provision referencing this machine (hostname = machine name) with matching phase
	provisions, err := c.ListProvisions(ctx)
	if err != nil {
		return nil, fmt.Errorf("list provisions: %w", err)
	}

	for _, p := range provisions {
		if p.Spec.MachineRef == hostname {
			// Filter by phase if specified
			if phase != "" {
				// Empty status.phase is treated as "Pending"
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

// UpdateProvisionStatus updates the status of a Provision.
// Pass empty string for ip to leave the existing IP unchanged.
func (c *Client) UpdateProvisionStatus(ctx context.Context, name, phase, message, ip string) error {
	obj, err := c.dynamicClient.Resource(provisionGVR).Namespace(c.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get provision: %w", err)
	}

	// Preserve existing IP if not provided
	if ip == "" {
		if existingStatus, ok := obj.Object["status"].(map[string]interface{}); ok {
			if existingIP, ok := existingStatus["ip"].(string); ok {
				ip = existingIP
			}
		}
	}

	status := map[string]interface{}{
		"phase":       phase,
		"message":     message,
		"lastUpdated": time.Now().UTC().Format(time.RFC3339),
	}
	if ip != "" {
		status["ip"] = ip
	}
	obj.Object["status"] = status

	_, err = c.dynamicClient.Resource(provisionGVR).Namespace(c.namespace).UpdateStatus(ctx, obj, metav1.UpdateOptions{})
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
			log.Printf("k8s: failed to parse DiskImage %s (skipping): %v", item.GetName(), err)
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
	}
}
