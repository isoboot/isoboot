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

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
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
}

// +kubebuilder:rbac:groups=isoboot.github.io,resources=bootsources,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=isoboot.github.io,resources=bootsources/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=isoboot.github.io,resources=bootsources/finalizers,verbs=update
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=create;get;list;watch

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

	// TODO: Handle other phases (Downloading, Verifying, etc.)

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *BootSourceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&isobootv1alpha1.BootSource{}).
		Named("bootsource").
		Complete(r)
}
