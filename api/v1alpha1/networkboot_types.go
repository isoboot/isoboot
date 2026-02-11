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

// URL is an HTTPS URL with no userinfo (@) allowed in the hostname.
// +kubebuilder:validation:MaxLength=2048
// +kubebuilder:validation:Pattern=`^https://[^/@]+/[^/].*$`
type URL string

// BinaryHashPair holds a binary URL and its corresponding hash URL.
// The hostnames of binary and hash must match.
// +kubebuilder:validation:XValidation:rule="self.binary.split('/')[2] == self.hash.split('/')[2]",message="binary and hash hostnames must match"
type BinaryHashPair struct {
	// binary is the HTTPS URL of the artifact.
	// +required
	Binary URL `json:"binary"`

	// hash is the HTTPS URL of the hash for the artifact.
	// +required
	Hash URL `json:"hash"`
}

// ISOSpec defines an ISO image and paths within it.
type ISOSpec struct {
	BinaryHashPair `json:",inline"`

	// kernel is the absolute path to the kernel within the ISO.
	// +required
	// +kubebuilder:validation:MaxLength=1024
	// +kubebuilder:validation:Pattern=`^/.*$`
	// +kubebuilder:validation:XValidation:rule="!self.contains('/../') && !self.endsWith('/..')",message="must not contain path traversal"
	Kernel string `json:"kernel"`

	// initrd is the absolute path to the initrd within the ISO.
	// +required
	// +kubebuilder:validation:MaxLength=1024
	// +kubebuilder:validation:Pattern=`^/.*$`
	// +kubebuilder:validation:XValidation:rule="!self.contains('/../') && !self.endsWith('/..')",message="must not contain path traversal"
	Initrd string `json:"initrd"`
}

// FirmwareSpec defines firmware binary/hash and an optional path prefix.
type FirmwareSpec struct {
	BinaryHashPair `json:",inline"`

	// prefix is the path prefix for firmware serving.
	// +optional
	// +kubebuilder:default="/with-firmware"
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^/[^/](.*[^/])?$`
	// +kubebuilder:validation:XValidation:rule="!self.contains('/../') && !self.endsWith('/..')",message="must not contain path traversal"
	Prefix *string `json:"prefix,omitempty"`
}

// NetworkBootSpec defines the desired state of NetworkBoot.
// Exactly one boot mode must be specified: either (kernel + initrd) for direct boot,
// or iso for ISO-based boot.
// +kubebuilder:validation:XValidation:rule="has(self.kernel) == has(self.initrd)",message="kernel and initrd must both be set or both be unset"
// +kubebuilder:validation:XValidation:rule="has(self.iso) != has(self.kernel)",message="must specify either (kernel and initrd) or iso, not both and not neither"
type NetworkBootSpec struct {
	// kernel defines the kernel binary and hash URLs (direct boot mode).
	// +optional
	Kernel *BinaryHashPair `json:"kernel,omitempty"`

	// initrd defines the initrd binary and hash URLs (direct boot mode).
	// +optional
	Initrd *BinaryHashPair `json:"initrd,omitempty"`

	// iso defines an ISO image and paths to kernel/initrd within it (ISO boot mode).
	// +optional
	ISO *ISOSpec `json:"iso,omitempty"`

	// firmware defines firmware binary, hash, and optional path prefix.
	// +optional
	Firmware *FirmwareSpec `json:"firmware,omitempty"`
}

// NetworkBootStatus defines the observed state of NetworkBoot.
type NetworkBootStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// conditions represent the current state of the NetworkBoot resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// NetworkBoot is the Schema for the networkboots API
type NetworkBoot struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of NetworkBoot
	// +required
	Spec NetworkBootSpec `json:"spec"`

	// status defines the observed state of NetworkBoot
	// +optional
	Status NetworkBootStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// NetworkBootList contains a list of NetworkBoot
type NetworkBootList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []NetworkBoot `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NetworkBoot{}, &NetworkBootList{})
}
