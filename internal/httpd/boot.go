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
	"bytes"
	"context"
	"errors"
	"fmt"
	"path"
	"text/template"

	"sigs.k8s.io/controller-runtime/pkg/client"

	isobootgithubiov1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
	"github.com/isoboot/isoboot/internal/urlutil"
)

// BootDirective holds the data needed to construct an iPXE boot script.
type BootDirective struct {
	KernelPath    string
	KernelArgs    string
	InitrdPath    string
	ProvisionName string
}

// KernelArgsData holds the template data for kernel args rendering.
type KernelArgsData struct {
	ProvisionAutomationBaseURL string
	ProxyURL                   string
	UpdatePhaseURL             string
	ProvisionName              string
}

// RenderKernelArgs renders kernel args as a Go template with the given data.
func RenderKernelArgs(args string, data KernelArgsData) (string, error) {
	tmpl, err := template.New("kernelArgs").
		Option("missingkey=error").Parse(args)
	if err != nil {
		return "", fmt.Errorf("parsing kernel args template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("executing kernel args template: %w", err)
	}

	return buf.String(), nil
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

	// Mode B (ISO): kernel and initrd are extracted to fixed filenames.
	if bc.Spec.ISO != nil {
		return &BootDirective{
			KernelPath:    path.Join(bc.Name, "vmlinuz"),
			KernelArgs:    bc.Spec.KernelArgs,
			InitrdPath:    path.Join(bc.Name, "initrd"),
			ProvisionName: provision.Name,
		}, nil
	}

	if bc.Spec.Netboot == nil {
		return nil, fmt.Errorf(
			"boot config %q has neither netboot nor iso", bc.Name)
	}

	var kernelArtifact isobootgithubiov1alpha1.BootArtifact
	if err := c.Get(ctx, client.ObjectKey{
		Name:      bc.Spec.Netboot.KernelRef,
		Namespace: ns,
	}, &kernelArtifact); err != nil {
		return nil, fmt.Errorf("getting kernel artifact %q: %w",
			bc.Spec.Netboot.KernelRef, err)
	}

	var initrdArtifact isobootgithubiov1alpha1.BootArtifact
	if err := c.Get(ctx, client.ObjectKey{
		Name:      bc.Spec.Netboot.InitrdRef,
		Namespace: ns,
	}, &initrdArtifact); err != nil {
		return nil, fmt.Errorf("getting initrd artifact %q: %w",
			bc.Spec.Netboot.InitrdRef, err)
	}

	kernelFile := urlutil.FilenameFromURL(kernelArtifact.Spec.URL)
	initrdFile := urlutil.FilenameFromURL(initrdArtifact.Spec.URL)

	return &BootDirective{
		KernelPath:    path.Join(bc.Name, "kernel", kernelFile),
		KernelArgs:    bc.Spec.KernelArgs,
		InitrdPath:    path.Join(bc.Name, "initrd", initrdFile),
		ProvisionName: provision.Name,
	}, nil
}
