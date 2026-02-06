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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	isobootv1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
)

// testDownloadImage is the container image used by download Jobs in tests.
const testDownloadImage = "alpine:3.23"

// newTestBootSource creates a BootSource for testing with kernel+initrd
func newTestBootSource(name, namespace string) *isobootv1alpha1.BootSource {
	return &isobootv1alpha1.BootSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: isobootv1alpha1.BootSourceSpec{
			Kernel: &isobootv1alpha1.KernelSource{
				URL: isobootv1alpha1.URLSource{
					Binary: "https://example.com/vmlinuz",
					Shasum: "https://example.com/vmlinuz.sha256",
				},
			},
			Initrd: &isobootv1alpha1.InitrdSource{
				URL: isobootv1alpha1.URLSource{
					Binary: "https://example.com/initrd.img",
					Shasum: "https://example.com/initrd.img.sha256",
				},
			},
		},
	}
}

// newTestBootSourceWithFirmware creates a BootSource for testing with kernel+initrd+firmware
func newTestBootSourceWithFirmware(name, namespace string) *isobootv1alpha1.BootSource {
	return &isobootv1alpha1.BootSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: isobootv1alpha1.BootSourceSpec{
			Kernel: &isobootv1alpha1.KernelSource{
				URL: isobootv1alpha1.URLSource{
					Binary: "https://example.com/vmlinuz",
					Shasum: "https://example.com/vmlinuz.sha256",
				},
			},
			Initrd: &isobootv1alpha1.InitrdSource{
				URL: isobootv1alpha1.URLSource{
					Binary: "https://example.com/initrd.img",
					Shasum: "https://example.com/initrd.img.sha256",
				},
			},
			Firmware: &isobootv1alpha1.FirmwareSource{
				URL: isobootv1alpha1.URLSource{
					Binary: "https://example.com/firmware.cpio.gz",
					Shasum: "https://example.com/firmware.cpio.gz.sha256",
				},
			},
		},
	}
}

// newTestBootSourceISO creates a BootSource for testing with ISO
func newTestBootSourceISO(name, namespace string) *isobootv1alpha1.BootSource {
	return &isobootv1alpha1.BootSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: isobootv1alpha1.BootSourceSpec{
			ISO: &isobootv1alpha1.ISOSource{
				URL: isobootv1alpha1.URLSource{
					Binary: "https://example.com/boot.iso",
					Shasum: "https://example.com/boot.iso.sha256",
				},
				Path: isobootv1alpha1.PathSource{
					Kernel: "/boot/vmlinuz",
					Initrd: "/boot/initrd.img",
				},
			},
		},
	}
}
