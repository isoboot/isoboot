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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	// ErrInvalidPhaseTransition indicates the requested phase transition
	// is not allowed from the current phase.
	ErrInvalidPhaseTransition = errors.New("invalid phase transition")
)

// validTransitions maps each target phase to its allowed source phase.
var validTransitions = map[isobootgithubiov1alpha1.ProvisionPhase]isobootgithubiov1alpha1.ProvisionPhase{
	isobootgithubiov1alpha1.ProvisionPhaseInProgress: isobootgithubiov1alpha1.ProvisionPhasePending,
	isobootgithubiov1alpha1.ProvisionPhaseComplete:   isobootgithubiov1alpha1.ProvisionPhaseInProgress,
}

// UpdateProvisionPhase transitions a Provision to the given target phase.
// It validates that the current phase allows the transition.
func UpdateProvisionPhase(
	ctx context.Context, c client.Client, ns, provisionName string,
	target isobootgithubiov1alpha1.ProvisionPhase, message string,
) error {
	requiredSource, ok := validTransitions[target]
	if !ok {
		return fmt.Errorf("%w: cannot transition to %s",
			ErrInvalidPhaseTransition, target)
	}

	var provision isobootgithubiov1alpha1.Provision
	if err := c.Get(ctx, client.ObjectKey{
		Name:      provisionName,
		Namespace: ns,
	}, &provision); err != nil {
		return fmt.Errorf("getting provision %q: %w", provisionName, err)
	}

	if provision.Status.Phase != requiredSource {
		return fmt.Errorf(
			"%w: cannot transition from %s to %s",
			ErrInvalidPhaseTransition,
			provision.Status.Phase, target)
	}

	now := metav1.Now()
	provision.Status.Phase = target
	provision.Status.Message = message
	provision.Status.LastUpdated = &now
	return c.Status().Update(ctx, &provision)
}

// IsProvisionNotFound reports whether err indicates the Provision
// was not found.
func IsProvisionNotFound(err error) bool {
	return apierrors.IsNotFound(err)
}

// IsProvisionPhaseError reports whether err indicates a phase
// transition error.
func IsProvisionPhaseError(err error) bool {
	return errors.Is(err, ErrInvalidPhaseTransition)
}

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
