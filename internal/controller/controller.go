package controller

import (
	"context"
	"log"
	"time"

	"github.com/isoboot/isoboot/internal/k8s"
)

const (
	reconcileInterval = 10 * time.Second
	inProgressTimeout = 30 * time.Minute
)

// Controller watches Deploy CRDs and manages their lifecycle
type Controller struct {
	k8sClient *k8s.Client
	stopCh    chan struct{}
}

// New creates a new controller
func New(k8sClient *k8s.Client) *Controller {
	return &Controller{
		k8sClient: k8sClient,
		stopCh:    make(chan struct{}),
	}
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
