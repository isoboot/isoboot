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
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	diskfs "github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/filesystem"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	isobootgithubiov1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
	"github.com/isoboot/isoboot/internal/urlutil"
)

// BootConfigReconciler reconciles a BootConfig object
type BootConfigReconciler struct {
	client.Client
	Scheme  *runtime.Scheme
	DataDir string
}

// +kubebuilder:rbac:groups=isoboot.github.io,resources=bootconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups=isoboot.github.io,resources=bootconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=isoboot.github.io,resources=bootartifacts,verbs=get;list;watch

func (r *BootConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var bc isobootgithubiov1alpha1.BootConfig
	if err := r.Get(ctx, req.NamespacedName, &bc); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return ctrl.Result{}, err
		}
		// Resource deleted — clean up boot directory
		bootDir := filepath.Join(r.DataDir, "boot", req.Name)
		if err := os.RemoveAll(bootDir); err != nil {
			log.Error(err, "Failed to clean up boot directory", "path", bootDir)
		}
		return ctrl.Result{}, nil
	}

	// Mode B: extract kernel and initrd from an ISO artifact.
	if bc.Spec.ISO != nil {
		return r.reconcileISO(ctx, &bc)
	}

	// Mode A: direct kernel and initrd refs.
	if bc.Spec.Netboot == nil {
		log.V(1).Info("Skipping BootConfig without netboot or iso")
		return ctrl.Result{}, nil
	}
	nb := bc.Spec.Netboot

	// Look up referenced BootArtifacts
	kernelArtifact, err := r.getArtifact(ctx, nb.KernelRef, bc.Namespace)
	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			return ctrl.Result{}, err
		}
		return r.setError(ctx, &bc, fmt.Sprintf("kernel artifact %q not found", nb.KernelRef))
	}

	initrdArtifact, err := r.getArtifact(ctx, nb.InitrdRef, bc.Namespace)
	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			return ctrl.Result{}, err
		}
		return r.setError(ctx, &bc, fmt.Sprintf("initrd artifact %q not found", nb.InitrdRef))
	}

	// Check if all artifacts are Ready
	if kernelArtifact.Status.Phase != isobootgithubiov1alpha1.BootArtifactPhaseReady {
		return r.setPending(ctx, &bc, fmt.Sprintf("waiting for kernel artifact %q to be Ready", nb.KernelRef))
	}
	if initrdArtifact.Status.Phase != isobootgithubiov1alpha1.BootArtifactPhaseReady {
		return r.setPending(ctx, &bc, fmt.Sprintf("waiting for initrd artifact %q to be Ready", nb.InitrdRef))
	}

	// Optionally look up firmware artifact
	var firmwareArtifact *isobootgithubiov1alpha1.BootArtifact
	if nb.FirmwareRef != "" {
		firmwareArtifact, err = r.getArtifact(ctx, nb.FirmwareRef, bc.Namespace)
		if err != nil {
			if client.IgnoreNotFound(err) != nil {
				return ctrl.Result{}, err
			}
			return r.setError(ctx, &bc, fmt.Sprintf("firmware artifact %q not found", nb.FirmwareRef))
		}
		if firmwareArtifact.Status.Phase != isobootgithubiov1alpha1.BootArtifactPhaseReady {
			return r.setPending(ctx, &bc, fmt.Sprintf("waiting for firmware artifact %q to be Ready", nb.FirmwareRef))
		}
	}

	// Assemble boot directory with symlinks
	bootDir := filepath.Join(r.DataDir, "boot", bc.Name)

	kernelFilename := urlutil.FilenameFromURL(kernelArtifact.Spec.URL)
	initrdFilename := urlutil.FilenameFromURL(initrdArtifact.Spec.URL)

	kernelDir := filepath.Join(bootDir, "kernel")
	initrdDir := filepath.Join(bootDir, "initrd")

	if err := os.MkdirAll(kernelDir, 0o755); err != nil {
		return r.setError(ctx, &bc, fmt.Sprintf("creating kernel dir: %v", err))
	}
	if err := os.MkdirAll(initrdDir, 0o755); err != nil {
		return r.setError(ctx, &bc, fmt.Sprintf("creating initrd dir: %v", err))
	}

	// Create kernel symlink
	kernelTarget := filepath.Join("..", "..", "..", "artifacts", kernelArtifact.Name, kernelFilename)
	if err := ensureSymlink(kernelDir, kernelFilename, kernelTarget); err != nil {
		return r.setError(ctx, &bc, fmt.Sprintf("creating kernel symlink: %v", err))
	}

	if firmwareArtifact != nil {
		// Firmware mode: concatenate initrd + firmware into initrd dir
		firmwareFilename := urlutil.FilenameFromURL(firmwareArtifact.Spec.URL)
		initrdPath := filepath.Join(r.DataDir, "artifacts", initrdArtifact.Name, initrdFilename)
		firmwarePath := filepath.Join(r.DataDir, "artifacts", firmwareArtifact.Name, firmwareFilename)
		combinedPath := filepath.Join(initrdDir, initrdFilename)

		if err := concatenateFiles(combinedPath, initrdPath, firmwarePath); err != nil {
			return r.setError(ctx, &bc, fmt.Sprintf("concatenating initrd + firmware: %v", err))
		}
	} else {
		// No firmware: symlink initrd directly
		initrdTarget := filepath.Join("..", "..", "..", "artifacts", initrdArtifact.Name, initrdFilename)
		if err := ensureSymlink(initrdDir, initrdFilename, initrdTarget); err != nil {
			return r.setError(ctx, &bc, fmt.Sprintf("creating initrd symlink: %v", err))
		}
	}

	if bc.Status.Phase != isobootgithubiov1alpha1.BootConfigPhaseReady {
		log.Info("BootConfig assembled", "name", bc.Name, "bootDir", bootDir)
	}

	return r.setReady(ctx, &bc)
}

