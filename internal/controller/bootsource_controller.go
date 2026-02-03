package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
	"github.com/isoboot/isoboot/internal/isoextract"
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

	// Reconcile all resources and compute overall phase
	phases, messages := r.reconcileAllResources(ctx, &bs, destDir)
	overallPhase := worstPhase(phases)
	bs.Status.Phase = overallPhase
	bs.Status.Message = buildStatusMessage(overallPhase, messages)

	// Update status
	if err := r.Status().Update(ctx, &bs); err != nil {
		return ctrl.Result{}, err
	}

	return resultForPhase(overallPhase), nil
}

// reconcileAllResources reconciles all resources for a BootSource and returns the phases and messages.
func (r *BootSourceReconciler) reconcileAllResources(
	ctx context.Context,
	bs *isobootv1alpha1.BootSource,
	destDir string,
) ([]isobootv1alpha1.BootSourcePhase, []string) {
	var phases []isobootv1alpha1.BootSourcePhase
	var messages []string

	// Track current reconcile results for derived artifacts
	var currentInitrdStatus *isobootv1alpha1.ResourceStatus
	var currentFirmwareStatus *isobootv1alpha1.ResourceStatus

	// Reconcile based on mode (ISO vs direct)
	if bs.Spec.ISO != nil {
		isoPhases, isoMsgs, initrdStatus := r.reconcileISOMode(ctx, bs, destDir)
		phases = append(phases, isoPhases...)
		messages = append(messages, isoMsgs...)
		currentInitrdStatus = initrdStatus
	} else {
		directPhases, directMsgs, initrdStatus := r.reconcileDirectMode(ctx, bs, destDir)
		phases = append(phases, directPhases...)
		messages = append(messages, directMsgs...)
		currentInitrdStatus = initrdStatus
	}

	// Reconcile firmware (optional, applies to both modes)
	if bs.Spec.Firmware != nil {
		phase, status, err := r.reconcileResource(ctx, bs.Spec.Firmware, "firmware", destDir)
		if err != nil {
			messages = append(messages, fmt.Sprintf("firmware: %v", err))
		}
		phases = append(phases, phase)
		if status != nil {
			bs.Status.Resources["firmware"] = *status
			currentFirmwareStatus = status
		}
	} else {
		// Clean up stale firmware and derived artifacts when firmware is removed from spec
		r.cleanupStaleArtifacts(bs, destDir)
	}

	// Build initrdWithFirmware only if firmware is specified AND both current reconciles succeeded
	if bs.Spec.Firmware != nil && currentInitrdStatus != nil && currentFirmwareStatus != nil {
		existingStatus := bs.Status.Resources["initrdWithFirmware"]
		buildPhase, buildStatus, buildErr := r.reconcileInitrdWithFirmware(
			ctx,
			currentInitrdStatus.Path,
			currentInitrdStatus.Shasum,
			currentFirmwareStatus.Path,
			currentFirmwareStatus.Shasum,
			&existingStatus,
			destDir,
		)
		if buildErr != nil {
			messages = append(messages, fmt.Sprintf("initrdWithFirmware: %v", buildErr))
		}
		phases = append(phases, buildPhase)
		if buildStatus != nil {
			bs.Status.Resources["initrdWithFirmware"] = *buildStatus
		}
	}

	return phases, messages
}

