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
	"bytes"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"text/template"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"

	isobootv1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
)

// downloadTask represents a single file to download.
type downloadTask struct {
	// EncodedURL is the base64-encoded URL to download from.
	EncodedURL string
	// OutputPath is the absolute host path where the file should be written.
	OutputPath string
}

// collectDownloadTasks iterates over the BootSource spec and returns download
// tasks for every binary and shasum file that needs to be fetched.
func collectDownloadTasks(spec isobootv1alpha1.BootSourceSpec, baseDir, namespace, name string) ([]downloadTask, error) {
	type urlEntry struct {
		rt  ResourceType
		url isobootv1alpha1.URLSource
	}

	var entries []urlEntry
	if spec.Kernel != nil {
		entries = append(entries, urlEntry{rt: ResourceKernel, url: spec.Kernel.URL})
	}
	if spec.Initrd != nil {
		entries = append(entries, urlEntry{rt: ResourceInitrd, url: spec.Initrd.URL})
	}
	if spec.Firmware != nil {
		entries = append(entries, urlEntry{rt: ResourceFirmware, url: spec.Firmware.URL})
	}
	if spec.ISO != nil {
		entries = append(entries, urlEntry{rt: ResourceISO, url: spec.ISO.URL})
	}

	var tasks []downloadTask
	for _, e := range entries {
		for _, raw := range []string{e.url.Binary, e.url.Shasum} {
			outPath, err := DownloadPath(baseDir, namespace, name, e.rt, raw)
			if err != nil {
				return nil, fmt.Errorf("computing download path for %s %q: %w", e.rt, raw, err)
			}
			tasks = append(tasks, downloadTask{
				EncodedURL: base64.StdEncoding.EncodeToString([]byte(raw)),
				OutputPath: outPath,
			})
		}
	}
	return tasks, nil
}

// scriptTask holds per-task data for the download script template.
type scriptTask struct {
	Index      int
	EncodedURL string
	OutputDir  string
	OutputPath string
}

// downloadScriptTmpl is the shell template for downloading files. Base64
// alphabet characters [A-Za-z0-9+/=] cannot break out of single quotes, so
// EncodedURL is safe to interpolate. OutputDir and OutputPath come from
// DownloadPath which only produces filesystem-safe characters.
var downloadScriptTmpl = template.Must(template.New("download").Parse(`set -eu
apk add --no-cache wget
{{- range . }}
mkdir -p '{{ .OutputDir }}'
echo '{{ .EncodedURL }}' | base64 -d > '/tmp/url_{{ .Index }}.txt'
wget -q -i '/tmp/url_{{ .Index }}.txt' -O '{{ .OutputPath }}'
rm -f '/tmp/url_{{ .Index }}.txt'
{{- end }}
`))

// buildDownloadScript generates a shell script that downloads every task.
// URLs are base64-encoded and decoded to a temporary file at runtime, so they
// never enter a shell-interpreted context.
func buildDownloadScript(tasks []downloadTask) string {
	data := make([]scriptTask, len(tasks))
	for i, t := range tasks {
		data[i] = scriptTask{
			Index:      i,
			EncodedURL: t.EncodedURL,
			OutputDir:  filepath.Dir(t.OutputPath),
			OutputPath: t.OutputPath,
		}
	}
	var buf bytes.Buffer
	if err := downloadScriptTmpl.Execute(&buf, data); err != nil {
		// Template is static and data is pre-validated; this should never happen.
		panic(fmt.Sprintf("executing download script template: %v", err))
	}
	return buf.String()
}

const maxJobNameLen = 63

// downloadJobName returns the Job name for a given BootSource name.
func downloadJobName(bootSourceName string) string {
	name := bootSourceName + "-download"
	if len(name) > maxJobNameLen {
		name = name[:maxJobNameLen]
	}
	return name
}

// buildDownloadJob constructs a batch/v1 Job that downloads all resources for
// the given BootSource.
func buildDownloadJob(bootSource *isobootv1alpha1.BootSource, scheme *runtime.Scheme, baseDir, downloadImage string) (*batchv1.Job, error) {
	tasks, err := collectDownloadTasks(bootSource.Spec, baseDir, bootSource.Namespace, bootSource.Name)
	if err != nil {
		return nil, err
	}

	script := buildDownloadScript(tasks)

	jobName := downloadJobName(bootSource.Name)

	volumeDir := filepath.Join(baseDir, bootSource.Namespace, bootSource.Name)
	dirOrCreate := corev1.HostPathDirectoryOrCreate
	var backoffLimit int32 = 2

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: bootSource.Namespace,
			Labels: map[string]string{
				"isoboot.github.io/bootsource-name": bootSource.Name,
				"app.kubernetes.io/managed-by":      "isoboot",
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
							Image:   downloadImage,
							Command: []string{"/bin/sh", "-c", script},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "data",
									MountPath: volumeDir,
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "data",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: volumeDir,
									Type: &dirOrCreate,
								},
							},
						},
					},
				},
			},
		},
	}

	if err := ctrl.SetControllerReference(bootSource, job, scheme); err != nil {
		return nil, fmt.Errorf("setting owner reference: %w", err)
	}

	return job, nil
}