// reconcileISO handles Mode B: extract kernel and initrd from an ISO artifact
// into the boot directory as real files.
func (r *BootConfigReconciler) reconcileISO(ctx context.Context, bc *isobootgithubiov1alpha1.BootConfig) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	iso := bc.Spec.ISO

	if !isSafeISOPath(iso.KernelPath) {
		return r.setError(ctx, bc, fmt.Sprintf("invalid kernelPath %q: path traversal not allowed", iso.KernelPath))
	}
	if !isSafeISOPath(iso.InitrdPath) {
		return r.setError(ctx, bc, fmt.Sprintf("invalid initrdPath %q: path traversal not allowed", iso.InitrdPath))
	}

	isoArtifact, err := r.getArtifact(ctx, iso.ArtifactRef, bc.Namespace)
	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			return ctrl.Result{}, err
		}
		return r.setError(ctx, bc, fmt.Sprintf("iso artifact %q not found", iso.ArtifactRef))
	}
	if isoArtifact.Status.Phase != isobootgithubiov1alpha1.BootArtifactPhaseReady {
		return r.setPending(ctx, bc, fmt.Sprintf("waiting for iso artifact %q to be Ready", iso.ArtifactRef))
	}

	isoFilename := urlutil.FilenameFromURL(isoArtifact.Spec.URL)
	isoPath := filepath.Join(r.DataDir, "artifacts", isoArtifact.Name, isoFilename)
	bootDir := filepath.Join(r.DataDir, "boot", bc.Name)

	if err := os.MkdirAll(bootDir, 0o755); err != nil {
		return r.setError(ctx, bc, fmt.Sprintf("creating boot dir: %v", err))
	}

	if err := extractFromISO(isoPath, iso.KernelPath, iso.InitrdPath, bootDir); err != nil {
		return r.setError(ctx, bc, fmt.Sprintf("extracting from iso: %v", err))
	}

	// Serve the ISO itself (as image.iso) so installers can fetch their root
	// filesystem over HTTP. Sibling-safe so it doesn't disturb vmlinuz/initrd.
	isoTarget := filepath.Join("..", "..", "artifacts", isoArtifact.Name, isoFilename)
	if err := ensureFileSymlink(filepath.Join(bootDir, "image.iso"), isoTarget); err != nil {
		return r.setError(ctx, bc, fmt.Sprintf("creating iso symlink: %v", err))
	}

	if bc.Status.Phase != isobootgithubiov1alpha1.BootConfigPhaseReady {
		log.Info("BootConfig assembled from ISO", "name", bc.Name, "bootDir", bootDir)
	}
	return r.setReady(ctx, bc)
}

