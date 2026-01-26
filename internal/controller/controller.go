package controller

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"sync"
	"text/template"
	"time"

	"github.com/isoboot/isoboot/internal/k8s"
)

const (
	reconcileInterval = 10 * time.Second
	inProgressTimeout = 30 * time.Minute
)

// Controller watches Deploy CRDs and manages their lifecycle
type Controller struct {
	k8sClient          *k8s.Client
	stopCh             chan struct{}
	host               string
	port               string
	isoBasePath        string
	activeDownloads    sync.Map // tracks in-progress DiskImage downloads by name
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

	// Then reconcile Deploys
	deploys, err := c.k8sClient.ListDeploys(ctx)
	if err != nil {
		log.Printf("Controller: failed to list deploys: %v", err)
		return
	}

	for _, deploy := range deploys {
		c.reconcileDeploy(ctx, deploy)
	}
}

func (c *Controller) reconcileDeploy(ctx context.Context, deploy *k8s.Deploy) {
	// Validate references before any status changes
	if err := c.validateDeployRefs(ctx, deploy); err != nil {
		if deploy.Status.Phase != "ConfigError" || deploy.Status.Message != err.Error() {
			log.Printf("Controller: config error for %s: %v", deploy.Name, err)
			if updateErr := c.k8sClient.UpdateDeployStatus(ctx, deploy.Name, "ConfigError", err.Error()); updateErr != nil {
				log.Printf("Controller: failed to set ConfigError for %s: %v", deploy.Name, updateErr)
			}
		}
		return
	}

	// Check if DiskImage is ready
	diskImageReady, diskImageMsg := c.checkDiskImageReady(ctx, deploy)
	if !diskImageReady {
		if deploy.Status.Phase != "WaitingForDiskImage" || deploy.Status.Message != diskImageMsg {
			log.Printf("Controller: %s waiting for DiskImage: %s", deploy.Name, diskImageMsg)
			if err := c.k8sClient.UpdateDeployStatus(ctx, deploy.Name, "WaitingForDiskImage", diskImageMsg); err != nil {
				log.Printf("Controller: failed to set WaitingForDiskImage for %s: %v", deploy.Name, err)
			}
		}
		return
	}

	// If previously in ConfigError or WaitingForDiskImage but now valid, reset to Pending
	if deploy.Status.Phase == "ConfigError" || deploy.Status.Phase == "WaitingForDiskImage" {
		log.Printf("Controller: %s now ready, setting to Pending", deploy.Name)
		if err := c.k8sClient.UpdateDeployStatus(ctx, deploy.Name, "Pending", "Ready for boot"); err != nil {
			log.Printf("Controller: failed to reset %s to Pending: %v", deploy.Name, err)
		}
		return
	}

	// Initialize empty status to Pending
	if deploy.Status.Phase == "" {
		log.Printf("Controller: initializing %s status to Pending", deploy.Name)
		if err := c.k8sClient.UpdateDeployStatus(ctx, deploy.Name, "Pending", "Initialized by controller"); err != nil {
			log.Printf("Controller: failed to set Pending for %s: %v", deploy.Name, err)
		}
		return
	}

	// Timeout InProgress deploys
	if deploy.Status.Phase == "InProgress" && !deploy.Status.LastUpdated.IsZero() {
		age := time.Since(deploy.Status.LastUpdated)
		if age > inProgressTimeout {
			log.Printf("Controller: timing out %s (InProgress for %s)", deploy.Name, age)
			if err := c.k8sClient.UpdateDeployStatus(ctx, deploy.Name, "Failed", "Timed out waiting for completion"); err != nil {
				log.Printf("Controller: failed to set Failed for %s: %v", deploy.Name, err)
			}
		}
	}
}

// checkDiskImageReady checks if the DiskImage for this Deploy is ready
func (c *Controller) checkDiskImageReady(ctx context.Context, deploy *k8s.Deploy) (bool, string) {
	// Get BootTarget to find DiskImage reference
	bootTarget, err := c.k8sClient.GetBootTarget(ctx, deploy.Spec.BootTargetRef)
	if err != nil {
		return false, fmt.Sprintf("BootTarget '%s' not found", deploy.Spec.BootTargetRef)
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

// validateDeployRefs checks that all referenced resources exist
func (c *Controller) validateDeployRefs(ctx context.Context, deploy *k8s.Deploy) error {
	// Validate machineRef
	if _, err := c.k8sClient.GetMachine(ctx, deploy.Spec.MachineRef); err != nil {
		return fmt.Errorf("Machine '%s' not found", deploy.Spec.MachineRef)
	}

	// Validate bootTargetRef (BootTarget)
	if _, err := c.k8sClient.GetBootTarget(ctx, deploy.Spec.BootTargetRef); err != nil {
		return fmt.Errorf("BootTarget '%s' not found", deploy.Spec.BootTargetRef)
	}

	// Validate responseTemplateRef
	if deploy.Spec.ResponseTemplateRef != "" {
		if _, err := c.k8sClient.GetResponseTemplate(ctx, deploy.Spec.ResponseTemplateRef); err != nil {
			return fmt.Errorf("ResponseTemplate '%s' not found", deploy.Spec.ResponseTemplateRef)
		}
	}

	// Validate configMaps
	for _, cm := range deploy.Spec.ConfigMaps {
		if _, err := c.k8sClient.GetConfigMap(ctx, cm); err != nil {
			return fmt.Errorf("ConfigMap '%s' not found", cm)
		}
	}

	// Validate secrets
	for _, secret := range deploy.Spec.Secrets {
		if _, err := c.k8sClient.GetSecret(ctx, secret); err != nil {
			return fmt.Errorf("Secret '%s' not found", secret)
		}
	}

	return nil
}

// RenderTemplate renders a template with merged values from ConfigMaps and Secrets
func (c *Controller) RenderTemplate(ctx context.Context, deploy *k8s.Deploy, templateContent string) (string, error) {
	// Build template data by merging ConfigMaps, then Secrets
	data := make(map[string]interface{})

	// Merge ConfigMaps in order
	for _, cmName := range deploy.Spec.ConfigMaps {
		cm, err := c.k8sClient.GetConfigMap(ctx, cmName)
		if err != nil {
			return "", fmt.Errorf("ConfigMap '%s' not found", cmName)
		}
		for k, v := range cm.Data {
			data[k] = v
		}
	}

	// Merge Secrets in order (override ConfigMaps)
	for _, secretName := range deploy.Spec.Secrets {
		secret, err := c.k8sClient.GetSecret(ctx, secretName)
		if err != nil {
			return "", fmt.Errorf("Secret '%s' not found", secretName)
		}
		for k, v := range secret.Data {
			data[k] = string(v)
		}
	}

	// Add system variables
	data["Host"] = c.host
	data["Port"] = c.port
	data["Hostname"] = deploy.Spec.MachineRef
	data["Target"] = deploy.Spec.BootTargetRef

	// Parse and execute template
	tmpl, err := template.New("response").Option("missingkey=error").Parse(templateContent)
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}

	return buf.String(), nil
}
