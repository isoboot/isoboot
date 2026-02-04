/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	isobootv1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
)

// Downloader performs the actual download operation
type Downloader interface {
	Download(ctx context.Context, bootSource *isobootv1alpha1.BootSource) error
}

// DefaultDownloader is a stub downloader that simulates a download
type DefaultDownloader struct{}

// Download simulates a download operation
func (d *DefaultDownloader) Download(ctx context.Context, bootSource *isobootv1alpha1.BootSource) error {
	// Stub: sleep to simulate download
	select {
	case <-time.After(10 * time.Second):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// DownloadManager tracks in-flight downloads
type DownloadManager struct {
	mu        sync.Mutex
	inFlight  map[types.UID]context.CancelFunc
	parentCtx context.Context
	client    client.Client
	downloads Downloader
}

// NewDownloadManager creates a new DownloadManager
func NewDownloadManager(parentCtx context.Context, c client.Client, downloader Downloader) *DownloadManager {
	return &DownloadManager{
		inFlight:  make(map[types.UID]context.CancelFunc),
		parentCtx: parentCtx,
		client:    c,
		downloads: downloader,
	}
}

// IsDownloading returns true if a download is in progress for the given BootSource
func (m *DownloadManager) IsDownloading(uid types.UID) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, exists := m.inFlight[uid]
	return exists
}

// StartDownload starts a download goroutine for the given BootSource
func (m *DownloadManager) StartDownload(bootSource *isobootv1alpha1.BootSource) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Already downloading
	if _, exists := m.inFlight[bootSource.UID]; exists {
		return
	}

	// Derive context from parent (propagates cancellation from controller shutdown)
	ctx, cancel := context.WithCancel(m.parentCtx)
	m.inFlight[bootSource.UID] = cancel

	// Deep copy values needed by goroutine to avoid race conditions
	uid := bootSource.UID
	namespacedName := types.NamespacedName{
		Name:      bootSource.Name,
		Namespace: bootSource.Namespace,
	}
	specCopy := bootSource.Spec.DeepCopy()

	go func() {
		defer func() {
			m.mu.Lock()
			delete(m.inFlight, uid)
			m.mu.Unlock()
		}()

		log := logf.Log.WithName("download").WithValues("bootsource", namespacedName)
		log.Info("Starting download")

		// Create a temporary BootSource with copied spec for the downloader
		downloadSource := &isobootv1alpha1.BootSource{
			Spec: *specCopy,
		}
		downloadSource.Name = namespacedName.Name
		downloadSource.Namespace = namespacedName.Namespace
		downloadSource.UID = uid

		// Perform download
		err := m.downloads.Download(ctx, downloadSource)

		// Update status with retry
		if updateErr := m.updateStatusWithRetry(ctx, namespacedName, err, log); updateErr != nil {
			log.Error(updateErr, "Failed to update BootSource status after retries")
		}
	}()
}

// updateStatusWithRetry updates the BootSource status with retry logic
func (m *DownloadManager) updateStatusWithRetry(ctx context.Context, namespacedName types.NamespacedName, downloadErr error, log logr.Logger) error {
	const maxRetries = 3
	backoff := 100 * time.Millisecond

	for attempt := range maxRetries {
		// Re-fetch the BootSource to get latest version
		current := &isobootv1alpha1.BootSource{}
		if err := m.client.Get(ctx, namespacedName, current); err != nil {
			if errors.IsNotFound(err) {
				log.Info("BootSource deleted, skipping status update")
				return nil
			}
			log.Error(err, "Failed to get BootSource", "attempt", attempt+1)
			time.Sleep(backoff)
			backoff *= 2
			continue
		}

		// Update status based on download result
		if downloadErr != nil {
			log.Error(downloadErr, "Download failed")
			current.Status.Phase = isobootv1alpha1.PhaseFailed
			current.Status.Message = "Download failed: " + downloadErr.Error()
		} else {
			log.Info("Download completed")
			current.Status.Phase = isobootv1alpha1.PhaseVerifying
			current.Status.Message = "Download completed, verifying"
		}

		if err := m.client.Status().Update(ctx, current); err != nil {
			log.Error(err, "Failed to update status", "attempt", attempt+1)
			time.Sleep(backoff)
			backoff *= 2
			continue
		}

		return nil
	}

	return errors.NewServiceUnavailable("failed to update status after retries")
}

