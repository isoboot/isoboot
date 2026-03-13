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
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	isobootgithubiov1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
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

	// Only Mode A (direct refs) for now
	if bc.Spec.Kernel == nil || bc.Spec.Initrd == nil {
		log.V(1).Info("Skipping BootConfig without direct refs")
		return ctrl.Result{}, nil
	}

	// Look up referenced BootArtifacts
	kernelArtifact, err := r.getArtifact(ctx, bc.Spec.Kernel.Ref, bc.Namespace)
	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			return ctrl.Result{}, err
		}
		return r.setError(ctx, &bc, fmt.Sprintf("kernel artifact %q not found", bc.Spec.Kernel.Ref))
	}

	initrdArtifact, err := r.getArtifact(ctx, bc.Spec.Initrd.Ref, bc.Namespace)
	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			return ctrl.Result{}, err
		}
		return r.setError(ctx, &bc, fmt.Sprintf("initrd artifact %q not found", bc.Spec.Initrd.Ref))
	}

	// Check if all artifacts are Ready
	if kernelArtifact.Status.Phase != isobootgithubiov1alpha1.BootArtifactPhaseReady {
		return r.setPending(ctx, &bc, fmt.Sprintf("waiting for kernel artifact %q to be Ready", bc.Spec.Kernel.Ref))
	}
	if initrdArtifact.Status.Phase != isobootgithubiov1alpha1.BootArtifactPhaseReady {
		return r.setPending(ctx, &bc, fmt.Sprintf("waiting for initrd artifact %q to be Ready", bc.Spec.Initrd.Ref))
	}

	// Optionally look up firmware artifact
	var firmwareArtifact *isobootgithubiov1alpha1.BootArtifact
	if bc.Spec.Firmware != nil {
		firmwareArtifact, err = r.getArtifact(ctx, bc.Spec.Firmware.Ref, bc.Namespace)
		if err != nil {
			if client.IgnoreNotFound(err) != nil {
				return ctrl.Result{}, err
			}
			return r.setError(ctx, &bc, fmt.Sprintf("firmware artifact %q not found", bc.Spec.Firmware.Ref))
		}
		if firmwareArtifact.Status.Phase != isobootgithubiov1alpha1.BootArtifactPhaseReady {
			return r.setPending(ctx, &bc, fmt.Sprintf("waiting for firmware artifact %q to be Ready", bc.Spec.Firmware.Ref))
		}
	}

	// Assemble boot directory with symlinks
	bootDir := filepath.Join(r.DataDir, "boot", bc.Name)

	kernelFilename := filenameFromURL(kernelArtifact.Spec.URL)
	initrdFilename := filenameFromURL(initrdArtifact.Spec.URL)

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
		firmwareFilename := filenameFromURL(firmwareArtifact.Spec.URL)
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
	entries, _ := os.ReadDir(dir)
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
	entries, _ := os.ReadDir(dir)
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
		if (bc.Spec.Kernel != nil && bc.Spec.Kernel.Ref == obj.GetName()) ||
			(bc.Spec.Initrd != nil && bc.Spec.Initrd.Ref == obj.GetName()) ||
			(bc.Spec.Firmware != nil && bc.Spec.Firmware.Ref == obj.GetName()) {
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
