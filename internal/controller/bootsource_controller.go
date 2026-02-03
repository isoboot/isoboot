package controller

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	isobootv1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
	"github.com/isoboot/isoboot/internal/checksum"
	"github.com/isoboot/isoboot/internal/downloader"
)

// ResourceFetcher abstracts HTTP fetching and downloading operations.
type ResourceFetcher interface {
	// FetchContent fetches a URL and returns its body as bytes.
	FetchContent(ctx context.Context, url string) ([]byte, error)
	// Download fetches a URL and writes its content to destPath.
	Download(ctx context.Context, url, destPath string) error
}

// HTTPResourceFetcher implements ResourceFetcher using the downloader package.
type HTTPResourceFetcher struct {
	Client *http.Client
}

// FetchContent fetches a URL and returns its body as bytes.
func (f *HTTPResourceFetcher) FetchContent(ctx context.Context, url string) ([]byte, error) {
	return downloader.FetchContent(ctx, f.Client, url)
}

// Download fetches a URL and writes its content to destPath.
func (f *HTTPResourceFetcher) Download(ctx context.Context, url, destPath string) error {
	return downloader.Download(ctx, f.Client, url, destPath)
}

// BootSourceReconciler reconciles a BootSource object
type BootSourceReconciler struct {
	client.Client
	Scheme  *runtime.Scheme
	BaseDir string          // Base directory for storing downloaded resources
	Fetcher ResourceFetcher // Fetcher for downloads (uses default HTTP fetcher if nil)
}

const (
	// finalizerName is the finalizer used to ensure cleanup on deletion.
	finalizerName = "isoboot.github.io/cleanup"

	// requeueImmediate is the requeue interval for immediate reprocessing.
	requeueImmediate = 1 * time.Millisecond

	// requeueInProgress is the requeue interval for in-progress operations.
	requeueInProgress = 30 * time.Second

	// requeueError is the requeue interval for failed/corrupted states.
	requeueError = 5 * time.Minute
)

