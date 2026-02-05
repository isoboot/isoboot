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

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	isobootv1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
	"github.com/isoboot/isoboot/internal/downloader"
)

// JobBuilder builds a Kubernetes Job for a BootSource.
type JobBuilder interface {
	Build(bootSource *isobootv1alpha1.BootSource) (*batchv1.Job, error)
}

// BootSourceReconciler reconciles a BootSource object
type BootSourceReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	JobBuilder JobBuilder
}

// +kubebuilder:rbac:groups=isoboot.github.io,resources=bootsources,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=isoboot.github.io,resources=bootsources/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=isoboot.github.io,resources=bootsources/finalizers,verbs=update
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create

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
		return ctrl.Result{}, r.setPhase(ctx, bootSource, isobootv1alpha1.PhasePending)
	}

	switch bootSource.Status.Phase {
	case isobootv1alpha1.PhasePending:
		return r.reconcilePending(ctx, bootSource)
	case isobootv1alpha1.PhaseDownloading:
		return r.reconcileDownloading(ctx, bootSource)
	case isobootv1alpha1.PhaseReady, isobootv1alpha1.PhaseFailed:
		// Terminal phases: nothing to reconcile.
		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, nil
}

// reconcilePending creates a download Job and transitions to Downloading.
func (r *BootSourceReconciler) reconcilePending(ctx context.Context, bootSource *isobootv1alpha1.BootSource) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	job, err := r.JobBuilder.Build(bootSource)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("building download job: %w", err)
	}

	if err := r.Create(ctx, job); err != nil {
		if errors.IsAlreadyExists(err) {
			log.Info("Download job already exists", "job", job.Name)
		} else {
			return ctrl.Result{}, fmt.Errorf("creating download job: %w", err)
		}
	} else {
		log.Info("Created download job", "job", job.Name)
	}

	return ctrl.Result{}, r.setPhase(ctx, bootSource, isobootv1alpha1.PhaseDownloading)
}

// reconcileDownloading checks the download Job status and transitions accordingly.
func (r *BootSourceReconciler) reconcileDownloading(ctx context.Context, bootSource *isobootv1alpha1.BootSource) (ctrl.Result, error) {
	job := &batchv1.Job{}
	jobName := types.NamespacedName{Name: bootSource.Name + downloader.JobNameSuffix, Namespace: bootSource.Namespace}
	if err := r.Get(ctx, jobName, job); err != nil {
		if errors.IsNotFound(err) {
			// Job was deleted externally; go back to Pending to recreate
			return ctrl.Result{}, r.setPhase(ctx, bootSource, isobootv1alpha1.PhasePending)
		}
		return ctrl.Result{}, fmt.Errorf("getting download job: %w", err)
	}

	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobComplete && c.Status == corev1.ConditionTrue {
			return ctrl.Result{}, r.setPhase(ctx, bootSource, isobootv1alpha1.PhaseReady)
		}
		if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
			return ctrl.Result{}, r.setPhase(ctx, bootSource, isobootv1alpha1.PhaseFailed)
		}
	}

	// Job still running — nothing to do, we'll be requeued when the Job updates
	return ctrl.Result{}, nil
}

// setPhase updates the BootSource status phase.
func (r *BootSourceReconciler) setPhase(ctx context.Context, bootSource *isobootv1alpha1.BootSource, phase isobootv1alpha1.BootSourcePhase) error {
	log := logf.FromContext(ctx)
	bootSource.Status.Phase = phase
	if err := r.Status().Update(ctx, bootSource); err != nil {
		log.Error(err, "Failed to update BootSource phase", "phase", phase)
		return err
	}
	log.Info("Updated BootSource phase", "name", bootSource.Name, "phase", phase)
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *BootSourceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&isobootv1alpha1.BootSource{}).
		Owns(&batchv1.Job{}).
		Named("bootsource").
		Complete(r)
}