// reconcileISOMode handles ISO mode reconciliation.
// Returns phases, messages, and the initrd status (for use by derived artifacts).
func (r *BootSourceReconciler) reconcileISOMode(
	ctx context.Context,
	bs *isobootv1alpha1.BootSource,
	destDir string,
) ([]isobootv1alpha1.BootSourcePhase, []string, *isobootv1alpha1.ResourceStatus) {
	var phases []isobootv1alpha1.BootSourcePhase
	var messages []string
	var resultInitrdStatus *isobootv1alpha1.ResourceStatus

	// Download and verify ISO
	isoPhase, isoStatus, isoErr := r.reconcileResource(ctx, &bs.Spec.ISO.DownloadableResource, "iso", destDir)
	if isoErr != nil {
		messages = append(messages, fmt.Sprintf("iso: %v", isoErr))
	}
	phases = append(phases, isoPhase)
	if isoStatus != nil {
		bs.Status.Resources["iso"] = *isoStatus
	}

	// Extract kernel/initrd from ISO (only if ISO is Ready)
	if isoPhase == isobootv1alpha1.BootSourcePhaseReady && isoStatus != nil {
		kernelStatus, initrdStatus, extractErr := r.extractFromISO(
			ctx,
			isoStatus.Path,
			isoStatus.Shasum,
			bs.Spec.ISO.KernelPath,
			bs.Spec.ISO.InitrdPath,
			destDir,
		)
		if extractErr != nil {
			messages = append(messages, fmt.Sprintf("extraction: %v", extractErr))
			phases = append(phases, isobootv1alpha1.BootSourcePhaseFailed)
		} else {
			if kernelStatus != nil {
				bs.Status.Resources["kernel"] = *kernelStatus
			}
			if initrdStatus != nil {
				bs.Status.Resources["initrd"] = *initrdStatus
				resultInitrdStatus = initrdStatus
			}
		}
	}

	return phases, messages, resultInitrdStatus
}

// reconcileDirectMode handles direct mode (kernel+initrd) reconciliation.
// Returns phases, messages, and the initrd status (for use by derived artifacts).
func (r *BootSourceReconciler) reconcileDirectMode(
	ctx context.Context,
	bs *isobootv1alpha1.BootSource,
	destDir string,
) ([]isobootv1alpha1.BootSourcePhase, []string, *isobootv1alpha1.ResourceStatus) {
	var phases []isobootv1alpha1.BootSourcePhase
	var messages []string
	var resultInitrdStatus *isobootv1alpha1.ResourceStatus

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

	if bs.Spec.Initrd != nil {
		phase, status, err := r.reconcileResource(ctx, bs.Spec.Initrd, "initrd", destDir)
		if err != nil {
			messages = append(messages, fmt.Sprintf("initrd: %v", err))
		}
		phases = append(phases, phase)
		if status != nil {
			bs.Status.Resources["initrd"] = *status
			resultInitrdStatus = status
		}
	}

	return phases, messages, resultInitrdStatus
}