// +kubebuilder:rbac:groups=isoboot.github.io,resources=bootsources,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=isoboot.github.io,resources=bootsources/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=isoboot.github.io,resources=bootsources/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *BootSourceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Fetch the BootSource instance
	var bs isobootv1alpha1.BootSource
	if err := r.Get(ctx, req.NamespacedName, &bs); err != nil {
		if k8serrors.IsNotFound(err) {
			// Object not found, likely deleted
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Handle deletion
	if !bs.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&bs, finalizerName) {
			if err := r.handleDeletion(ctx, &bs); err != nil {
				log.Error(err, "Failed to clean up resources")
				return ctrl.Result{}, err
			}
			controllerutil.RemoveFinalizer(&bs, finalizerName)
			if err := r.Update(ctx, &bs); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(&bs, finalizerName) {
		controllerutil.AddFinalizer(&bs, finalizerName)
		if err := r.Update(ctx, &bs); err != nil {
			return ctrl.Result{}, err
		}
		// Requeue to continue reconciliation after adding finalizer
		return ctrl.Result{RequeueAfter: requeueImmediate}, nil
	}

	// Skip ISO mode (not yet implemented)
	if bs.Spec.ISO != nil {
		log.Info("ISO mode not yet implemented, skipping reconciliation")
		bs.Status.Phase = isobootv1alpha1.BootSourcePhasePending
		bs.Status.Message = "ISO mode not yet implemented"
		if err := r.Status().Update(ctx, &bs); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Ensure directory exists
	destDir, err := r.ensureDirectory(bs.Namespace, bs.Name)
	if err != nil {
		log.Error(err, "Failed to create resource directory")
		bs.Status.Phase = isobootv1alpha1.BootSourcePhaseFailed
		bs.Status.Message = fmt.Sprintf("Failed to create directory: %v", err)
		if statusErr := r.Status().Update(ctx, &bs); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return resultForPhase(isobootv1alpha1.BootSourcePhaseFailed), nil
	}

	// Initialize status.Resources if nil
	if bs.Status.Resources == nil {
		bs.Status.Resources = make(map[string]isobootv1alpha1.ResourceStatus)
	}

	// Track phases for overall status
	var phases []isobootv1alpha1.BootSourcePhase
	var messages []string

	// Reconcile kernel
	if bs.Spec.Kernel != nil {
		phase, status, err := r.reconcileResource(ctx, bs.Spec.Kernel, "kernel", destDir)
		if err != nil {
			messages = append(messages, fmt.Sprintf("kernel: %v", err))
		}
		phases = append(phases, phase)
		if status != nil {
			bs.Status.Resources["kernel"] = *status
		}
	}

	// Reconcile initrd
	if bs.Spec.Initrd != nil {
		phase, status, err := r.reconcileResource(ctx, bs.Spec.Initrd, "initrd", destDir)
		if err != nil {
			messages = append(messages, fmt.Sprintf("initrd: %v", err))
		}
		phases = append(phases, phase)
		if status != nil {
			bs.Status.Resources["initrd"] = *status
		}
	}

	// Reconcile firmware (optional)
	if bs.Spec.Firmware != nil {
		phase, status, err := r.reconcileResource(ctx, bs.Spec.Firmware, "firmware", destDir)
		if err != nil {
			messages = append(messages, fmt.Sprintf("firmware: %v", err))
		}
		phases = append(phases, phase)
		if status != nil {
			bs.Status.Resources["firmware"] = *status
		}
	}

	// Compute overall phase
	overallPhase := worstPhase(phases)
	bs.Status.Phase = overallPhase

	// Set message
	switch overallPhase {
	case isobootv1alpha1.BootSourcePhaseReady:
		bs.Status.Message = "All resources ready"
	case isobootv1alpha1.BootSourcePhaseFailed, isobootv1alpha1.BootSourcePhaseCorrupted:
		if len(messages) > 0 {
			bs.Status.Message = messages[0]
		}
	default:
		bs.Status.Message = fmt.Sprintf("Resources in %s phase", overallPhase)
	}

	// Update status
	if err := r.Status().Update(ctx, &bs); err != nil {
		return ctrl.Result{}, err
	}

	return resultForPhase(overallPhase), nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *BootSourceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&isobootv1alpha1.BootSource{}).
		Named("bootsource").
		Complete(r)
}

// resultForPhase returns the appropriate ctrl.Result for a given phase.
func resultForPhase(phase isobootv1alpha1.BootSourcePhase) ctrl.Result {
	switch phase {
	case isobootv1alpha1.BootSourcePhaseReady:
		return ctrl.Result{}
	case isobootv1alpha1.BootSourcePhaseFailed, isobootv1alpha1.BootSourcePhaseCorrupted:
		return ctrl.Result{RequeueAfter: requeueError}
	default:
		// Downloading, Building, Extracting, Verifying, Pending
		return ctrl.Result{RequeueAfter: requeueInProgress}
	}
}

// handleDeletion cleans up resources when a BootSource is deleted.
func (r *BootSourceReconciler) handleDeletion(_ context.Context, bs *isobootv1alpha1.BootSource) error {
	if r.BaseDir == "" {
		return nil // Nothing to clean up if BaseDir is not configured
	}
	dir := filepath.Join(r.BaseDir, bs.Namespace, bs.Name)
	if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing directory %s: %w", dir, err)
	}
	return nil
}

// reconcileResource handles the download and verification of a single resource.
// It returns the phase, resource status, and any error encountered.
func (r *BootSourceReconciler) reconcileResource(
	ctx context.Context,
	dr *isobootv1alpha1.DownloadableResource,
	name, destDir string,
) (isobootv1alpha1.BootSourcePhase, *isobootv1alpha1.ResourceStatus, error) {
	log := logf.FromContext(ctx)

	// Determine destination path
	destPath := filepath.Join(destDir, name)

	// Resolve expected hash
	expectedHash, err := r.resolveExpectedHash(ctx, dr)
	if err != nil {
		log.Error(err, "Failed to resolve expected hash", "resource", name)
		return isobootv1alpha1.BootSourcePhaseFailed, nil, err
	}

	// Check if file already exists with correct hash
	if _, statErr := os.Stat(destPath); statErr == nil {
		if verifyErr := r.verifyResource(destPath, expectedHash); verifyErr == nil {
			// File exists and hash matches - Ready
			info, _ := os.Stat(destPath)
			status := &isobootv1alpha1.ResourceStatus{
				URL:    dr.URL,
				Shasum: expectedHash,
				Size:   info.Size(),
				Path:   destPath,
			}
			return isobootv1alpha1.BootSourcePhaseReady, status, nil
		}
		// File exists but hash mismatch - need to re-download
		log.Info("Existing file has incorrect hash, re-downloading", "resource", name)
	}

	// Download the resource
	log.Info("Downloading resource", "resource", name, "url", dr.URL)
	if err := r.downloadResource(ctx, dr.URL, destPath); err != nil {
		log.Error(err, "Failed to download resource", "resource", name)
		return isobootv1alpha1.BootSourcePhaseFailed, nil, err
	}

	// Verify the downloaded file
	if err := r.verifyResource(destPath, expectedHash); err != nil {
		log.Error(err, "Hash verification failed", "resource", name)
		// Remove the corrupted file
		_ = os.Remove(destPath)
		return isobootv1alpha1.BootSourcePhaseCorrupted, nil, err
	}

	// Success
	info, _ := os.Stat(destPath)
	status := &isobootv1alpha1.ResourceStatus{
		URL:    dr.URL,
		Shasum: expectedHash,
		Size:   info.Size(),
		Path:   destPath,
	}
	return isobootv1alpha1.BootSourcePhaseReady, status, nil
}

