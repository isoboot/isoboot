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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// URLSource defines URLs for a downloadable resource and its checksum file
type URLSource struct {
	// Binary is the URL to download the file from
	Binary string `json:"binary"`
	// Shasum is the URL to download the checksum file from
	Shasum string `json:"shasum"`
}

// PathSource defines paths inside an ISO image
type PathSource struct {
	// Kernel is the path to the kernel inside the ISO
	Kernel string `json:"kernel"`
	// Initrd is the path to the initrd inside the ISO
	Initrd string `json:"initrd"`
}

// KernelSource defines a kernel binary source
type KernelSource struct {
	// URL contains the download URLs for the kernel
	URL URLSource `json:"url"`
}

// InitrdSource defines an initrd binary source
type InitrdSource struct {
	// URL contains the download URLs for the initrd
	URL URLSource `json:"url"`
}

// FirmwareSource defines a firmware binary source
type FirmwareSource struct {
	// URL contains the download URLs for the firmware
	URL URLSource `json:"url"`
}

// ISOSource defines an ISO image with paths to kernel/initrd inside
type ISOSource struct {
	// URL contains the download URLs for the ISO
	URL URLSource `json:"url"`
	// Path contains the paths to kernel/initrd inside the ISO
	Path PathSource `json:"path"`
}

// BootSourceSpec defines the desired state of BootSource
type BootSourceSpec struct {
	// Kernel specifies the kernel binary source
	// +optional
	Kernel *KernelSource `json:"kernel,omitempty"`
	// Initrd specifies the initrd binary source
	// +optional
	Initrd *InitrdSource `json:"initrd,omitempty"`
	// Firmware specifies the firmware binary source
	// +optional
	Firmware *FirmwareSource `json:"firmware,omitempty"`
	// ISO specifies an ISO image containing kernel and initrd
	// +optional
	ISO *ISOSource `json:"iso,omitempty"`
}

// BootSourceStatus defines the observed state of BootSource
type BootSourceStatus struct {
	// Conditions represent the latest available observations of an object's state
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// BootSource is the Schema for the bootsources API
type BootSource struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BootSourceSpec   `json:"spec,omitempty"`
	Status BootSourceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// BootSourceList contains a list of BootSource
type BootSourceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BootSource `json:"items"`
}

func init() {
	SchemeBuilder.Register(&BootSource{}, &BootSourceList{})
}
