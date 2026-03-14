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
	"fmt"
	"maps"
	"text/template"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	isobootgithubiov1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
)

// TemplateData holds the merged data passed to automation file templates.
type TemplateData struct {
	ConfigMaps map[string]string
	Secrets    map[string]string
}

// RenderAutomationFile looks up a Provision by name, fetches the referenced
// ProvisionAutomation, and renders the named file template using merged
// ConfigMap and Secret data from the Provision.
func RenderAutomationFile(
	ctx context.Context, c client.Client, ns, provisionName, fileName string,
) (string, error) {
	var provision isobootgithubiov1alpha1.Provision
	if err := c.Get(ctx, client.ObjectKey{
		Name:      provisionName,
		Namespace: ns,
	}, &provision); err != nil {
		return "", fmt.Errorf("getting provision %q: %w", provisionName, err)
	}

	var pa isobootgithubiov1alpha1.ProvisionAutomation
	if err := c.Get(ctx, client.ObjectKey{
		Name:      provision.Spec.ProvisionAutomationRef,
		Namespace: ns,
	}, &pa); err != nil {
		return "", fmt.Errorf("getting provision automation %q: %w",
			provision.Spec.ProvisionAutomationRef, err)
	}

	tmplContent, ok := pa.Spec.Files[fileName]
	if !ok {
		return "", fmt.Errorf("file %q not found in provision automation %q",
			fileName, pa.Name)
	}

	data, err := buildTemplateData(ctx, c, ns, &provision)
	if err != nil {
		return "", err
	}

	tmpl, err := template.New(fileName).Parse(tmplContent)
	if err != nil {
		return "", fmt.Errorf("parsing template %q: %w", fileName, err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("executing template %q: %w", fileName, err)
	}

	return buf.String(), nil
}

func buildTemplateData(
	ctx context.Context, c client.Client, ns string,
	provision *isobootgithubiov1alpha1.Provision,
) (*TemplateData, error) {
	data := &TemplateData{
		ConfigMaps: make(map[string]string),
		Secrets:    make(map[string]string),
	}

	for _, name := range provision.Spec.ConfigMaps {
		var cm corev1.ConfigMap
		if err := c.Get(ctx, client.ObjectKey{
			Name:      name,
			Namespace: ns,
		}, &cm); err != nil {
			return nil, fmt.Errorf("getting configmap %q: %w", name, err)
		}
		maps.Copy(data.ConfigMaps, cm.Data)
	}

	for _, name := range provision.Spec.Secrets {
		var secret corev1.Secret
		if err := c.Get(ctx, client.ObjectKey{
			Name:      name,
			Namespace: ns,
		}, &secret); err != nil {
			return nil, fmt.Errorf("getting secret %q: %w", name, err)
		}
		for k, v := range secret.Data {
			data.Secrets[k] = string(v)
		}
	}

	return data, nil
}
