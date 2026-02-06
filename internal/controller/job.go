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
	_ "embed"
	"encoding/base64"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
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
	// URL is the raw URL to download from.
	URL string
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
				URL:        raw,
				OutputPath: outPath,
			})
		}
	}
	return tasks, nil
}

// scriptTask holds per-task data for the download script template.
type scriptTask struct {
	Index      int
	URL        string
	OutputDir  string
	OutputPath string
}

// isoExtractInfo carries ISO extraction parameters into the script template.
type isoExtractInfo struct {
	ISOPath      string // host path to the downloaded ISO file
	KernelSrc    string // path inside the ISO (e.g. "/casper/vmlinuz")
	KernelDstDir string // directory for the extracted kernel
	KernelDst    string // full path for the extracted kernel
	InitrdSrc    string // path inside the ISO (e.g. "/casper/initrd")
	InitrdDstDir string // directory for the extracted initrd
	InitrdDst    string // full path for the extracted initrd
}

// firmwareBuildInfo carries firmware concatenation parameters into the script template.
type firmwareBuildInfo struct {
	FirmwarePath string // host path to downloaded firmware
	InitrdPath   string // host path to initrd (downloaded or extracted)
	OutputDir    string // directory for combined initrd
	OutputPath   string // full path for combined initrd
}

// scriptData is the top-level data structure passed to the download script template.
type scriptData struct {
	Tasks    []scriptTask
	ISO      *isoExtractInfo    // nil for non-ISO sources
	Firmware *firmwareBuildInfo // nil when no firmware
}

// relativeURLPath computes the relative path of binaryURL from the directory
// of shasumURL. For example, given binary ".../images/netboot/.../linux" and
// shasum ".../images/SHA256SUMS", it returns "netboot/.../linux".
func relativeURLPath(binaryURL, shasumURL string) string {
	bu, err := url.Parse(binaryURL)
	if err != nil {
		return filepath.Base(binaryURL)
	}
	su, err := url.Parse(shasumURL)
	if err != nil {
		return filepath.Base(binaryURL)
	}
	lastSlash := strings.LastIndex(su.Path, "/")
	if lastSlash == -1 {
		return filepath.Base(bu.Path)
	}
	shasumDir := su.Path[:lastSlash+1]
	rel := strings.TrimPrefix(bu.Path, shasumDir)
	return strings.TrimPrefix(rel, "./")
}

// templateFuncs contains reusable template functions.
var templateFuncs = template.FuncMap{
	"b64enc": func(s string) string {
		return base64.StdEncoding.EncodeToString([]byte(s))
	},
	"mod": func(a, b int) int {
		return a % b
	},
	"sub": func(a, b int) int {
		return a - b
	},
	"relpath": relativeURLPath,
}

// downloadScriptRaw is the raw shell template embedded from download.sh.tmpl.
// The b64enc function encodes URLs so they never enter a shell-interpreted
// context. Base64 alphabet [A-Za-z0-9+/=] cannot break single quotes.
// OutputDir and OutputPath come from DownloadPath which only produces
// filesystem-safe characters.
//
//go:embed download.sh.tmpl
var downloadScriptRaw string

var downloadScriptTmpl = template.Must(template.New("download").Funcs(templateFuncs).Parse(downloadScriptRaw))

// buildDownloadScript generates a shell script that downloads every task.
// URLs are base64-encoded and decoded to a temporary file at runtime, so they
// never enter a shell-interpreted context.
//
// Tasks must arrive in binary/shasum pairs: even indices are binaries,
// odd indices are the corresponding shasum files. This invariant is
// maintained by collectDownloadTasks which iterates {Binary, Shasum}
// for each resource.
func buildDownloadScript(tasks []downloadTask, iso *isoExtractInfo, fw *firmwareBuildInfo) string {
	st := make([]scriptTask, len(tasks))
	for i, t := range tasks {
		st[i] = scriptTask{
			Index:      i,
			URL:        t.URL,
			OutputDir:  filepath.Dir(t.OutputPath),
			OutputPath: t.OutputPath,
		}
	}
	data := scriptData{Tasks: st, ISO: iso, Firmware: fw}
	var buf bytes.Buffer
	if err := downloadScriptTmpl.Execute(&buf, data); err != nil {
		// Template is static and data is pre-validated; this should never happen.
		panic(fmt.Sprintf("executing download script template: %v", err))
	}
	return buf.String()
}

