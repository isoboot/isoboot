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
	"path"

	"sigs.k8s.io/controller-runtime/pkg/client"

	isobootgithubiov1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
	"github.com/isoboot/isoboot/internal/urlutil"
)

// BootDirective holds the data needed to construct an iPXE boot script.
type BootDirective struct {
	KernelPath string
	KernelArgs string
	InitrdPath string
}

// IsDuplicateError reports whether err indicates a duplicate machine or provision.
func IsDuplicateError(err error) bool {
	return errors.Is(err, ErrMultipleMachines) ||
		errors.Is(err, ErrMultipleProvisions)
}

// BootDirectiveForMAC looks up the pending provision for the given MAC address
// and returns boot directive data. It returns nil if no pending provision exists.
func BootDirectiveForMAC(
	ctx context.Context, c client.Client, ns, mac string,
) (*BootDirective, error) {
	provision, err := PendingProvisionForMAC(ctx, c, ns, mac)
	if err != nil {
		return nil, err
	}
	if provision == nil {
		return nil, nil
	}

	var bc isobootgithubiov1alpha1.BootConfig
	if err := c.Get(ctx, client.ObjectKey{
		Name:      provision.Spec.BootConfigRef,
		Namespace: ns,
	}, &bc); err != nil {
		return nil, fmt.Errorf("getting boot config %q: %w",
			provision.Spec.BootConfigRef, err)
	}

	if bc.Spec.Kernel == nil || bc.Spec.Initrd == nil {
		return nil, fmt.Errorf(
			"boot config %q missing kernel or initrd", bc.Name)
	}

	var kernelArtifact isobootgithubiov1alpha1.BootArtifact
	if err := c.Get(ctx, client.ObjectKey{
		Name:      bc.Spec.Kernel.Ref,
		Namespace: ns,
	}, &kernelArtifact); err != nil {
		return nil, fmt.Errorf("getting kernel artifact %q: %w",
			bc.Spec.Kernel.Ref, err)
	}

	var initrdArtifact isobootgithubiov1alpha1.BootArtifact
	if err := c.Get(ctx, client.ObjectKey{
		Name:      bc.Spec.Initrd.Ref,
		Namespace: ns,
	}, &initrdArtifact); err != nil {
		return nil, fmt.Errorf("getting initrd artifact %q: %w",
			bc.Spec.Initrd.Ref, err)
	}

	kernelFile := urlutil.FilenameFromURL(kernelArtifact.Spec.URL)
	initrdFile := urlutil.FilenameFromURL(initrdArtifact.Spec.URL)

	return &BootDirective{
		KernelPath: path.Join(bc.Name, "kernel", kernelFile),
		KernelArgs: bc.Spec.Kernel.Args,
		InitrdPath: path.Join(bc.Name, "initrd", initrdFile),
	}, nil
}
