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
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	isobootv1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
)

func TestExtractDownloads_KernelInitrd(t *testing.T) {
	spec := isobootv1alpha1.BootSourceSpec{
		Kernel: &isobootv1alpha1.KernelSource{
			URL: isobootv1alpha1.URLSource{Binary: "https://example.com/vmlinuz"},
		},
		Initrd: &isobootv1alpha1.InitrdSource{
			URL: isobootv1alpha1.URLSource{Binary: "https://example.com/initrd.img"},
		},
	}

	items := ExtractDownloads(spec, "/data", "default", "myboot")
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].URL != "https://example.com/vmlinuz" {
		t.Errorf("expected kernel URL, got %s", items[0].URL)
	}
	if items[0].Dest != "/data/default/myboot/kernel" {
		t.Errorf("expected kernel dest /data/default/myboot/kernel, got %s", items[0].Dest)
	}
	if items[1].URL != "https://example.com/initrd.img" {
		t.Errorf("expected initrd URL, got %s", items[1].URL)
	}
	if items[1].Dest != "/data/default/myboot/initrd" {
		t.Errorf("expected initrd dest /data/default/myboot/initrd, got %s", items[1].Dest)
	}
}

func TestExtractDownloads_ISO(t *testing.T) {
	spec := isobootv1alpha1.BootSourceSpec{
		ISO: &isobootv1alpha1.ISOSource{
			URL: isobootv1alpha1.URLSource{Binary: "https://example.com/boot.iso"},
		},
	}

	items := ExtractDownloads(spec, "/data", "ns1", "isoboot")
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].URL != "https://example.com/boot.iso" {
		t.Errorf("expected ISO URL, got %s", items[0].URL)
	}
	if items[0].Dest != "/data/ns1/isoboot/iso" {
		t.Errorf("expected iso dest /data/ns1/isoboot/iso, got %s", items[0].Dest)
	}
}

func TestExtractDownloads_WithFirmware(t *testing.T) {
	spec := isobootv1alpha1.BootSourceSpec{
		Kernel: &isobootv1alpha1.KernelSource{
			URL: isobootv1alpha1.URLSource{Binary: "https://example.com/vmlinuz"},
		},
		Initrd: &isobootv1alpha1.InitrdSource{
			URL: isobootv1alpha1.URLSource{Binary: "https://example.com/initrd.img"},
		},
		Firmware: &isobootv1alpha1.FirmwareSource{
			URL: isobootv1alpha1.URLSource{Binary: "https://example.com/firmware.bin"},
		},
	}

	items := ExtractDownloads(spec, "/data", "default", "myboot")
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
	if items[2].Dest != "/data/default/myboot/firmware" {
		t.Errorf("expected firmware dest, got %s", items[2].Dest)
	}
}

func TestBuildJob(t *testing.T) {
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
		t.Fatalf("BuildJob failed: %v", err)
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
	if len(podSpec.Containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(podSpec.Containers))
	}
	container := podSpec.Containers[0]
	if container.Image != "curlimages/curl" {
		t.Errorf("expected image curlimages/curl, got %s", container.Image)
	}
	if len(container.VolumeMounts) != 1 {
		t.Fatalf("expected 1 volume mount, got %d", len(container.VolumeMounts))
	}
	if container.VolumeMounts[0].MountPath != "/var/lib/isoboot" {
		t.Errorf("expected mount path /var/lib/isoboot, got %s", container.VolumeMounts[0].MountPath)
	}
	if len(podSpec.Volumes) != 1 {
		t.Fatalf("expected 1 volume, got %d", len(podSpec.Volumes))
	}
	if podSpec.Volumes[0].HostPath.Path != "/var/lib/isoboot" {
		t.Errorf("expected host path /var/lib/isoboot, got %s", podSpec.Volumes[0].HostPath.Path)
	}
}
