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
	if bc.Spec.KernelRef == nil || bc.Spec.InitrdRef == nil {
		return ctrl.Result{}, nil
	}

	// Look up referenced BootArtifacts
	kernelArtifact, err := r.getArtifact(ctx, *bc.Spec.KernelRef, bc.Namespace)
	if err != nil {
		return r.setError(ctx, &bc, fmt.Sprintf("kernel artifact %q not found", *bc.Spec.KernelRef))
	}

	initrdArtifact, err := r.getArtifact(ctx, *bc.Spec.InitrdRef, bc.Namespace)
	if err != nil {
		return r.setError(ctx, &bc, fmt.Sprintf("initrd artifact %q not found", *bc.Spec.InitrdRef))
	}

	// Check if all artifacts are Ready
	if kernelArtifact.Status.Phase != isobootgithubiov1alpha1.BootArtifactPhaseReady {
		return r.setPending(ctx, &bc, fmt.Sprintf("waiting for kernel artifact %q to be Ready", *bc.Spec.KernelRef))
	}
	if initrdArtifact.Status.Phase != isobootgithubiov1alpha1.BootArtifactPhaseReady {
		return r.setPending(ctx, &bc, fmt.Sprintf("waiting for initrd artifact %q to be Ready", *bc.Spec.InitrdRef))
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

	// Create symlinks (relative paths)
	kernelLink := filepath.Join(kernelDir, kernelFilename)
	kernelTarget := filepath.Join("..", "..", "..", "artifacts", kernelArtifact.Name, kernelFilename)
	if err := r.ensureSymlink(kernelLink, kernelTarget); err != nil {
		return r.setError(ctx, &bc, fmt.Sprintf("creating kernel symlink: %v", err))
	}

	initrdLink := filepath.Join(initrdDir, initrdFilename)
	initrdTarget := filepath.Join("..", "..", "..", "artifacts", initrdArtifact.Name, initrdFilename)
	if err := r.ensureSymlink(initrdLink, initrdTarget); err != nil {
		return r.setError(ctx, &bc, fmt.Sprintf("creating initrd symlink: %v", err))
	}

	if bc.Status.Phase != isobootgithubiov1alpha1.BootConfigPhaseReady {
		log.Info("BootConfig assembled", "name", bc.Name, "bootDir", bootDir)
	}

	return r.setReady(ctx, &bc)
}

func (r *BootConfigReconciler) getArtifact(ctx context.Context, name, namespace string) (*isobootgithubiov1alpha1.BootArtifact, error) {
	var artifact isobootgithubiov1alpha1.BootArtifact
	if err := r.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, &artifact); err != nil {
		return nil, err
	}
	return &artifact, nil
}

func (r *BootConfigReconciler) ensureSymlink(link, target string) error {
	existing, err := os.Readlink(link)
	if err == nil && existing == target {
		return nil // already correct
	}
	// Remove stale link or file
	_ = os.Remove(link)
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
	if err := r.List(ctx, &configs); err != nil {
		return nil
	}

	var requests []reconcile.Request
	for i := range configs.Items {
		bc := &configs.Items[i]
		if (bc.Spec.KernelRef != nil && *bc.Spec.KernelRef == obj.GetName()) ||
			(bc.Spec.InitrdRef != nil && *bc.Spec.InitrdRef == obj.GetName()) {
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
