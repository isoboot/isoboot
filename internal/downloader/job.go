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
	"fmt"
	"path"
	"path/filepath"
	"text/template"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	isobootv1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
)

// JobNameSuffix is appended to the BootSource name to form the download Job name.
const JobNameSuffix = "-download"

//go:embed download.sh.tmpl
var scriptTemplate string

var scriptTmpl = template.Must(template.New("download").Parse(scriptTemplate))

// fileItem is a single downloadable file passed to the shell template.
type fileItem struct {
	Name        string // "kernel", "initrd", "firmware", "iso"
	URL         string // binary download URL
	ShasumURL   string // shasum file URL (may be empty)
	URLBasename string // final path component of URL, e.g. "mini.iso"
	Dest        string // absolute destination path
}

// isoData holds ISO-specific extraction paths for the shell template.
type isoData struct {
	ISOPath          string
	KernelPath       string
	KernelBasename   string // e.g. "linux"
	InitrdPath       string
	InitrdBasename   string // e.g. "initrd.gz"
	FirmwarePath     string // empty if no firmware inside ISO
	FirmwareBasename string // e.g. "firmware.cpio.gz"
}

// firmwareConcat holds paths for non-ISO firmware concatenation.
type firmwareConcat struct {
	InitrdBasename   string
	FirmwareBasename string
}

type templateData struct {
	Dir            string
	Files          []fileItem
	ISO            *isoData
	FirmwareConcat *firmwareConcat
}

// JobBuilder builds download Jobs for a BootSource.
type JobBuilder struct {
	BaseDir string
	Image   string
}

// NewJobBuilder returns a JobBuilder that creates Jobs downloading into baseDir.
func NewJobBuilder(baseDir, image string) *JobBuilder {
	return &JobBuilder{BaseDir: baseDir, Image: image}
}

// Build creates a Kubernetes Job that downloads, verifies, and (for ISOs)
// extracts all files for a BootSource.
func (b *JobBuilder) Build(bootSource *isobootv1alpha1.BootSource) (*batchv1.Job, error) {
	dir := filepath.Join(b.BaseDir, bootSource.Namespace, bootSource.Name)
	spec := bootSource.Spec

	var files []fileItem
	if spec.Kernel != nil {
		files = append(files, newFileItem("kernel", spec.Kernel.URL, dir))
	}
	if spec.Initrd != nil {
		files = append(files, newFileItem("initrd", spec.Initrd.URL, dir))
	}
	if spec.Firmware != nil {
		files = append(files, newFileItem("firmware", spec.Firmware.URL, dir))
	}
	if spec.ISO != nil {
		basename := path.Base(spec.ISO.URL.Binary)
		files = append(files, fileItem{
			Name:        "iso",
			URL:         spec.ISO.URL.Binary,
			ShasumURL:   spec.ISO.URL.Shasum,
			URLBasename: basename,
			Dest:        filepath.Join(dir, "iso", basename),
		})
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("no downloadable files: spec must include kernel+initrd or iso")
	}

	data := templateData{Dir: dir, Files: files}
	if spec.ISO != nil {
		iso := &isoData{
			ISOPath:        filepath.Join(dir, "iso", path.Base(spec.ISO.URL.Binary)),
			KernelPath:     spec.ISO.Path.Kernel,
			KernelBasename: path.Base(spec.ISO.Path.Kernel),
			InitrdPath:     spec.ISO.Path.Initrd,
			InitrdBasename: path.Base(spec.ISO.Path.Initrd),
			FirmwarePath:   spec.ISO.Path.Firmware,
		}
		if spec.ISO.Path.Firmware != "" {
			iso.FirmwareBasename = path.Base(spec.ISO.Path.Firmware)
		}
		data.ISO = iso
	}
	if spec.ISO == nil && spec.Initrd != nil && spec.Firmware != nil {
		data.FirmwareConcat = &firmwareConcat{
			InitrdBasename:   path.Base(spec.Initrd.URL.Binary),
			FirmwareBasename: path.Base(spec.Firmware.URL.Binary),
		}
	}

	var buf bytes.Buffer
	if err := scriptTmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("rendering download script: %w", err)
	}

	var secCtx *corev1.SecurityContext
	if spec.ISO != nil {
		secCtx = &corev1.SecurityContext{
			Capabilities: &corev1.Capabilities{
				Add: []corev1.Capability{"SYS_ADMIN"},
			},
		}
	}

	backoffLimit := int32(3)
	ttl := int32(600)
	hostPathType := corev1.HostPathDirectoryOrCreate
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      bootSource.Name + JobNameSuffix,
			Namespace: bootSource.Namespace,
			Labels: map[string]string{
				"isoboot.github.io/bootsource": bootSource.Name,
				"app.kubernetes.io/component":  "downloader",
				"app.kubernetes.io/managed-by": "isoboot",
			},
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(bootSource, isobootv1alpha1.GroupVersion.WithKind("BootSource")),
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoffLimit,
			TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:            "download",
							Image:           b.Image,
							Command:         []string{"/bin/sh", "-c", buf.String()},
							SecurityContext: secCtx,
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "data",
									MountPath: b.BaseDir,
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "data",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: b.BaseDir,
									Type: &hostPathType,
								},
							},
						},
					},
				},
			},
		},
	}, nil
}

func newFileItem(name string, url isobootv1alpha1.URLSource, dir string) fileItem {
	basename := path.Base(url.Binary)
	return fileItem{
		Name:        name,
		URL:         url.Binary,
		ShasumURL:   url.Shasum,
		URLBasename: basename,
		Dest:        filepath.Join(dir, name, basename),
	}
}
