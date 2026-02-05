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
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	isobootv1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
)

func TestBuildJob_KernelInitrd(t *testing.T) {
	bs := &isobootv1alpha1.BootSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "myboot",
			Namespace: "default",
			UID:       "test-uid",
		},
		Spec: isobootv1alpha1.BootSourceSpec{
			Kernel: &isobootv1alpha1.KernelSource{
				URL: isobootv1alpha1.URLSource{Binary: "https://example.com/vmlinuz"},
			},
			Initrd: &isobootv1alpha1.InitrdSource{
				URL: isobootv1alpha1.URLSource{Binary: "https://example.com/initrd.img"},
			},
		},
	}

	builder := NewJobBuilder("/var/lib/isoboot")
	job, err := builder.Build(bs)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	if job.Name != "myboot-download" {
		t.Errorf("expected job name myboot-download, got %s", job.Name)
	}
	if job.Namespace != "default" {
		t.Errorf("expected namespace default, got %s", job.Namespace)
	}
	if job.Labels["isoboot.github.io/bootsource"] != "myboot" {
		t.Errorf("expected bootsource label myboot, got %s", job.Labels["isoboot.github.io/bootsource"])
	}
	if job.Labels["app.kubernetes.io/component"] != "downloader" {
		t.Errorf("expected component label downloader, got %s", job.Labels["app.kubernetes.io/component"])
	}
	if len(job.OwnerReferences) != 1 {
		t.Fatalf("expected 1 owner reference, got %d", len(job.OwnerReferences))
	}
	if job.OwnerReferences[0].Name != "myboot" {
		t.Errorf("expected owner ref name myboot, got %s", job.OwnerReferences[0].Name)
	}
	if *job.Spec.BackoffLimit != 3 {
		t.Errorf("expected backoff limit 3, got %d", *job.Spec.BackoffLimit)
	}

	podSpec := job.Spec.Template.Spec
	if podSpec.RestartPolicy != "Never" {
		t.Errorf("expected restart policy Never, got %s", podSpec.RestartPolicy)
	}
	container := podSpec.Containers[0]
	if container.Image != "curlimages/curl" {
		t.Errorf("expected image curlimages/curl, got %s", container.Image)
	}
	if container.SecurityContext != nil {
		t.Error("expected no security context for non-ISO job")
	}

	script := container.Command[2]
	if !strings.Contains(script, "https://example.com/vmlinuz") {
		t.Error("script should contain kernel URL")
	}
	if !strings.Contains(script, "https://example.com/initrd.img") {
		t.Error("script should contain initrd URL")
	}
	if strings.Contains(script, "mount") {
		t.Error("non-ISO script should not contain mount")
	}
}

func TestBuildJob_ISO(t *testing.T) {
	bs := &isobootv1alpha1.BootSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "isoboot",
			Namespace: "ns1",
			UID:       "test-uid",
		},
		Spec: isobootv1alpha1.BootSourceSpec{
			ISO: &isobootv1alpha1.ISOSource{
				URL:  isobootv1alpha1.URLSource{Binary: "https://example.com/boot.iso"},
				Path: isobootv1alpha1.PathSource{Kernel: "/linux", Initrd: "/initrd.gz"},
			},
		},
	}

	builder := NewJobBuilder("/data")
	job, err := builder.Build(bs)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	container := job.Spec.Template.Spec.Containers[0]
	if container.Image != "alpine" {
		t.Errorf("expected alpine image for ISO, got %s", container.Image)
	}
	if container.SecurityContext == nil || container.SecurityContext.Privileged == nil || !*container.SecurityContext.Privileged {
		t.Error("expected privileged security context for ISO job")
	}

	script := container.Command[2]
	if !strings.Contains(script, "https://example.com/boot.iso") {
		t.Error("script should contain ISO URL")
	}
	if !strings.Contains(script, "mount -o ro,loop") {
		t.Error("ISO script should contain mount command")
	}
	if !strings.Contains(script, "/linux") {
		t.Error("ISO script should contain kernel path")
	}
	if !strings.Contains(script, "/initrd.gz") {
		t.Error("ISO script should contain initrd path")
	}
}

func TestBuildJob_WithFirmware(t *testing.T) {
	bs := &isobootv1alpha1.BootSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "myboot",
			Namespace: "default",
			UID:       "test-uid",
		},
		Spec: isobootv1alpha1.BootSourceSpec{
			Kernel: &isobootv1alpha1.KernelSource{
				URL: isobootv1alpha1.URLSource{Binary: "https://example.com/vmlinuz"},
			},
			Initrd: &isobootv1alpha1.InitrdSource{
				URL: isobootv1alpha1.URLSource{Binary: "https://example.com/initrd.img"},
			},
			Firmware: &isobootv1alpha1.FirmwareSource{
				URL: isobootv1alpha1.URLSource{Binary: "https://example.com/firmware.bin"},
			},
		},
	}

	builder := NewJobBuilder("/data")
	job, err := builder.Build(bs)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	script := job.Spec.Template.Spec.Containers[0].Command[2]
	if !strings.Contains(script, "https://example.com/firmware.bin") {
		t.Error("script should contain firmware URL")
	}
}
