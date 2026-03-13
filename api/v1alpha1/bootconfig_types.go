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

// BootConfigISOSpec defines the ISO extraction configuration.
type BootConfigISOSpec struct {
	// artifactRef is the name of the BootArtifact for the ISO file.
	// +required
	// +kubebuilder:validation:MinLength=1
	ArtifactRef string `json:"artifactRef"`

	// kernelPath is the path to the kernel within the ISO.
	// +required
	// +kubebuilder:validation:MinLength=1
	KernelPath string `json:"kernelPath"`

	// initrdPath is the path to the initrd within the ISO.
	// +required
	// +kubebuilder:validation:MinLength=1
	InitrdPath string `json:"initrdPath"`
}

// BootConfigKernelSpec defines the kernel configuration for a BootConfig.
type BootConfigKernelSpec struct {
	// ref is the name of the BootArtifact for the kernel.
	// +required
	// +kubebuilder:validation:MinLength=1
	Ref string `json:"ref"`

	// args is the kernel boot arguments template string.
	// May contain Go template variables for interpolation at provision time.
	// +optional
	Args string `json:"args,omitempty"`
}

// BootConfigInitrdSpec defines the initrd configuration for a BootConfig.
type BootConfigInitrdSpec struct {
	// ref is the name of the BootArtifact for the initrd.
	// +required
	// +kubebuilder:validation:MinLength=1
	Ref string `json:"ref"`
}

// BootConfigFirmwareSpec defines the firmware configuration for a BootConfig.
type BootConfigFirmwareSpec struct {
	// ref is the name of the BootArtifact for the firmware archive.
	// When set, the controller concatenates initrd + firmware into the served initrd.
	// +required
	// +kubebuilder:validation:MinLength=1
	Ref string `json:"ref"`
}

// BootConfigSpec defines the desired state of BootConfig.
// A BootConfig groups BootArtifacts into a servable PXE boot directory.
// The directory name is metadata.name.
// Two mutually exclusive modes: direct refs (kernel + initrd) or ISO extraction (iso).
// +kubebuilder:validation:XValidation:rule="(has(self.kernel) && has(self.initrd) && !has(self.iso)) || (!has(self.kernel) && !has(self.initrd) && has(self.iso))",message="must use either kernel+initrd or iso, not both"
// +kubebuilder:validation:XValidation:rule="!has(self.iso) || !has(self.firmware)",message="firmware is not supported with iso mode"
type BootConfigSpec struct {
	// kernel defines the kernel artifact and boot arguments (mode A).
	// +optional
	Kernel *BootConfigKernelSpec `json:"kernel,omitempty"`

	// initrd defines the initrd artifact (mode A).
	// +optional
	Initrd *BootConfigInitrdSpec `json:"initrd,omitempty"`

	// firmware defines the firmware archive artifact (mode A only).
	// When set, the controller concatenates initrd + firmware into the served initrd.
	// +optional
	Firmware *BootConfigFirmwareSpec `json:"firmware,omitempty"`

	// iso defines ISO extraction configuration (mode B).
	// +optional
	ISO *BootConfigISOSpec `json:"iso,omitempty"`
}

// BootConfigPhase describes the current phase of a BootConfig.
// +kubebuilder:validation:Enum=Pending;Ready;Error
type BootConfigPhase string

const (
	BootConfigPhasePending BootConfigPhase = "Pending"
	BootConfigPhaseReady   BootConfigPhase = "Ready"
	BootConfigPhaseError   BootConfigPhase = "Error"
)

// BootConfigStatus defines the observed state of BootConfig.
type BootConfigStatus struct {
	// phase is the current phase of the boot config.
	// +optional
	Phase BootConfigPhase `json:"phase,omitempty"`

	// message provides human-readable details about the current phase.
	// +optional
	Message string `json:"message,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// BootConfig is the Schema for the bootconfigs API.
type BootConfig struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of BootConfig
	// +required
	Spec BootConfigSpec `json:"spec"`

	// status defines the observed state of BootConfig
	// +optional
	Status BootConfigStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// BootConfigList contains a list of BootConfig
type BootConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []BootConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&BootConfig{}, &BootConfigList{})
}
