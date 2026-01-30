package controller

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"sync"
	"time"

	"github.com/isoboot/isoboot/internal/k8s"
	"github.com/isoboot/isoboot/internal/k8s/typed"
)

// validMachineId validates systemd machine-id format (exactly 32 lowercase hex characters)
var validMachineId = regexp.MustCompile(`^[0-9a-f]{32}$`)

const (
	reconcileInterval = 10 * time.Second
	inProgressTimeout = 30 * time.Minute
)

// Controller watches Provision CRDs and manages their lifecycle
type Controller struct {
	k8sClient                KubernetesClient
	typedK8s                 *typed.Client
	httpClient               HTTPDoer
	stopCh                   chan struct{}
	isoBasePath              string
	filesBasePath            string
	activeDiskImageDownloads sync.Map // tracks in-progress DiskImage downloads by name
	activeBootMediaDownloads sync.Map // tracks in-progress BootMedia downloads by name
}

// New creates a new controller
func New(k8sClient KubernetesClient) *Controller {
	return &Controller{
		k8sClient:  k8sClient,
		httpClient: http.DefaultClient,
		stopCh:     make(chan struct{}),
	}
}

// SetISOBasePath sets the base path for ISO storage
func (c *Controller) SetISOBasePath(path string) {
	c.isoBasePath = path
}

// SetTypedK8s sets the typed k8s client
func (c *Controller) SetTypedK8s(client *typed.Client) {
	c.typedK8s = client
}

// SetFilesBasePath sets the base path for file storage
func (c *Controller) SetFilesBasePath(path string) {
	c.filesBasePath = path
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

	// Reconcile BootMedias first (downloads)
	c.reconcileBootMedias(ctx)

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

	// Check if BootTarget is ready
	bootTargetReady, bootTargetMsg := c.checkBootTargetReady(ctx, provision)
	if !bootTargetReady {
		if provision.Status.Phase != "WaitingForBootMedia" || provision.Status.Message != bootTargetMsg {
			log.Printf("Controller: %s waiting for BootTarget: %s", provision.Name, bootTargetMsg)
			if err := c.k8sClient.UpdateProvisionStatus(ctx, provision.Name, "WaitingForBootMedia", bootTargetMsg, ""); err != nil {
				log.Printf("Controller: failed to set WaitingForBootMedia for %s: %v", provision.Name, err)
			}
		}
		return
	}

	// If previously in ConfigError or WaitingForBootMedia but now valid, reset to Pending
	if provision.Status.Phase == "ConfigError" || provision.Status.Phase == "WaitingForBootMedia" {
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

// checkBootTargetReady checks if the BootMedia for this Provision's BootTarget is ready
func (c *Controller) checkBootTargetReady(ctx context.Context, provision *k8s.Provision) (bool, string) {
	var bootTarget typed.BootTarget
	if err := c.typedK8s.Get(ctx, c.typedK8s.Key(provision.Spec.BootTargetRef), &bootTarget); err != nil {
		return false, fmt.Sprintf("BootTarget '%s' not found", provision.Spec.BootTargetRef)
	}

	var bootMedia typed.BootMedia
	if err := c.typedK8s.Get(ctx, c.typedK8s.Key(bootTarget.Spec.BootMediaRef), &bootMedia); err != nil {
		return false, fmt.Sprintf("BootMedia '%s' not found (referenced by BootTarget '%s')", bootTarget.Spec.BootMediaRef, bootTarget.Name)
	}

	return checkBootMediaStatus(&bootMedia)
}

// checkBootMediaStatus checks the status of a BootMedia and returns whether it's ready
func checkBootMediaStatus(bm *typed.BootMedia) (bool, string) {
	switch bm.Status.Phase {
	case "Complete":
		return true, ""
	case "Failed":
		return false, fmt.Sprintf("BootMedia '%s' failed: %s", bm.Name, bm.Status.Message)
	case "Downloading":
		return false, fmt.Sprintf("BootMedia '%s' downloading", bm.Name)
	default:
		return false, fmt.Sprintf("BootMedia '%s' pending", bm.Name)
	}
}

// validateProvisionRefs checks that all referenced resources exist and have valid configuration
func (c *Controller) validateProvisionRefs(ctx context.Context, provision *k8s.Provision) error {
	// Validate machineRef exists
	if _, err := c.k8sClient.GetMachine(ctx, provision.Spec.MachineRef); err != nil {
		return fmt.Errorf("Machine '%s' not found", provision.Spec.MachineRef)
	}

	// Validate machineId format if present
	if provision.Spec.MachineId != "" && !validMachineId.MatchString(provision.Spec.MachineId) {
		return fmt.Errorf("Provision '%s' has invalid machineId: must be exactly 32 lowercase hex characters", provision.Name)
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
