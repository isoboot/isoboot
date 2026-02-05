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

package downloader

import (
	"bytes"
	_ "embed"
	"path/filepath"
	"text/template"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	isobootv1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
)

//go:embed download.sh.tmpl
var scriptTemplate string

// DownloadItem represents a single file to download.
type DownloadItem struct {
	URL  string
	Dest string
}

type templateData struct {
	Dir       string
	Downloads []DownloadItem
}

// ExtractDownloads builds the list of download items from a BootSourceSpec.
func ExtractDownloads(spec isobootv1alpha1.BootSourceSpec, baseDir, namespace, name string) []DownloadItem {
	dir := filepath.Join(baseDir, namespace, name)
	var items []DownloadItem

	if spec.Kernel != nil {
		items = append(items, DownloadItem{
			URL:  spec.Kernel.URL.Binary,
			Dest: filepath.Join(dir, "kernel"),
		})
	}
	if spec.Initrd != nil {
		items = append(items, DownloadItem{
			URL:  spec.Initrd.URL.Binary,
			Dest: filepath.Join(dir, "initrd"),
		})
	}
	if spec.Firmware != nil {
		items = append(items, DownloadItem{
			URL:  spec.Firmware.URL.Binary,
			Dest: filepath.Join(dir, "firmware"),
		})
	}
	if spec.ISO != nil {
		items = append(items, DownloadItem{
			URL:  spec.ISO.URL.Binary,
			Dest: filepath.Join(dir, "iso"),
		})
	}

	return items
}

// JobBuilder builds download Jobs for a BootSource.
type JobBuilder struct {
	BaseDir string
}

// NewJobBuilder returns a JobBuilder that creates Jobs downloading into baseDir.
func NewJobBuilder(baseDir string) *JobBuilder {
	return &JobBuilder{BaseDir: baseDir}
}

// Build creates a Kubernetes Job that downloads all binary files for a BootSource.
func (b *JobBuilder) Build(bs *isobootv1alpha1.BootSource) (*batchv1.Job, error) {
	return buildJob(bs, b.BaseDir)
}

func buildJob(bs *isobootv1alpha1.BootSource, baseDir string) (*batchv1.Job, error) {
	downloads := ExtractDownloads(bs.Spec, baseDir, bs.Namespace, bs.Name)
	dir := filepath.Join(baseDir, bs.Namespace, bs.Name)

	tmpl, err := template.New("download").Parse(scriptTemplate)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, templateData{
		Dir:       dir,
		Downloads: downloads,
	}); err != nil {
		return nil, err
	}

	backoffLimit := int32(3)
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      bs.Name + "-download",
			Namespace: bs.Namespace,
			Labels: map[string]string{
				"isoboot.github.io/bootsource": bs.Name,
				"app.kubernetes.io/component":  "downloader",
				"app.kubernetes.io/managed-by": "isoboot",
			},
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(bs, isobootv1alpha1.GroupVersion.WithKind("BootSource")),
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
							Image:   "curlimages/curl",
							Command: []string{"/bin/sh", "-c", buf.String()},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "data",
									MountPath: baseDir,
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "data",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: baseDir,
								},
							},
						},
					},
				},
			},
		},
	}, nil
}
