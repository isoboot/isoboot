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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	isobootv1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
)

// JobBuilder builds Jobs for BootSource resources
type JobBuilder interface {
	Build(bootSource *isobootv1alpha1.BootSource) *batchv1.Job
}

// DefaultJobBuilder builds download Jobs using busybox
type DefaultJobBuilder struct{}

// Build creates a Job spec for downloading files
func (b *DefaultJobBuilder) Build(bootSource *isobootv1alpha1.BootSource) *batchv1.Job {
	backoffLimit := int32(0)

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: fmt.Sprintf("%s-download-", bootSource.Name),
			Namespace:    bootSource.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":       "isoboot",
				"app.kubernetes.io/component":  "download",
				"app.kubernetes.io/managed-by": "isoboot-controller",
				"isoboot.github.io/bootsource": bootSource.Name,
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoffLimit,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:    "download",
							Image:   "busybox:1.37",
							Command: []string{"sleep", "10"},
						},
					},
				},
			},
		},
	}
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
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;delete

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

	// Dispatch based on current phase
	switch bootSource.Status.Phase {
	case isobootv1alpha1.PhasePending:
		return r.handlePending(ctx, bootSource)
	case isobootv1alpha1.PhaseDownloading:
		return r.handleDownloading(ctx, bootSource)
	}

	return ctrl.Result{}, nil
}

// handlePending creates a download Job and transitions to Downloading phase
func (r *BootSourceReconciler) handlePending(ctx context.Context, bootSource *isobootv1alpha1.BootSource) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Create the download Job
	job := r.JobBuilder.Build(bootSource)
	if err := ctrl.SetControllerReference(bootSource, job, r.Scheme); err != nil {
		log.Error(err, "Failed to set controller reference on Job")
		return ctrl.Result{}, err
	}

	if err := r.Create(ctx, job); err != nil {
		log.Error(err, "Failed to create download Job")
		return ctrl.Result{}, err
	}
	log.Info("Created download Job", "job", job.Name)

	// Update status to Downloading
	bootSource.Status.Phase = isobootv1alpha1.PhaseDownloading
	bootSource.Status.DownloadJobName = job.Name
	bootSource.Status.Message = "Download job created"
	if err := r.Status().Update(ctx, bootSource); err != nil {
		log.Error(err, "Failed to update BootSource status to Downloading")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// handleDownloading monitors the download Job and transitions based on its status
func (r *BootSourceReconciler) handleDownloading(ctx context.Context, bootSource *isobootv1alpha1.BootSource) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Get the Job
	job := &batchv1.Job{}
	jobName := bootSource.Status.DownloadJobName
	if jobName == "" {
		// No job name recorded, go back to Pending to create one
		bootSource.Status.Phase = isobootv1alpha1.PhasePending
		bootSource.Status.Message = "No download job found, retrying"
		if err := r.Status().Update(ctx, bootSource); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	err := r.Get(ctx, client.ObjectKey{Namespace: bootSource.Namespace, Name: jobName}, job)
	if err != nil {
		if errors.IsNotFound(err) {
			// Job was deleted, go back to Pending
			log.Info("Download job not found, returning to Pending", "job", jobName)
			bootSource.Status.Phase = isobootv1alpha1.PhasePending
			bootSource.Status.DownloadJobName = ""
			bootSource.Status.Message = "Download job not found, retrying"
			if err := r.Status().Update(ctx, bootSource); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get download Job")
		return ctrl.Result{}, err
	}

	// Check Job status
	if job.Status.Succeeded > 0 {
		// Job completed successfully, transition to Verifying
		log.Info("Download job succeeded, transitioning to Verifying", "job", jobName)
		bootSource.Status.Phase = isobootv1alpha1.PhaseVerifying
		bootSource.Status.Message = "Download completed, verifying"
		if err := r.Status().Update(ctx, bootSource); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if job.Status.Failed > 0 {
		// Job failed
		log.Info("Download job failed", "job", jobName)
		bootSource.Status.Phase = isobootv1alpha1.PhaseFailed
		bootSource.Status.Message = "Download job failed"
		if err := r.Status().Update(ctx, bootSource); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Job is still running, nothing to do (we'll be notified via watch)
	log.Info("Download job still running", "job", jobName)
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *BootSourceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&isobootv1alpha1.BootSource{}).
		Owns(&batchv1.Job{}).
		Named("bootsource").
		Complete(r)
}