// ensureFileSymlink idempotently points link at target without disturbing
// sibling entries (unlike ensureSymlink, which manages a whole directory).
func ensureFileSymlink(link, target string) error {
	if existing, err := os.Readlink(link); err == nil && existing == target {
		return nil
	}
	_ = os.Remove(link)
	return os.Symlink(target, link)
}

// maxExtractedFileSize caps an individual file extracted from an ISO, guarding
// against an oversized entry from a malicious ISO.
const maxExtractedFileSize = 1 << 30 // 1 GiB

// isSafeISOPath reports whether p is a non-empty in-ISO path with no ".." segment.
func isSafeISOPath(p string) bool {
	return p != "" && !slices.Contains(strings.Split(p, "/"), "..")
}

// extractFromISO opens the ISO9660 image at isoPath and writes the files at
// kernelPath and initrdPath to outputDir/vmlinuz and outputDir/initrd. It skips
// the work when both outputs already exist and are newer than the ISO.
func extractFromISO(isoPath, kernelPath, initrdPath, outputDir string) error {
	isoInfo, err := os.Stat(isoPath)
	if err != nil {
		return fmt.Errorf("stat iso %q: %w", isoPath, err)
	}

	targets := []struct{ src, dst string }{
		{kernelPath, filepath.Join(outputDir, "vmlinuz")},
		{initrdPath, filepath.Join(outputDir, "initrd")},
	}
	if upToDate(targets[0].dst, isoInfo.ModTime()) && upToDate(targets[1].dst, isoInfo.ModTime()) {
		return nil // already extracted and current
	}

	disk, err := diskfs.Open(isoPath, diskfs.WithOpenMode(diskfs.ReadOnly))
	if err != nil {
		return fmt.Errorf("opening iso %q: %w", isoPath, err)
	}
	defer func() { _ = disk.Close() }()

	fsys, err := disk.GetFilesystem(0)
	if err != nil {
		return fmt.Errorf("reading iso filesystem: %w", err)
	}

	for _, e := range targets {
		if err := extractFile(fsys, e.src, e.dst); err != nil {
			return err
		}
	}
	return nil
}

// upToDate reports whether dst exists and was modified after srcModTime.
func upToDate(dst string, srcModTime time.Time) bool {
	info, err := os.Stat(dst)
	return err == nil && info.ModTime().After(srcModTime)
}

// extractFile copies src from the ISO filesystem to dst on disk atomically.
func extractFile(fsys filesystem.FileSystem, src, dst string) error {
	f, err := fsys.OpenFile("/"+strings.TrimPrefix(src, "/"), os.O_RDONLY)
	if err != nil {
		return fmt.Errorf("opening %q in iso: %w", src, err)
	}
	defer func() { _ = f.Close() }()

	tmp, err := os.CreateTemp(filepath.Dir(dst), ".extract-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()

	n, err := io.Copy(tmp, io.LimitReader(f, maxExtractedFileSize+1))
	if err != nil {
		return fmt.Errorf("copying %q: %w", src, err)
	}
	if n > maxExtractedFileSize {
		return fmt.Errorf("file %q exceeds max extract size %d bytes", src, int64(maxExtractedFileSize))
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o444); err != nil {
		return fmt.Errorf("setting permissions: %w", err)
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		return fmt.Errorf("renaming temp file: %w", err)
	}
	return nil
}

// concatenateFiles writes the concatenation of srcA and srcB to dst atomically.
func concatenateFiles(dst, srcA, srcB string) error {
	// Check if the concatenated file already exists and is newer than both sources
	dstInfo, dstErr := os.Stat(dst)
	if dstErr == nil {
		srcAInfo, err := os.Stat(srcA)
		if err != nil {
			return fmt.Errorf("stat initrd: %w", err)
		}
		srcBInfo, err := os.Stat(srcB)
		if err != nil {
			return fmt.Errorf("stat firmware: %w", err)
		}
		if dstInfo.ModTime().After(srcAInfo.ModTime()) && dstInfo.ModTime().After(srcBInfo.ModTime()) {
			return nil // already up to date
		}
	}

	// Remove any existing entries (stale symlinks or old concatenated files)
	dir := filepath.Dir(dst)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("reading dir %s: %w", dir, err)
	}
	for _, e := range entries {
		_ = os.RemoveAll(filepath.Join(dir, e.Name()))
	}

	tmp, err := os.CreateTemp(dir, ".concat-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath) // clean up on error
	}()

	for _, src := range []string{srcA, srcB} {
		f, err := os.Open(src)
		if err != nil {
			return fmt.Errorf("opening %s: %w", src, err)
		}
		_, err = io.Copy(tmp, f)
		_ = f.Close()
		if err != nil {
			return fmt.Errorf("copying %s: %w", src, err)
		}
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}

	if err := os.Chmod(tmpPath, 0o444); err != nil {
		return fmt.Errorf("setting permissions: %w", err)
	}

	if err := os.Rename(tmpPath, dst); err != nil {
		return fmt.Errorf("renaming temp file: %w", err)
	}

	return nil
}

