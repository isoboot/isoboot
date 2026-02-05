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

func TestBuild_KernelInitrd(t *testing.T) {
	bs := &isobootv1alpha1.BootSource{
		ObjectMeta: metav1.ObjectMeta{Name: "myboot", Namespace: "default", UID: "uid"},
		Spec: isobootv1alpha1.BootSourceSpec{
			Kernel: &isobootv1alpha1.KernelSource{
				URL: isobootv1alpha1.URLSource{
					Binary: "https://example.com/vmlinuz",
					Shasum: "https://example.com/SHA256SUMS",
				},
			},
			Initrd: &isobootv1alpha1.InitrdSource{
				URL: isobootv1alpha1.URLSource{
					Binary: "https://example.com/initrd.img",
					Shasum: "https://example.com/SHA256SUMS",
				},
			},
		},
	}

	job, err := NewJobBuilder("/var/lib/isoboot").Build(bs)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	// Job metadata
	if job.Name != "myboot-download" {
		t.Errorf("name: got %s, want myboot-download", job.Name)
	}
	if job.Labels["isoboot.github.io/bootsource"] != "myboot" {
		t.Errorf("label: got %s", job.Labels["isoboot.github.io/bootsource"])
	}
	if len(job.OwnerReferences) != 1 || job.OwnerReferences[0].Name != "myboot" {
		t.Error("owner reference missing or wrong")
	}

	// Always alpine
	container := job.Spec.Template.Spec.Containers[0]
	if container.Image != "alpine" {
		t.Errorf("image: got %s, want alpine", container.Image)
	}

	// No privileged for non-ISO
	if container.SecurityContext != nil {
		t.Error("non-ISO job should not be privileged")
	}

	script := container.Command[2]

	// Download section
	assertContains(t, script, "Checking kernel")
	assertContains(t, script, "https://example.com/vmlinuz")
	assertContains(t, script, "Checking initrd")
	assertContains(t, script, "https://example.com/initrd.img")

	// Verify section
	assertContains(t, script, "Verifying kernel")
	assertContains(t, script, "https://example.com/SHA256SUMS")
	assertContains(t, script, "sha256sum")
	assertContains(t, script, `BASENAME="vmlinuz"`)
	assertContains(t, script, `BASENAME="initrd.img"`)

	// No ISO extraction
	assertNotContains(t, script, "mount -o ro,loop")
}

func TestBuild_ISO(t *testing.T) {
	bs := &isobootv1alpha1.BootSource{
		ObjectMeta: metav1.ObjectMeta{Name: "isoboot", Namespace: "ns1", UID: "uid"},
		Spec: isobootv1alpha1.BootSourceSpec{
			ISO: &isobootv1alpha1.ISOSource{
				URL: isobootv1alpha1.URLSource{
					Binary: "https://example.com/boot.iso",
					Shasum: "https://example.com/SHA256SUMS",
				},
				Path: isobootv1alpha1.PathSource{Kernel: "/linux", Initrd: "/initrd.gz"},
			},
		},
	}

	job, err := NewJobBuilder("/data").Build(bs)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	container := job.Spec.Template.Spec.Containers[0]
	if container.Image != "alpine" {
		t.Errorf("image: got %s, want alpine", container.Image)
	}
	if container.SecurityContext == nil || !*container.SecurityContext.Privileged {
		t.Error("ISO job should be privileged")
	}

	script := container.Command[2]

	// Download ISO
	assertContains(t, script, "https://example.com/boot.iso")

	// Verify
	assertContains(t, script, "Verifying iso")
	assertContains(t, script, `BASENAME="boot.iso"`)

	// Extract into subdirectories
	assertContains(t, script, "mount -o ro,loop")
	assertContains(t, script, "kernel/linux")
	assertContains(t, script, "initrd/initrd.gz")

	// ISO downloaded into iso/ subdirectory
	assertContains(t, script, "iso/boot.iso")

	// No firmware -> no concatenation
	assertNotContains(t, script, "with-firmware")
}

