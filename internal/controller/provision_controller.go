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

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	isobootgithubiov1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
)

// ProvisionReconciler reconciles a Provision object
type ProvisionReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=isoboot.github.io,resources=provisions,verbs=get;list;watch
// +kubebuilder:rbac:groups=isoboot.github.io,resources=provisions/status,verbs=get;update;patch

func (r *ProvisionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var prov isobootgithubiov1alpha1.Provision
	if err := r.Get(ctx, req.NamespacedName, &prov); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if prov.Status.Phase == "" {
		log.Info("Initializing Provision phase", "name", prov.Name)
		prov.Status.Phase = isobootgithubiov1alpha1.ProvisionPhasePending
		if err := r.Status().Update(ctx, &prov); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ProvisionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&isobootgithubiov1alpha1.Provision{}).
		Named("provision").
		Complete(r)
}
