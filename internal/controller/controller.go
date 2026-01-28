package controller

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"regexp"
	"sync"
	"text/template"
	"time"

	"github.com/isoboot/isoboot/internal/k8s"
)

// validMachineId validates systemd machine-id format (exactly 32 lowercase hex characters)
var validMachineId = regexp.MustCompile(`^[0-9a-f]{32}$`)

// templateFuncs provides custom functions for templates
var templateFuncs = template.FuncMap{
	"b64enc": func(s string) string {
		return base64.StdEncoding.EncodeToString([]byte(s))
	},
	"hasKey": func(m map[string]interface{}, key string) bool {
		_, ok := m[key]
		return ok
	},
}

const (
	reconcileInterval = 10 * time.Second
	inProgressTimeout = 30 * time.Minute
)

// Controller watches Provision CRDs and manages their lifecycle
type Controller struct {
	k8sClient          *k8s.Client
	stopCh             chan struct{}
	host               string
	port               string
	isoBasePath        string
	activeDiskImageDownloads sync.Map // tracks in-progress DiskImage downloads by name
}

// New creates a new controller
func New(k8sClient *k8s.Client) *Controller {
	return &Controller{
		k8sClient: k8sClient,
		stopCh:    make(chan struct{}),
	}
}

// SetHostPort sets the host and port for template rendering
func (c *Controller) SetHostPort(host, port string) {
	c.host = host
	c.port = port
}

// SetISOBasePath sets the base path for ISO storage
func (c *Controller) SetISOBasePath(path string) {
	c.isoBasePath = path
}

// Start begins the controller reconciliation loop
func (c *Controller) Start() {
	log.Printf("Starting controller (reconcile every %s, InProgress timeout %s)", reconcileInterval, inProgressTimeout)
	go c.run()
}

// Stop halts the controller
func (c *Controller) Stop() {
	close(c.stopCh)
}

func (c *Controller) run() {
	// Initial reconcile
	c.reconcile()

	ticker := time.NewTicker(reconcileInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopCh:
			log.Println("Controller stopped")
			return
		case <-ticker.C:
			c.reconcile()
		}
	}
}

func (c *Controller) reconcile() {
	ctx := context.Background()

	// Reconcile DiskImages first (downloads)
	c.reconcileDiskImages(ctx)

	// Then reconcile Provisions
	provisions, err := c.k8sClient.ListProvisions(ctx)
	if err != nil {
		log.Printf("Controller: failed to list provisions: %v", err)
		return
	}

	for _, provision := range provisions {
		c.reconcileProvision(ctx, provision)
	}
}

func (c *Controller) reconcileProvision(ctx context.Context, provision *k8s.Provision) {
	// Validate references before any status changes
	if err := c.validateProvisionRefs(ctx, provision); err != nil {
		if provision.Status.Phase != "ConfigError" || provision.Status.Message != err.Error() {
			log.Printf("Controller: config error for %s: %v", provision.Name, err)
			if updateErr := c.k8sClient.UpdateProvisionStatus(ctx, provision.Name, "ConfigError", err.Error(), ""); updateErr != nil {
				log.Printf("Controller: failed to set ConfigError for %s: %v", provision.Name, updateErr)
			}
		}
		return
	}

	// Check if DiskImage is ready
	diskImageReady, diskImageMsg := c.checkDiskImageReady(ctx, provision)
	if !diskImageReady {
		if provision.Status.Phase != "WaitingForDiskImage" || provision.Status.Message != diskImageMsg {
			log.Printf("Controller: %s waiting for DiskImage: %s", provision.Name, diskImageMsg)
			if err := c.k8sClient.UpdateProvisionStatus(ctx, provision.Name, "WaitingForDiskImage", diskImageMsg, ""); err != nil {
				log.Printf("Controller: failed to set WaitingForDiskImage for %s: %v", provision.Name, err)
			}
		}
		return
	}

	// If previously in ConfigError or WaitingForDiskImage but now valid, reset to Pending
	if provision.Status.Phase == "ConfigError" || provision.Status.Phase == "WaitingForDiskImage" {
		log.Printf("Controller: %s now ready, setting to Pending", provision.Name)
		if err := c.k8sClient.UpdateProvisionStatus(ctx, provision.Name, "Pending", "Ready for boot", ""); err != nil {
			log.Printf("Controller: failed to reset %s to Pending: %v", provision.Name, err)
		}
		return
	}

	// Initialize empty status to Pending
	if provision.Status.Phase == "" {
		log.Printf("Controller: initializing %s status to Pending", provision.Name)
		if err := c.k8sClient.UpdateProvisionStatus(ctx, provision.Name, "Pending", "Initialized by controller", ""); err != nil {
			log.Printf("Controller: failed to set Pending for %s: %v", provision.Name, err)
		}
		return
	}

	// Timeout InProgress provisions
	if provision.Status.Phase == "InProgress" && !provision.Status.LastUpdated.IsZero() {
		age := time.Since(provision.Status.LastUpdated)
		if age > inProgressTimeout {
			log.Printf("Controller: timing out %s (InProgress for %s)", provision.Name, age)
			if err := c.k8sClient.UpdateProvisionStatus(ctx, provision.Name, "Failed", "Timed out waiting for completion", ""); err != nil {
				log.Printf("Controller: failed to set Failed for %s: %v", provision.Name, err)
			}
		}
	}
}

