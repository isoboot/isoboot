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

package httpd

import (
	"context"
	"errors"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	isobootgithubiov1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
	"github.com/isoboot/isoboot/internal/controller"
)

var (
	// ErrMultipleMachines indicates more than one Machine shares a MAC.
	ErrMultipleMachines = errors.New("multiple machines")
	// ErrMultipleProvisions indicates more than one pending Provision
	// exists for the same Machine.
	ErrMultipleProvisions = errors.New("multiple pending provisions")
)

// PendingProvisionForMAC returns the Provision with status.phase == Pending
// for the Machine with the given MAC address. It returns nil if no match is
// found, or an error if multiple machines or provisions match.
func PendingProvisionForMAC(
	ctx context.Context, c client.Client, ns, mac string,
) (*isobootgithubiov1alpha1.Provision, error) {
	var machines isobootgithubiov1alpha1.MachineList
	if err := c.List(ctx, &machines,
		client.InNamespace(ns),
		client.MatchingFields{controller.MachineSpecMACField: mac},
	); err != nil {
		return nil, fmt.Errorf("listing machines: %w", err)
	}

	switch len(machines.Items) {
	case 0:
		return nil, nil
	case 1:
		// proceed
	default:
		return nil, fmt.Errorf(
			"%w with MAC %s", ErrMultipleMachines, mac)
	}

	var provisions isobootgithubiov1alpha1.ProvisionList
	if err := c.List(ctx, &provisions,
		client.InNamespace(ns),
		client.MatchingFields{
			controller.ProvisionMachineRefField: machines.Items[0].Name,
			controller.ProvisionPhaseField:      string(isobootgithubiov1alpha1.ProvisionPhasePending),
		},
	); err != nil {
		return nil, fmt.Errorf("listing provisions: %w", err)
	}

	switch len(provisions.Items) {
	case 0:
		return nil, nil
	case 1:
		return &provisions.Items[0], nil
	default:
		return nil, fmt.Errorf(
			"%w for MAC %s", ErrMultipleProvisions, mac)
	}
}