// buildStatusMessage creates a human-readable status message for the given phase and messages.
func buildStatusMessage(phase isobootv1alpha1.BootSourcePhase, messages []string) string {
	switch phase {
	case isobootv1alpha1.BootSourcePhaseReady:
		return "All resources ready"
	case isobootv1alpha1.BootSourcePhaseFailed, isobootv1alpha1.BootSourcePhaseCorrupted:
		switch len(messages) {
		case 0:
			return "Unknown error"
		case 1:
			return messages[0]
		default:
			var sb strings.Builder
			sb.WriteString(messages[0])
			for _, m := range messages[1:] {
				sb.WriteString("; ")
				sb.WriteString(m)
			}
			return sb.String()
		}
	default:
		return fmt.Sprintf("Resources in %s phase", phase)
	}
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
	if info, statErr := os.Stat(destPath); statErr == nil {
		if verifyErr := r.verifyResource(destPath, expectedHash); verifyErr == nil {
			// File exists and hash matches - Ready
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

	// Success - get file size for status
	info, statErr := os.Stat(destPath)
	if statErr != nil {
		log.Error(statErr, "Failed to stat downloaded file", "resource", name)
		return isobootv1alpha1.BootSourcePhaseFailed, nil, statErr
	}
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

// extractFromISO extracts kernel and initrd from an ISO image.
// Re-extracts if the ISO hash changes, requested paths change, or extracted files are missing.
func (r *BootSourceReconciler) extractFromISO(
	ctx context.Context,
	isoPath, isoShasum, kernelPath, initrdPath, destDir string,
) (kernelStatus, initrdStatus *isobootv1alpha1.ResourceStatus, err error) {
	log := logf.FromContext(ctx)

	kernelDest := filepath.Join(destDir, "kernel")
	initrdDest := filepath.Join(destDir, "initrd")
	markerFile := filepath.Join(destDir, ".iso-extracted")

	// Check if extraction is needed by comparing ISO hash and paths with marker
	// Marker format: "isoShasum:kernelPath:initrdPath"
	expectedMarker := isoShasum + ":" + kernelPath + ":" + initrdPath
	needExtract := true
	if markerData, err := os.ReadFile(markerFile); err == nil {
		if string(markerData) == expectedMarker {
			// ISO and paths haven't changed, check if extracted files exist
			kernelOK := false
			initrdOK := false
			if info, statErr := os.Stat(kernelDest); statErr == nil && info.Size() > 0 {
				kernelOK = true
			}
			if info, statErr := os.Stat(initrdDest); statErr == nil && info.Size() > 0 {
				initrdOK = true
			}
			needExtract = !kernelOK || !initrdOK
		}
	}

	// Extract if needed
	if needExtract {
		log.Info("Extracting files from ISO", "kernelPath", kernelPath, "initrdPath", initrdPath)

		// Extract to temp directory then move to canonical names
		extractDir := filepath.Join(destDir, "extract-tmp")
		if err := os.MkdirAll(extractDir, 0o755); err != nil {
			return nil, nil, fmt.Errorf("creating extract directory: %w", err)
		}
		defer os.RemoveAll(extractDir) //nolint:errcheck // cleanup

		if err := isoextract.Extract(isoPath, []string{kernelPath, initrdPath}, extractDir); err != nil {
			return nil, nil, fmt.Errorf("extracting from ISO: %w", err)
		}

		// Normalize paths: trim leading "/" to get relative paths within extractDir
		kernelRel := strings.TrimPrefix(kernelPath, "/")
		initrdRel := strings.TrimPrefix(initrdPath, "/")

		// Move extracted files to canonical names
		extractedKernel := filepath.Join(extractDir, filepath.FromSlash(kernelRel))
		extractedInitrd := filepath.Join(extractDir, filepath.FromSlash(initrdRel))

		if err := os.Rename(extractedKernel, kernelDest); err != nil {
			return nil, nil, fmt.Errorf("moving kernel to destination: %w", err)
		}
		if err := os.Rename(extractedInitrd, initrdDest); err != nil {
			return nil, nil, fmt.Errorf("moving initrd to destination: %w", err)
		}

		// Write marker file with ISO hash and paths
		if err := os.WriteFile(markerFile, []byte(expectedMarker), 0o644); err != nil {
			return nil, nil, fmt.Errorf("writing extraction marker: %w", err)
		}
	}

	// Compute hashes for status
	kernelHash, err := checksum.ComputeFileHash(kernelDest)
	if err != nil {
		return nil, nil, fmt.Errorf("computing kernel hash: %w", err)
	}
	initrdHash, err := checksum.ComputeFileHash(initrdDest)
	if err != nil {
		return nil, nil, fmt.Errorf("computing initrd hash: %w", err)
	}

	// Get file sizes (handle potential errors)
	kernelInfo, err := os.Stat(kernelDest)
	if err != nil {
		return nil, nil, fmt.Errorf("stat kernel: %w", err)
	}
	initrdInfo, err := os.Stat(initrdDest)
	if err != nil {
		return nil, nil, fmt.Errorf("stat initrd: %w", err)
	}

	kernelStatus = &isobootv1alpha1.ResourceStatus{
		Shasum: kernelHash,
		Size:   kernelInfo.Size(),
		Path:   kernelDest,
	}
	initrdStatus = &isobootv1alpha1.ResourceStatus{
		Shasum: initrdHash,
		Size:   initrdInfo.Size(),
		Path:   initrdDest,
	}

	return kernelStatus, initrdStatus, nil
}

// buildInitrdWithFirmware concatenates initrd and firmware into a single file.
// Uses atomic write (temp file + rename) to avoid partial files.
func (r *BootSourceReconciler) buildInitrdWithFirmware(
	initrdPath, firmwarePath, destPath string,
) (*isobootv1alpha1.ResourceStatus, error) {
	// Open source files
	initrdFile, err := os.Open(initrdPath)
	if err != nil {
		return nil, fmt.Errorf("opening initrd: %w", err)
	}
	defer initrdFile.Close() //nolint:errcheck

	firmwareFile, err := os.Open(firmwarePath)
	if err != nil {
		return nil, fmt.Errorf("opening firmware: %w", err)
	}
	defer firmwareFile.Close() //nolint:errcheck

	// Create temp file for atomic write
	tmpFile, err := os.CreateTemp(filepath.Dir(destPath), "initrdWithFirmware-*.tmp")
	if err != nil {
		return nil, fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	success := false
	defer func() {
		if !success {
			_ = tmpFile.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	// Write initrd then firmware, computing hash as we go
	h := sha256.New()
	w := io.MultiWriter(tmpFile, h)

	if _, err := io.Copy(w, initrdFile); err != nil {
		return nil, fmt.Errorf("copying initrd: %w", err)
	}
	if _, err := io.Copy(w, firmwareFile); err != nil {
		return nil, fmt.Errorf("copying firmware: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return nil, fmt.Errorf("closing temp file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, destPath); err != nil {
		return nil, fmt.Errorf("renaming temp file: %w", err)
	}
	success = true

	// Get file info for status
	info, err := os.Stat(destPath)
	if err != nil {
		return nil, fmt.Errorf("stat destination: %w", err)
	}

	return &isobootv1alpha1.ResourceStatus{
		Shasum: hex.EncodeToString(h.Sum(nil)),
		Size:   info.Size(),
		Path:   destPath,
	}, nil
}

// reconcileInitrdWithFirmware ensures initrdWithFirmware is built and valid.
// It tracks source hashes via a marker file to detect when inputs change.
// Returns Ready when successfully reconciled, or Failed on error.
func (r *BootSourceReconciler) reconcileInitrdWithFirmware(
	ctx context.Context,
	initrdPath, initrdShasum string,
	firmwarePath, firmwareShasum string,
	existingStatus *isobootv1alpha1.ResourceStatus,
	destDir string,
) (isobootv1alpha1.BootSourcePhase, *isobootv1alpha1.ResourceStatus, error) {
	log := logf.FromContext(ctx)
	destPath := filepath.Join(destDir, "initrdWithFirmware")
	markerFile := filepath.Join(destDir, ".initrdWithFirmware-sources")

	// Expected source marker is the combination of initrd and firmware hashes
	expectedMarker := initrdShasum + "+" + firmwareShasum

	// Check if existing file is valid and built from current sources
	if existingStatus != nil && existingStatus.Path != "" {
		// Check marker file for source hashes
		if markerData, err := os.ReadFile(markerFile); err == nil && string(markerData) == expectedMarker {
			if info, err := os.Stat(existingStatus.Path); err == nil && info.Size() > 0 {
				// Verify hash matches
				actualHash, err := checksum.ComputeFileHash(existingStatus.Path)
				if err == nil && actualHash == existingStatus.Shasum {
					// Existing file is valid and from current sources
					return isobootv1alpha1.BootSourcePhaseReady, existingStatus, nil
				}
				log.Info("Existing initrdWithFirmware is corrupted, rebuilding")
			}
		} else {
			log.Info("Source inputs changed, rebuilding initrdWithFirmware")
		}
	}

	// Build initrdWithFirmware
	log.Info("Building initrdWithFirmware", "initrd", initrdPath, "firmware", firmwarePath)
	status, err := r.buildInitrdWithFirmware(initrdPath, firmwarePath, destPath)
	if err != nil {
		return isobootv1alpha1.BootSourcePhaseFailed, nil, err
	}

	// Write marker file with source hashes
	if err := os.WriteFile(markerFile, []byte(expectedMarker), 0o644); err != nil {
		return isobootv1alpha1.BootSourcePhaseFailed, nil, fmt.Errorf("writing source marker: %w", err)
	}

	return isobootv1alpha1.BootSourcePhaseReady, status, nil
}

// cleanupStaleArtifacts removes firmware and initrdWithFirmware status entries
// and files when firmware is no longer specified in the spec.
func (r *BootSourceReconciler) cleanupStaleArtifacts(bs *isobootv1alpha1.BootSource, destDir string) {
	// Remove status entries
	delete(bs.Status.Resources, "firmware")
	delete(bs.Status.Resources, "initrdWithFirmware")

	// Remove files (best effort, ignore errors)
	_ = os.Remove(filepath.Join(destDir, "firmware"))
	_ = os.Remove(filepath.Join(destDir, "initrdWithFirmware"))
	_ = os.Remove(filepath.Join(destDir, ".initrdWithFirmware-sources"))
}