// CancelDownload cancels an in-progress download
func (m *DownloadManager) CancelDownload(uid types.UID) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if cancel, exists := m.inFlight[uid]; exists {
		cancel()
		delete(m.inFlight, uid)
	}
}

// BootSourceReconciler reconciles a BootSource object
type BootSourceReconciler struct {
	client.Client
	Scheme          *runtime.Scheme
	DownloadManager *DownloadManager
}

// +kubebuilder:rbac:groups=isoboot.github.io,resources=bootsources,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=isoboot.github.io,resources=bootsources/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=isoboot.github.io,resources=bootsources/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *BootSourceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Fetch the BootSource instance
	bootSource := &isobootv1alpha1.BootSource{}
	if err := r.Get(ctx, req.NamespacedName, bootSource); err != nil {
		if errors.IsNotFound(err) {
			// Object not found, return. Created objects are automatically garbage collected.
			log.Info("BootSource resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get BootSource")
		return ctrl.Result{}, err
	}

	// Set initial phase to Pending if not set
	if bootSource.Status.Phase == "" {
		bootSource.Status.Phase = isobootv1alpha1.PhasePending
		if err := r.Status().Update(ctx, bootSource); err != nil {
			log.Error(err, "Failed to update BootSource status to Pending")
			return ctrl.Result{}, err
		}
		log.Info("Set BootSource phase to Pending", "name", bootSource.Name)
		return ctrl.Result{}, nil
	}

	// Handle phases
	switch bootSource.Status.Phase {
	case isobootv1alpha1.PhasePending:
		return r.handlePending(ctx, bootSource)
	case isobootv1alpha1.PhaseDownloading:
		return r.handleDownloading(ctx, bootSource)
	}

	return ctrl.Result{}, nil
}

// handlePending handles the Pending phase - starts a download if not already in progress
func (r *BootSourceReconciler) handlePending(ctx context.Context, bootSource *isobootv1alpha1.BootSource) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Check if download is already in progress
	if r.DownloadManager.IsDownloading(bootSource.UID) {
		log.Info("Download already in progress")
		return ctrl.Result{}, nil
	}

	// Start download
	r.DownloadManager.StartDownload(bootSource)

	// Update status to Downloading
	bootSource.Status.Phase = isobootv1alpha1.PhaseDownloading
	bootSource.Status.Message = "Download started"
	if err := r.Status().Update(ctx, bootSource); err != nil {
		log.Error(err, "Failed to update BootSource status to Downloading")
		return ctrl.Result{}, err
	}

	log.Info("Started download, transitioning to Downloading phase")
	return ctrl.Result{}, nil
}

// handleDownloading handles the Downloading phase - checks if download is still in progress
func (r *BootSourceReconciler) handleDownloading(ctx context.Context, bootSource *isobootv1alpha1.BootSource) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// If download is still in progress, nothing to do
	if r.DownloadManager.IsDownloading(bootSource.UID) {
		return ctrl.Result{}, nil
	}

	// Download not in progress - this means either:
	// 1. Controller restarted and lost track of the download
	// 2. Download completed but status update failed
	// In either case, go back to Pending to retry
	log.Info("Download not in progress, returning to Pending to retry")
	bootSource.Status.Phase = isobootv1alpha1.PhasePending
	bootSource.Status.Message = "Download interrupted, retrying"
	if err := r.Status().Update(ctx, bootSource); err != nil {
		log.Error(err, "Failed to update BootSource status to Pending")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *BootSourceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&isobootv1alpha1.BootSource{}).
		Named("bootsource").
		Complete(r)
}
