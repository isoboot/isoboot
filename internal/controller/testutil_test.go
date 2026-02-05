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
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	isobootv1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
)

const (
	testName      = "test-bootsource"
	testNamespace = "default"
)

// newTestBootSource creates a BootSource for testing with kernel+initrd
func newTestBootSource() *isobootv1alpha1.BootSource {
	return &isobootv1alpha1.BootSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testName,
			Namespace: testNamespace,
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

// newTestDownloadJob creates a download Job for testing with optional conditions.
func newTestDownloadJob(conditions ...batchv1.JobCondition) *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testName + "-download",
			Namespace: testNamespace,
		},
		Status: batchv1.JobStatus{
			Conditions: conditions,
		},
	}
}

// newJobCondition creates a batchv1.JobCondition for testing.
func newJobCondition(condType batchv1.JobConditionType, status corev1.ConditionStatus) batchv1.JobCondition {
	return batchv1.JobCondition{
		Type:   condType,
		Status: status,
	}
}

// newTestBootSourceISO creates a BootSource for testing with ISO
func newTestBootSourceISO() *isobootv1alpha1.BootSource {
	return &isobootv1alpha1.BootSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testName,
			Namespace: testNamespace,
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
