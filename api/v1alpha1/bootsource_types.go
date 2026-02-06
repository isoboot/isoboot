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
// +kubebuilder:validation:XValidation:rule="size(self.shasum) > 0",message="shasum URL is required"
// +kubebuilder:validation:XValidation:rule="size(self.binary) == 0 || self.binary.startsWith('https://')",message="binary URL must use https"
// +kubebuilder:validation:XValidation:rule="size(self.shasum) == 0 || self.shasum.startsWith('https://')",message="shasum URL must use https"
// +kubebuilder:validation:XValidation:rule="size(self.binary) == 0 || size(self.shasum) == 0 || !self.binary.startsWith('https://') || !self.shasum.startsWith('https://') || self.binary.split('://')[1].split('/')[0] == self.shasum.split('://')[1].split('/')[0]",message="binary and shasum URLs must be on the same server"
type URLSource struct {
	// Binary is the URL to download the file from
	Binary string `json:"binary"`
	// Shasum is the URL to download the checksum file from
	Shasum string `json:"shasum"`
}

// PathSource defines paths inside an ISO image
// +kubebuilder:validation:XValidation:rule="self.kernel.matches('^[a-zA-Z0-9/._-]+$')",message="kernel path contains invalid characters"
// +kubebuilder:validation:XValidation:rule="self.initrd.matches('^[a-zA-Z0-9/._-]+$')",message="initrd path contains invalid characters"
// +kubebuilder:validation:XValidation:rule="!self.kernel.contains('..')",message="kernel path must not contain path traversal (..)"
// +kubebuilder:validation:XValidation:rule="!self.initrd.contains('..')",message="initrd path must not contain path traversal (..)"
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
// +kubebuilder:validation:Enum=Pending;Downloading;Verifying;Extracting;Building;Ready;Failed;Corrupted
type BootSourcePhase string

const (
	// PhasePending indicates the BootSource is waiting to be processed
	PhasePending BootSourcePhase = "Pending"
	// PhaseDownloading indicates the BootSource is downloading files
	PhaseDownloading BootSourcePhase = "Downloading"
	// PhaseVerifying indicates the BootSource is verifying checksums
	PhaseVerifying BootSourcePhase = "Verifying"
	// PhaseExtracting indicates the BootSource is extracting files from ISO
	PhaseExtracting BootSourcePhase = "Extracting"
	// PhaseBuilding indicates the BootSource is building artifacts
	PhaseBuilding BootSourcePhase = "Building"
	// PhaseReady indicates the BootSource is ready for use
	PhaseReady BootSourcePhase = "Ready"
	// PhaseFailed indicates the BootSource processing failed
	PhaseFailed BootSourcePhase = "Failed"
	// PhaseCorrupted indicates the BootSource checksum verification failed
	PhaseCorrupted BootSourcePhase = "Corrupted"
)

// BootSourceStatus defines the observed state of BootSource
type BootSourceStatus struct {
	// Phase represents the current phase of the BootSource
	// +optional
	Phase BootSourcePhase `json:"phase,omitempty"`
	// Conditions represent the latest available observations of an object's state
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// DownloadJobName is the name of the batch/v1 Job performing downloads
	// +optional
	DownloadJobName string `json:"downloadJobName,omitempty"`
	// ArtifactPaths maps resource type (kernel, initrd, etc.) to host path
	// +optional
	ArtifactPaths map[string]string `json:"artifactPaths,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:validation:XValidation:rule="size(self.metadata.name) <= 50",message="name must be 50 characters or less"

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