func (r *BootConfigReconciler) getArtifact(ctx context.Context, name, namespace string) (*isobootgithubiov1alpha1.BootArtifact, error) {
	var artifact isobootgithubiov1alpha1.BootArtifact
	if err := r.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, &artifact); err != nil {
		return nil, err
	}
	return &artifact, nil
}

func ensureSymlink(dir, filename, target string) error {
	link := filepath.Join(dir, filename)
	existing, err := os.Readlink(link)
	if err == nil && existing == target {
		return nil // already correct
	}
	// Symlink is wrong or missing — clean all entries (stale refs, wrong target)
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		return fmt.Errorf("reading dir %s: %w", dir, readErr)
	}
	for _, e := range entries {
		_ = os.RemoveAll(filepath.Join(dir, e.Name()))
	}
	return os.Symlink(target, link)
}

func (r *BootConfigReconciler) setReady(ctx context.Context, bc *isobootgithubiov1alpha1.BootConfig) (ctrl.Result, error) {
	if bc.Status.Phase == isobootgithubiov1alpha1.BootConfigPhaseReady {
		return ctrl.Result{}, nil
	}
	bc.Status.Phase = isobootgithubiov1alpha1.BootConfigPhaseReady
	bc.Status.Message = ""
	if err := r.Status().Update(ctx, bc); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating status: %w", err)
	}
	return ctrl.Result{}, nil
}

func (r *BootConfigReconciler) setPending(ctx context.Context, bc *isobootgithubiov1alpha1.BootConfig, message string) (ctrl.Result, error) {
	if bc.Status.Phase == isobootgithubiov1alpha1.BootConfigPhasePending && bc.Status.Message == message {
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}
	bc.Status.Phase = isobootgithubiov1alpha1.BootConfigPhasePending
	bc.Status.Message = message
	if err := r.Status().Update(ctx, bc); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating status: %w", err)
	}
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

func (r *BootConfigReconciler) setError(ctx context.Context, bc *isobootgithubiov1alpha1.BootConfig, message string) (ctrl.Result, error) {
	if bc.Status.Phase == isobootgithubiov1alpha1.BootConfigPhaseError && bc.Status.Message == message {
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}
	log := logf.FromContext(ctx)
	log.Info("BootConfig error", "message", message)
	bc.Status.Phase = isobootgithubiov1alpha1.BootConfigPhaseError
	bc.Status.Message = message
	if err := r.Status().Update(ctx, bc); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating status: %w", err)
	}
	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

func (r *BootConfigReconciler) findBootConfigsForArtifact(ctx context.Context, obj client.Object) []reconcile.Request {
	var configs isobootgithubiov1alpha1.BootConfigList
	if err := r.List(ctx, &configs, client.InNamespace(obj.GetNamespace())); err != nil {
		return nil
	}

	var requests []reconcile.Request
	for i := range configs.Items {
		bc := &configs.Items[i]
		if (bc.Spec.Netboot != nil && (bc.Spec.Netboot.KernelRef == obj.GetName() ||
			bc.Spec.Netboot.InitrdRef == obj.GetName() ||
			bc.Spec.Netboot.FirmwareRef == obj.GetName())) ||
			(bc.Spec.ISO != nil && bc.Spec.ISO.ArtifactRef == obj.GetName()) {
			requests = append(requests, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(bc),
			})
		}
	}
	return requests
}

// SetupWithManager sets up the controller with the Manager.
func (r *BootConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&isobootgithubiov1alpha1.BootConfig{}).
		Watches(&isobootgithubiov1alpha1.BootArtifact{}, handler.EnqueueRequestsFromMapFunc(
			r.findBootConfigsForArtifact,
		)).
		Named("bootconfig").
		Complete(r)
}
