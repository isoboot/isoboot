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

	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	isobootv1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
)

// BootSourceReconciler reconciles a BootSource object
type BootSourceReconciler struct {
	client.Client
	Scheme          *runtime.Scheme
	HostPathBaseDir string
	DownloadImage   string
}

// +kubebuilder:rbac:groups=isoboot.github.io,resources=bootsources,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=isoboot.github.io,resources=bootsources/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=isoboot.github.io,resources=bootsources/finalizers,verbs=update
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=create;get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.1/pkg/reconcile
func (r *BootSourceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Fetch the BootSource instance
	bootSource := &isobootv1alpha1.BootSource{}
	if err := r.Get(ctx, req.NamespacedName, bootSource); err != nil {
		if errors.IsNotFound(err) {
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

	switch bootSource.Status.Phase {
	case isobootv1alpha1.PhasePending:
		return r.reconcilePending(ctx, bootSource)
	case isobootv1alpha1.PhaseDownloading:
		return r.reconcileDownloading(ctx, bootSource)
	}

	return ctrl.Result{}, nil
}

// reconcilePending creates a download Job and transitions to Downloading.
func (r *BootSourceReconciler) reconcilePending(ctx context.Context, bootSource *isobootv1alpha1.BootSource) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	job, err := buildDownloadJob(bootSource, r.Scheme, r.HostPathBaseDir, r.DownloadImage)
	if err != nil {
		log.Error(err, "Failed to build download job")
		return ctrl.Result{}, err
	}

	if err := r.Create(ctx, job); err != nil {
		if !errors.IsAlreadyExists(err) {
			log.Error(err, "Failed to create download job")
			return ctrl.Result{}, err
		}
		log.Info("Download job already exists", "job", job.Name)
	}

	bootSource.Status.Phase = isobootv1alpha1.PhaseDownloading
	bootSource.Status.DownloadJobName = job.Name
	if err := r.Status().Update(ctx, bootSource); err != nil {
		log.Error(err, "Failed to update BootSource status to Downloading")
		return ctrl.Result{}, err
	}

	log.Info("Created download job, transitioning to Downloading", "job", job.Name)
	return ctrl.Result{}, nil
}

// reconcileDownloading checks the download Job status and transitions accordingly.
func (r *BootSourceReconciler) reconcileDownloading(ctx context.Context, bootSource *isobootv1alpha1.BootSource) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if bootSource.Status.DownloadJobName == "" {
		log.Info("DownloadJobName is empty, reverting to Pending")
		bootSource.Status.Phase = isobootv1alpha1.PhasePending
		if err := r.Status().Update(ctx, bootSource); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	job := &batchv1.Job{}
	jobKey := types.NamespacedName{Name: bootSource.Status.DownloadJobName, Namespace: bootSource.Namespace}
	if err := r.Get(ctx, jobKey, job); err != nil {
		if errors.IsNotFound(err) {
			log.Info("Download job not found, reverting to Pending")
			bootSource.Status.Phase = isobootv1alpha1.PhasePending
			bootSource.Status.DownloadJobName = ""
			if err := r.Status().Update(ctx, bootSource); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	for _, cond := range job.Status.Conditions {
		if cond.Type == batchv1.JobComplete && cond.Status == "True" {
			log.Info("Download job completed, transitioning to Verifying")
			bootSource.Status.Phase = isobootv1alpha1.PhaseVerifying
			bootSource.Status.ArtifactPaths = buildArtifactPaths(ctx, bootSource.Spec, r.HostPathBaseDir, bootSource.Namespace, bootSource.Name)
			if err := r.Status().Update(ctx, bootSource); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
		if cond.Type == batchv1.JobFailed && cond.Status == "True" {
			log.Info("Download job failed, transitioning to Failed")
			bootSource.Status.Phase = isobootv1alpha1.PhaseFailed
			if err := r.Status().Update(ctx, bootSource); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
	}

	// Job still running — nothing to do; the Owns watch will re-queue us.
	return ctrl.Result{}, nil
}

// buildArtifactPaths computes a map of resource type to host path for binary
// files (not shasums) based on the BootSource spec.
func buildArtifactPaths(ctx context.Context, spec isobootv1alpha1.BootSourceSpec, baseDir, namespace, name string) map[string]string {
	log := logf.FromContext(ctx)
	paths := make(map[string]string)

	type entry struct {
		rt  ResourceType
		url string
	}

	var entries []entry
	if spec.Kernel != nil {
		entries = append(entries, entry{rt: ResourceKernel, url: spec.Kernel.URL.Binary})
	}
	if spec.Initrd != nil {
		entries = append(entries, entry{rt: ResourceInitrd, url: spec.Initrd.URL.Binary})
	}
	if spec.Firmware != nil {
		entries = append(entries, entry{rt: ResourceFirmware, url: spec.Firmware.URL.Binary})
	}
	if spec.ISO != nil {
		entries = append(entries, entry{rt: ResourceISO, url: spec.ISO.URL.Binary})
	}

	for _, e := range entries {
		p, err := DownloadPath(baseDir, namespace, name, e.rt, e.url)
		if err != nil {
			log.Error(err, "Skipping artifact path for resource type", "resourceType", e.rt, "url", e.url)
			continue
		}
		paths[string(e.rt)] = p
	}
	return paths
}

// SetupWithManager sets up the controller with the Manager.
func (r *BootSourceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&isobootv1alpha1.BootSource{}).
		Owns(&batchv1.Job{}).
		Named("bootsource").
		Complete(r)
}
