// Package controller implements the BootSource reconciliation logic.
//
// # BootSource State Machine
//
// The BootSource controller manages the lifecycle of boot resources through
// the following phases:
//
//	Pending      - Initial state when BootSource is created
//	Downloading  - Fetching kernel, initrd, ISO, and/or firmware from URLs
//	Verifying    - Validating downloaded files against checksums
//	Extracting   - Extracting kernel/initrd from ISO (ISO mode only)
//	Building     - Combining initrd with firmware (when firmware specified)
//	Ready        - All resources available and verified
//	Corrupted    - Checksum verification failed
//	Failed       - Unrecoverable error occurred
//
// # State Transitions
//
// The following table documents all valid state transitions and their conditions.
//
// NOTE: State transition logic is NOT YET IMPLEMENTED. The Reconcile function
// is currently a stub. Tests for these transitions do not exist yet.
//
//	From         To            Condition
//	──────────── ───────────── ─────────────────────────────────────────────────
//	(new)        Pending       BootSource created
//	Pending      Downloading   Reconciler starts processing
//	Downloading  Verifying     All downloads completed successfully
//	Downloading  Failed        Network error, HTTP error, or timeout
//	Verifying    Extracting    Hash verified, ISO mode, need to extract
//	Verifying    Building      Hash verified, firmware specified, need to combine
//	Verifying    Ready         Hash verified, no extraction or building needed
//	Verifying    Corrupted     Hash mismatch detected
//	Extracting   Building      Extraction complete, firmware specified
//	Extracting   Ready         Extraction complete, no firmware
//	Extracting   Failed        Extraction error (file not found, corrupt ISO)
//	Building     Ready         Initrd + firmware combined successfully
//	Building     Failed        Build error (cpio/gzip failure)
//	Ready        Verifying     Re-verification triggered (e.g., file watcher)
//	Corrupted    Downloading   Re-download triggered (manual or automatic)
//
// # Terminal States
//
//   - Ready: Success state, resources available for PXE/iPXE serving
//   - Failed: Unrecoverable error, requires spec change or manual intervention
//   - Corrupted: Recoverable error, can retry download
//
// # Mode-Specific Flows
//
// Kernel + Initrd mode (no firmware):
//
//	Pending → Downloading → Verifying → Ready
//
// Kernel + Initrd mode (with firmware):
//
//	Pending → Downloading → Verifying → Building → Ready
//
// ISO mode (no firmware):
//
//	Pending → Downloading → Verifying → Extracting → Ready
//
// ISO mode (with firmware):
//
//	Pending → Downloading → Verifying → Extracting → Building → Ready
package controller

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

// +kubebuilder:rbac:groups=isoboot.github.io,resources=bootsources,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=isoboot.github.io,resources=bootsources/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=isoboot.github.io,resources=bootsources/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the BootSource object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.1/pkg/reconcile
func (r *BootSourceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = logf.FromContext(ctx)

	// TODO(user): your logic here

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *BootSourceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&isobootv1alpha1.BootSource{}).
		Named("bootsource").
		Complete(r)
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
