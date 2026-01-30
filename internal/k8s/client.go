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
	MachineId           string // Optional systemd machine-id (32 lowercase hex chars)
}

type ProvisionStatus struct {
	Phase       string
	Message     string
	LastUpdated time.Time
	IP          string
}

// BootTarget represents a BootTarget CRD
type BootTarget struct {
	Name          string
	Files         []BootTargetFile
	CombinedFiles []CombinedFile
	Template      string
	Status        BootTargetStatus
}

// BootTargetFile represents a file to download
type BootTargetFile struct {
	URL         string
	ChecksumURL string
}

// CombinedFile represents a file built by concatenating other files
type CombinedFile struct {
	Name    string
	Sources []string
}

// BootTargetStatus represents the status of a BootTarget
type BootTargetStatus struct {
	Phase         string // Pending, Downloading, Complete, Failed
	Message       string
	Files         []FileStatus
	CombinedFiles []FileStatus
}

// FileStatus represents the download status of a single file
type FileStatus struct {
	Name   string
	Phase  string // Pending, Downloading, Complete, Failed
	SHA256 string
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

	bt := &BootTarget{
		Name:     obj.GetName(),
		Template: getString(spec, "template"),
	}

	// Parse files array
	if filesRaw, ok := spec["files"].([]interface{}); ok {
		for _, item := range filesRaw {
			if m, ok := item.(map[string]interface{}); ok {
				bt.Files = append(bt.Files, BootTargetFile{
					URL:         getString(m, "url"),
					ChecksumURL: getString(m, "checksumURL"),
				})
			}
		}
	}

	// Parse combinedFiles array
	if combinedRaw, ok := spec["combinedFiles"].([]interface{}); ok {
		for _, item := range combinedRaw {
			if m, ok := item.(map[string]interface{}); ok {
				cf := CombinedFile{
					Name: getString(m, "name"),
				}
				if sources, ok := m["sources"].([]interface{}); ok {
					for _, s := range sources {
						if str, ok := s.(string); ok {
							cf.Sources = append(cf.Sources, str)
						}
					}
				}
				bt.CombinedFiles = append(bt.CombinedFiles, cf)
			}
		}
	}

	// Parse status if present
	if status, ok := obj.Object["status"].(map[string]interface{}); ok {
		bt.Status = BootTargetStatus{
			Phase:   getString(status, "phase"),
			Message: getString(status, "message"),
		}
		if filesStatus, ok := status["files"].([]interface{}); ok {
			for _, item := range filesStatus {
				if m, ok := item.(map[string]interface{}); ok {
					bt.Status.Files = append(bt.Status.Files, FileStatus{
						Name:   getString(m, "name"),
						Phase:  getString(m, "phase"),
						SHA256: getString(m, "sha256"),
					})
				}
			}
		}
		if combinedStatus, ok := status["combinedFiles"].([]interface{}); ok {
			for _, item := range combinedStatus {
				if m, ok := item.(map[string]interface{}); ok {
					bt.Status.CombinedFiles = append(bt.Status.CombinedFiles, FileStatus{
						Name:   getString(m, "name"),
						Phase:  getString(m, "phase"),
						SHA256: getString(m, "sha256"),
					})
				}
			}
		}
	}

	return bt, nil
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

// FindMachineByMAC finds a Machine by MAC address
// MAC must be dash-separated (e.g., aa-bb-cc-dd-ee-ff)
func (c *Client) FindMachineByMAC(ctx context.Context, mac string) (*Machine, error) {
	normalizedMAC := normalizeMAC(mac)
	if normalizedMAC == "" {
		return nil, nil // Invalid MAC format (contains colons)
	}

	machines, err := c.ListMachines(ctx)
	if err != nil {
		return nil, fmt.Errorf("list machines: %w", err)
	}

	for _, m := range machines {
		machineMAC := normalizeMAC(m.MAC)
		if machineMAC == normalizedMAC {
			return m, nil
		}
	}

	return nil, nil // No machine with this MAC
}

// ListProvisionsByMachine returns all Provisions referencing a Machine
func (c *Client) ListProvisionsByMachine(ctx context.Context, machineRef string) ([]*Provision, error) {
	provisions, err := c.ListProvisions(ctx)
	if err != nil {
		return nil, fmt.Errorf("list provisions: %w", err)
	}

	var result []*Provision
	for _, p := range provisions {
		if p.Spec.MachineRef == machineRef {
			result = append(result, p)
		}
	}

	return result, nil
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

// ListBootTargets lists all BootTargets
func (c *Client) ListBootTargets(ctx context.Context) ([]*BootTarget, error) {
	list, err := c.dynamicClient.Resource(bootTargetGVR).Namespace(c.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var bootTargets []*BootTarget
	for _, item := range list.Items {
		bt, err := parseBootTarget(&item)
		if err != nil {
			log.Printf("k8s: failed to parse BootTarget %s (skipping): %v", item.GetName(), err)
			continue
		}
		bootTargets = append(bootTargets, bt)
	}
	return bootTargets, nil
}

// UpdateBootTargetStatus updates the status of a BootTarget.
func (c *Client) UpdateBootTargetStatus(ctx context.Context, name string, status *BootTargetStatus) error {
	if status == nil {
		return fmt.Errorf("status cannot be nil")
	}

	obj, err := c.dynamicClient.Resource(bootTargetGVR).Namespace(c.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get boottarget: %w", err)
	}

	statusMap := map[string]interface{}{
		"phase":   status.Phase,
		"message": status.Message,
	}

	if len(status.Files) > 0 {
		var filesArr []interface{}
		for _, f := range status.Files {
			filesArr = append(filesArr, map[string]interface{}{
				"name":   f.Name,
				"phase":  f.Phase,
				"sha256": f.SHA256,
			})
		}
		statusMap["files"] = filesArr
	}

	if len(status.CombinedFiles) > 0 {
		var combinedArr []interface{}
		for _, f := range status.CombinedFiles {
			combinedArr = append(combinedArr, map[string]interface{}{
				"name":   f.Name,
				"phase":  f.Phase,
				"sha256": f.SHA256,
			})
		}
		statusMap["combinedFiles"] = combinedArr
	}

	obj.Object["status"] = statusMap

	_, err = c.dynamicClient.Resource(bootTargetGVR).Namespace(c.namespace).UpdateStatus(ctx, obj, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("update status: %w", err)
	}

	return nil
}
