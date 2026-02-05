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
// +kubebuilder:validation:XValidation:rule="size(self.binary) > 0",message="binary URL is required"
// +kubebuilder:validation:XValidation:rule="size(self.binary) == 0 || self.binary.startsWith('https://')",message="binary URL must use https"
// +kubebuilder:validation:XValidation:rule="size(self.shasum) == 0 || self.shasum.startsWith('https://')",message="shasum URL must use https"
// +kubebuilder:validation:XValidation:rule="size(self.binary) == 0 || size(self.shasum) == 0 || !self.binary.startsWith('https://') || !self.shasum.startsWith('https://') || self.binary.split('://')[1].split('/')[0] == self.shasum.split('://')[1].split('/')[0]",message="binary and shasum URLs must be on the same server"
type URLSource struct {
	// Binary is the URL to download the file from
	Binary string `json:"binary"`
	// Shasum is the URL to download the checksum file from.
	// When set, the downloaded file is verified against this checksum.
	// When omitted, no checksum verification is performed.
	// +optional
	Shasum string `json:"shasum,omitempty"`
}

// PathSource defines paths inside an ISO image
// +kubebuilder:validation:XValidation:rule="!self.kernel.startsWith('/')",message="kernel path must be relative (cannot start with /)"
// +kubebuilder:validation:XValidation:rule="!self.kernel.contains('/../') && !self.kernel.startsWith('../') && !self.kernel.endsWith('/..') && self.kernel != '..'",message="kernel path cannot contain .. components"
// +kubebuilder:validation:XValidation:rule="!self.initrd.startsWith('/')",message="initrd path must be relative (cannot start with /)"
// +kubebuilder:validation:XValidation:rule="!self.initrd.contains('/../') && !self.initrd.startsWith('../') && !self.initrd.endsWith('/..') && self.initrd != '..'",message="initrd path cannot contain .. components"
// +kubebuilder:validation:XValidation:rule="size(self.firmware) == 0 || !self.firmware.startsWith('/')",message="firmware path must be relative (cannot start with /)"
// +kubebuilder:validation:XValidation:rule="size(self.firmware) == 0 || (!self.firmware.contains('/../') && !self.firmware.startsWith('../') && !self.firmware.endsWith('/..') && self.firmware != '..')",message="firmware path cannot contain .. components"
type PathSource struct {
	// Kernel is the path to the kernel inside the ISO
	Kernel string `json:"kernel"`
	// Initrd is the path to the initrd inside the ISO
	Initrd string `json:"initrd"`
	// Firmware is the path to the firmware cpio.gz inside the ISO.
	// When set, a combined initrd is produced by concatenating initrd + firmware
	// (the standard Debian netboot firmware pattern).
	// +optional
	Firmware string `json:"firmware,omitempty"`
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
// +kubebuilder:validation:XValidation:rule="size(self.path.kernel) > 0",message="iso requires path.kernel to be specified"
// +kubebuilder:validation:XValidation:rule="size(self.path.initrd) > 0",message="iso requires path.initrd to be specified"
type ISOSource struct {
	// URL contains the download URLs for the ISO
	URL URLSource `json:"url"`
	// Path contains the paths to kernel/initrd inside the ISO
	Path PathSource `json:"path"`
}

// BootSourceSpec defines the desired state of BootSource
// +kubebuilder:validation:XValidation:rule="(has(self.kernel) && has(self.initrd)) || has(self.iso)",message="must specify either (kernel and initrd) or iso"
// +kubebuilder:validation:XValidation:rule="!((has(self.kernel) || has(self.initrd)) && has(self.iso))",message="cannot specify both (kernel or initrd) and iso"
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

// BootSourcePhase represents the current phase of a BootSource
// +kubebuilder:validation:Enum=Pending;Downloading;Ready;Failed
type BootSourcePhase string

const (
	// PhasePending indicates the BootSource is waiting to be processed
	PhasePending BootSourcePhase = "Pending"
	// PhaseDownloading indicates the BootSource is downloading, verifying, and extracting files
	PhaseDownloading BootSourcePhase = "Downloading"
	// PhaseReady indicates the BootSource is ready for use
	PhaseReady BootSourcePhase = "Ready"
	// PhaseFailed indicates the BootSource processing failed
	PhaseFailed BootSourcePhase = "Failed"
)

// BootSourceStatus defines the observed state of BootSource
type BootSourceStatus struct {
	// Phase represents the current phase of the BootSource
	// +optional
	Phase BootSourcePhase `json:"phase,omitempty"`
	// Conditions represent the latest available observations of an object's state
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:validation:XValidation:rule="size(self.metadata.name) <= 200",message="name must be at most 200 characters"

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