// fetcher returns the ResourceFetcher to use, defaulting to HTTPResourceFetcher if nil.
func (r *BootSourceReconciler) fetcher() ResourceFetcher {
	if r.Fetcher != nil {
		return r.Fetcher
	}
	return &HTTPResourceFetcher{Client: http.DefaultClient}
}

// resolveExpectedHash returns the expected hash for a DownloadableResource.
// If Shasum is set, it returns that directly. If ShasumURL is set, it fetches
// the checksum file and parses it to find the hash for the resource URL.
func (r *BootSourceReconciler) resolveExpectedHash(ctx context.Context, dr *isobootv1alpha1.DownloadableResource) (string, error) {
	if dr.Shasum != nil && *dr.Shasum != "" {
		return *dr.Shasum, nil
	}

	if dr.ShasumURL != nil && *dr.ShasumURL != "" {
		content, err := r.fetcher().FetchContent(ctx, *dr.ShasumURL)
		if err != nil {
			return "", fmt.Errorf("fetching shasum file: %w", err)
		}
		hash, err := checksum.ParseShasumFile(string(content), dr.URL, *dr.ShasumURL)
		if err != nil {
			return "", fmt.Errorf("parsing shasum file: %w", err)
		}
		return hash, nil
	}

	return "", fmt.Errorf("no checksum source specified")
}

// downloadResource downloads a file from the given URL to the destination path.
func (r *BootSourceReconciler) downloadResource(ctx context.Context, url, destPath string) error {
	if err := r.fetcher().Download(ctx, url, destPath); err != nil {
		return fmt.Errorf("downloading %s: %w", url, err)
	}
	return nil
}

// verifyResource verifies that the file at path matches the expected hash.
func (r *BootSourceReconciler) verifyResource(path, expectedHash string) error {
	if err := checksum.VerifyFile(path, expectedHash); err != nil {
		return fmt.Errorf("verifying %s: %w", path, err)
	}
	return nil
}

// ensureDirectory creates the directory structure for storing resources
// for a given BootSource, returning the path. Returns an error if BaseDir is not set.
func (r *BootSourceReconciler) ensureDirectory(namespace, name string) (string, error) {
	if r.BaseDir == "" {
		return "", fmt.Errorf("BaseDir is not configured")
	}
	dir := filepath.Join(r.BaseDir, namespace, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating directory %s: %w", dir, err)
	}
	return dir, nil
}

// phasePriority defines the priority ordering of phases (higher = worse).
var phasePriority = map[isobootv1alpha1.BootSourcePhase]int{
	isobootv1alpha1.BootSourcePhaseReady:       0,
	isobootv1alpha1.BootSourcePhasePending:     1,
	isobootv1alpha1.BootSourcePhaseVerifying:   2,
	isobootv1alpha1.BootSourcePhaseBuilding:    3,
	isobootv1alpha1.BootSourcePhaseExtracting:  4,
	isobootv1alpha1.BootSourcePhaseDownloading: 5,
	isobootv1alpha1.BootSourcePhaseCorrupted:   6,
	isobootv1alpha1.BootSourcePhaseFailed:      7,
}

// worstPhase returns the phase with the highest priority (worst) from the input list.
// Priority order (worst to best): Failed > Corrupted > Downloading > Extracting > Building > Verifying > Pending > Ready.
// Returns Pending for empty input. Unknown phases are treated as Failed.
func worstPhase(phases []isobootv1alpha1.BootSourcePhase) isobootv1alpha1.BootSourcePhase {
	if len(phases) == 0 {
		return isobootv1alpha1.BootSourcePhasePending
	}

	worst := phases[0]
	worstPrio, known := phasePriority[worst]
	if !known {
		return isobootv1alpha1.BootSourcePhaseFailed
	}

	for _, p := range phases[1:] {
		prio, known := phasePriority[p]
		if !known {
			return isobootv1alpha1.BootSourcePhaseFailed
		}
		if prio > worstPrio {
			worst = p
			worstPrio = prio
		}
	}

	return worst
}