// checkDiskImageReady checks if the DiskImage for this Provision is ready
func (c *Controller) checkDiskImageReady(ctx context.Context, provision *k8s.Provision) (bool, string) {
	// Get BootTarget to find DiskImage reference
	bootTarget, err := c.k8sClient.GetBootTarget(ctx, provision.Spec.BootTargetRef)
	if err != nil {
		return false, fmt.Sprintf("BootTarget '%s' not found", provision.Spec.BootTargetRef)
	}

	// Get DiskImage
	diskImage, err := c.k8sClient.GetDiskImage(ctx, bootTarget.DiskImageRef)
	if err != nil {
		return false, fmt.Sprintf("DiskImage '%s' not found", bootTarget.DiskImageRef)
	}

	return checkDiskImageStatus(diskImage)
}

// checkDiskImageStatus checks the status of a DiskImage and returns whether it's ready
func checkDiskImageStatus(diskImage *k8s.DiskImage) (bool, string) {
	switch diskImage.Status.Phase {
	case "Complete":
		return true, ""
	case "Failed":
		return false, fmt.Sprintf("DiskImage '%s' failed: %s", diskImage.Name, diskImage.Status.Message)
	case "Downloading":
		return false, fmt.Sprintf("DiskImage '%s' downloading (%d%%)", diskImage.Name, diskImage.Status.Progress)
	default:
		return false, fmt.Sprintf("DiskImage '%s' pending", diskImage.Name)
	}
}

// validateProvisionRefs checks that all referenced resources exist and have valid configuration
func (c *Controller) validateProvisionRefs(ctx context.Context, provision *k8s.Provision) error {
	// Validate machineRef exists
	if _, err := c.k8sClient.GetMachine(ctx, provision.Spec.MachineRef); err != nil {
		return fmt.Errorf("Machine '%s' not found", provision.Spec.MachineRef)
	}

	// Validate bootTargetRef (BootTarget)
	if _, err := c.k8sClient.GetBootTarget(ctx, provision.Spec.BootTargetRef); err != nil {
		return fmt.Errorf("BootTarget '%s' not found", provision.Spec.BootTargetRef)
	}

	// Validate responseTemplateRef
	if provision.Spec.ResponseTemplateRef != "" {
		if _, err := c.k8sClient.GetResponseTemplate(ctx, provision.Spec.ResponseTemplateRef); err != nil {
			return fmt.Errorf("ResponseTemplate '%s' not found", provision.Spec.ResponseTemplateRef)
		}
	}

	// Validate configMaps
	for _, cm := range provision.Spec.ConfigMaps {
		if _, err := c.k8sClient.GetConfigMap(ctx, cm); err != nil {
			return fmt.Errorf("ConfigMap '%s' not found", cm)
		}
	}

	// Validate secrets
	for _, secret := range provision.Spec.Secrets {
		if _, err := c.k8sClient.GetSecret(ctx, secret); err != nil {
			return fmt.Errorf("Secret '%s' not found", secret)
		}
	}

	return nil
}

// RenderTemplate renders a template with merged values from ConfigMaps and Secrets
func (c *Controller) RenderTemplate(ctx context.Context, provision *k8s.Provision, templateContent string) (string, error) {
	// Build template data by merging ConfigMaps, then Secrets
	data := make(map[string]interface{})

	// Merge ConfigMaps in order
	for _, cmName := range provision.Spec.ConfigMaps {
		cm, err := c.k8sClient.GetConfigMap(ctx, cmName)
		if err != nil {
			return "", fmt.Errorf("ConfigMap '%s' not found", cmName)
		}
		for k, v := range cm.Data {
			data[k] = v
		}
	}

	// Merge Secrets in order (override ConfigMaps)
	for _, secretName := range provision.Spec.Secrets {
		secret, err := c.k8sClient.GetSecret(ctx, secretName)
		if err != nil {
			return "", fmt.Errorf("Secret '%s' not found", secretName)
		}
		for k, v := range secret.Data {
			data[k] = string(v)
		}
	}

	// Derive SSH public keys from private keys in secrets
	if err := DeriveSSHPublicKeys(data); err != nil {
		return "", fmt.Errorf("derive SSH public keys: %w", err)
	}

	// Add system variables
	data["Host"] = c.host
	data["Port"] = c.port
	data["Hostname"] = provision.Spec.MachineRef
	data["Target"] = provision.Spec.BootTargetRef

	// Parse and execute template
	tmpl, err := template.New("response").Funcs(templateFuncs).Option("missingkey=error").Parse(templateContent)
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}

	return buf.String(), nil
}
