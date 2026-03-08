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

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// BootArtifactSpec defines the desired state of BootArtifact.
// A BootArtifact represents a single downloadable file (kernel, initrd, or firmware)
// with integrity verification via SHA-256 or SHA-512.
type BootArtifactSpec struct {
	// url is the download URL for the artifact.
	// +required
	// +kubebuilder:validation:MinLength=1
	URL string `json:"url"`

	// sha256 is the expected SHA-256 hex digest of the downloaded file.
	// Exactly one of sha256 or sha512 must be specified.
	// +optional
	// +kubebuilder:validation:Pattern="^[a-fA-F0-9]{64}$"
	SHA256 *string `json:"sha256,omitempty"`

	// sha512 is the expected SHA-512 hex digest of the downloaded file.
	// Exactly one of sha256 or sha512 must be specified.
	// +optional
	// +kubebuilder:validation:Pattern="^[a-fA-F0-9]{128}$"
	SHA512 *string `json:"sha512,omitempty"`
}

// BootArtifactPhase describes the current phase of a BootArtifact.
// +kubebuilder:validation:Enum=Pending;Downloading;Ready;Error
type BootArtifactPhase string

const (
	BootArtifactPhasePending     BootArtifactPhase = "Pending"
	BootArtifactPhaseDownloading BootArtifactPhase = "Downloading"
	BootArtifactPhaseReady       BootArtifactPhase = "Ready"
	BootArtifactPhaseError       BootArtifactPhase = "Error"
)

// BootArtifactStatus defines the observed state of BootArtifact.
type BootArtifactStatus struct {
	// phase is the current phase of the artifact.
	// +optional
	Phase BootArtifactPhase `json:"phase,omitempty"`

	// filePath is the local path where the artifact is stored.
	// +optional
	FilePath string `json:"filePath,omitempty"`

	// message provides human-readable details about the current phase.
	// +optional
	Message string `json:"message,omitempty"`

	// lastChecked is the last time the artifact file was verified.
	// +optional
	LastChecked *metav1.Time `json:"lastChecked,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="URL",type=string,JSONPath=".spec.url"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// BootArtifact is the Schema for the bootartifacts API.
type BootArtifact struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of BootArtifact
	// +required
	Spec BootArtifactSpec `json:"spec"`

	// status defines the observed state of BootArtifact
	// +optional
	Status BootArtifactStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// BootArtifactList contains a list of BootArtifact
type BootArtifactList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []BootArtifact `json:"items"`
}

func init() {
	SchemeBuilder.Register(&BootArtifact{}, &BootArtifactList{})
}