// downloadJobName returns the Job name for a given BootSource name.
// BootSource names are limited to 50 characters by CEL validation,
// so the result is at most 59 characters (well within the 63-char
// DNS label limit).
func downloadJobName(bootSourceName string) string {
	return bootSourceName + "-download"
}

// buildDownloadJob constructs a batch/v1 Job that downloads all resources for
// the given BootSource.
func buildDownloadJob(bootSource *isobootv1alpha1.BootSource, scheme *runtime.Scheme, baseDir, downloadImage string) (*batchv1.Job, error) {
	tasks, err := collectDownloadTasks(bootSource.Spec, baseDir, bootSource.Namespace, bootSource.Name)
	if err != nil {
		return nil, err
	}

	volumeDir := filepath.Join(baseDir, bootSource.Namespace, bootSource.Name)

	// Compute ISO extraction info when an ISO source is configured.
	var iso *isoExtractInfo
	if bootSource.Spec.ISO != nil {
		isoPath, err := DownloadPath(baseDir, bootSource.Namespace, bootSource.Name, ResourceISO, bootSource.Spec.ISO.URL.Binary)
		if err != nil {
			return nil, fmt.Errorf("computing ISO download path: %w", err)
		}
		kernelDst := filepath.Join(volumeDir, string(ResourceKernel), filepath.Base(bootSource.Spec.ISO.Path.Kernel))
		initrdDst := filepath.Join(volumeDir, string(ResourceInitrd), filepath.Base(bootSource.Spec.ISO.Path.Initrd))
		iso = &isoExtractInfo{
			ISOPath:      isoPath,
			KernelSrc:    bootSource.Spec.ISO.Path.Kernel,
			KernelDstDir: filepath.Dir(kernelDst),
			KernelDst:    kernelDst,
			InitrdSrc:    bootSource.Spec.ISO.Path.Initrd,
			InitrdDstDir: filepath.Dir(initrdDst),
			InitrdDst:    initrdDst,
		}
	}

	// Compute firmware concatenation info when firmware is configured.
	var fw *firmwareBuildInfo
	if bootSource.Spec.Firmware != nil {
		fwPath, err := DownloadPath(baseDir, bootSource.Namespace, bootSource.Name, ResourceFirmware, bootSource.Spec.Firmware.URL.Binary)
		if err != nil {
			return nil, fmt.Errorf("computing firmware download path: %w", err)
		}

		var initrdPath string
		if iso != nil {
			initrdPath = iso.InitrdDst
		} else if bootSource.Spec.Initrd != nil {
			initrdPath, err = DownloadPath(baseDir, bootSource.Namespace, bootSource.Name, ResourceInitrd, bootSource.Spec.Initrd.URL.Binary)
			if err != nil {
				return nil, fmt.Errorf("computing initrd download path: %w", err)
			}
		}

		if initrdPath != "" {
			outputDir := filepath.Join(filepath.Dir(initrdPath), "with-firmware")
			fw = &firmwareBuildInfo{
				FirmwarePath: fwPath,
				InitrdPath:   initrdPath,
				OutputDir:    outputDir,
				OutputPath:   filepath.Join(outputDir, filepath.Base(initrdPath)),
			}
		}
	}

	script := buildDownloadScript(tasks, iso, fw)

	jobName := downloadJobName(bootSource.Name)

	dirOrCreate := corev1.HostPathDirectoryOrCreate
	var backoffLimit int32 = 2

	container := corev1.Container{
		Name:    "download",
		Image:   downloadImage,
		Command: []string{"/bin/sh", "-c", script},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "data",
				MountPath: volumeDir,
			},
		},
	}
	if iso != nil {
		container.SecurityContext = &corev1.SecurityContext{
			Capabilities: &corev1.Capabilities{
				Add: []corev1.Capability{"SYS_ADMIN"},
			},
		}
	}

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
					Containers:    []corev1.Container{container},
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
