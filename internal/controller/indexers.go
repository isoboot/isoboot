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

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	isobootgithubiov1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
)

// ProvisionPhaseField is the field path used to index Provision
// resources by status.phase.
const ProvisionPhaseField = "status.phase"

// ProvisionMachineRefField is the field path used to index Provision
// resources by spec.machineRef.
const ProvisionMachineRefField = "spec.machineRef"

// MachineSpecMACField is the field path used to index Machine
// resources by spec.mac.
const MachineSpecMACField = "spec.mac"

// +kubebuilder:rbac:groups=isoboot.github.io,resources=provisions,verbs=get;list;watch
// +kubebuilder:rbac:groups=isoboot.github.io,resources=provisions/status,verbs=get
// +kubebuilder:rbac:groups=isoboot.github.io,resources=machines,verbs=get;list;watch

// SetupIndexers registers field indexes on the manager's cache.
func SetupIndexers(ctx context.Context, mgr manager.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(ctx,
		&isobootgithubiov1alpha1.Provision{}, ProvisionPhaseField,
		func(obj client.Object) []string {
			p := obj.(*isobootgithubiov1alpha1.Provision)
			if p.Status.Phase == "" {
				return nil
			}
			return []string{string(p.Status.Phase)}
		}); err != nil {
		return err
	}

	if err := mgr.GetFieldIndexer().IndexField(ctx,
		&isobootgithubiov1alpha1.Provision{}, ProvisionMachineRefField,
		func(obj client.Object) []string {
			p := obj.(*isobootgithubiov1alpha1.Provision)
			if p.Spec.MachineRef == "" {
				return nil
			}
			return []string{p.Spec.MachineRef}
		}); err != nil {
		return err
	}

	return mgr.GetFieldIndexer().IndexField(ctx,
		&isobootgithubiov1alpha1.Machine{}, MachineSpecMACField,
		func(obj client.Object) []string {
			m := obj.(*isobootgithubiov1alpha1.Machine)
			if m.Spec.MAC == "" {
				return nil
			}
			return []string{m.Spec.MAC}
		})
}