func TestBuild_ISOWithFirmware(t *testing.T) {
	bs := &isobootv1alpha1.BootSource{
		ObjectMeta: metav1.ObjectMeta{Name: "debian", Namespace: "default", UID: "uid"},
		Spec: isobootv1alpha1.BootSourceSpec{
			ISO: &isobootv1alpha1.ISOSource{
				URL: isobootv1alpha1.URLSource{
					Binary: "https://example.com/mini.iso",
					Shasum: "https://example.com/SHA256SUMS",
				},
				Path: isobootv1alpha1.PathSource{
					Kernel:   "linux",
					Initrd:   "initrd.gz",
					Firmware: "firmware.cpio.gz",
				},
			},
		},
	}

	job, err := NewJobBuilder("/data").Build(bs)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	script := job.Spec.Template.Spec.Containers[0].Command[2]

	// Extract firmware into firmware/ subdirectory
	assertContains(t, script, "firmware/firmware.cpio.gz")

	// Original initrd in initrd/initrd.gz
	assertContains(t, script, "initrd/initrd.gz")

	// Concatenated initrd+firmware in initrd/with-firmware/initrd.gz
	assertContains(t, script, "initrd/with-firmware/initrd.gz")
	assertContains(t, script, `cat "$DIR/initrd/initrd.gz" "$DIR/firmware/firmware.cpio.gz"`)
}

func TestBuild_NoShasumSkipsVerify(t *testing.T) {
	bs := &isobootv1alpha1.BootSource{
		ObjectMeta: metav1.ObjectMeta{Name: "noshasum", Namespace: "default", UID: "uid"},
		Spec: isobootv1alpha1.BootSourceSpec{
			Kernel: &isobootv1alpha1.KernelSource{
				URL: isobootv1alpha1.URLSource{Binary: "https://example.com/vmlinuz"},
			},
			Initrd: &isobootv1alpha1.InitrdSource{
				URL: isobootv1alpha1.URLSource{Binary: "https://example.com/initrd.img"},
			},
		},
	}

	job, err := NewJobBuilder("/data").Build(bs)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	script := job.Spec.Template.Spec.Containers[0].Command[2]
	assertNotContains(t, script, "Verifying")
	assertNotContains(t, script, "sha256sum")
}

func TestBuild_WithFirmware(t *testing.T) {
	bs := &isobootv1alpha1.BootSource{
		ObjectMeta: metav1.ObjectMeta{Name: "fw", Namespace: "default", UID: "uid"},
		Spec: isobootv1alpha1.BootSourceSpec{
			Kernel: &isobootv1alpha1.KernelSource{
				URL: isobootv1alpha1.URLSource{Binary: "https://example.com/vmlinuz", Shasum: "https://example.com/SHA256SUMS"},
			},
			Initrd: &isobootv1alpha1.InitrdSource{
				URL: isobootv1alpha1.URLSource{Binary: "https://example.com/initrd.img", Shasum: "https://example.com/SHA256SUMS"},
			},
			Firmware: &isobootv1alpha1.FirmwareSource{
				URL: isobootv1alpha1.URLSource{Binary: "https://example.com/firmware.bin", Shasum: "https://example.com/SHA256SUMS"},
			},
		},
	}

	job, err := NewJobBuilder("/data").Build(bs)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	script := job.Spec.Template.Spec.Containers[0].Command[2]
	assertContains(t, script, "Checking firmware")
	assertContains(t, script, "https://example.com/firmware.bin")
	assertContains(t, script, "Verifying firmware")
	assertContains(t, script, `BASENAME="firmware.bin"`)
}

func assertContains(t *testing.T, s, sub string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Errorf("script should contain %q", sub)
	}
}

func assertNotContains(t *testing.T, s, sub string) {
	t.Helper()
	if strings.Contains(s, sub) {
		t.Errorf("script should NOT contain %q", sub)
	}
}
