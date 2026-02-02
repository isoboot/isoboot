package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// BootSourcePhase represents the current phase of a BootSource.
type BootSourcePhase string

const (
	BootSourcePhasePending     BootSourcePhase = "Pending"
	BootSourcePhaseDownloading BootSourcePhase = "Downloading"
	BootSourcePhaseBuilding    BootSourcePhase = "Building"
	BootSourcePhaseExtracting  BootSourcePhase = "Extracting"
	BootSourcePhaseVerifying   BootSourcePhase = "Verifying"
	BootSourcePhaseReady       BootSourcePhase = "Ready"
	BootSourcePhaseCorrupted   BootSourcePhase = "Corrupted"
	BootSourcePhaseFailed      BootSourcePhase = "Failed"
)

// DownloadableResource defines a resource that can be downloaded and verified.
// +kubebuilder:validation:XValidation:rule="has(self.shasumURL) || has(self.shasum)",message="at least one of shasumURL or shasum must be specified"
type DownloadableResource struct {
	// url is the download URL for the resource.
	// +required
	URL string `json:"url"`

	// shasumURL is the URL to a SHA256SUMS file for verification.
	// +optional
	ShasumURL *string `json:"shasumURL,omitempty"`

	// shasum is the expected SHA256 checksum of the resource.
	// +optional
	Shasum *string `json:"shasum,omitempty"`
}

// ISOSource defines an ISO image source with kernel and initrd extraction paths.
type ISOSource struct {
	DownloadableResource `json:",inline"`

	// kernelPath is the path within the ISO to the kernel file.
	// +required
	// +kubebuilder:validation:MinLength=1
	KernelPath string `json:"kernelPath"`

	// initrdPath is the path within the ISO to the initrd file.
	// +required
	// +kubebuilder:validation:MinLength=1
	InitrdPath string `json:"initrdPath"`
}

// BootSourceSpec defines the desired state of BootSource.
// +kubebuilder:validation:XValidation:rule="(has(self.kernel) && has(self.initrd)) || has(self.iso)",message="must specify either (kernel and initrd) or iso"
// +kubebuilder:validation:XValidation:rule="!(has(self.iso) && (has(self.kernel) || has(self.initrd)))",message="cannot specify both iso and kernel/initrd"
type BootSourceSpec struct {
	// kernel is a direct kernel download source.
	// +optional
	Kernel *DownloadableResource `json:"kernel,omitempty"`

	// initrd is a direct initrd download source.
	// +optional
	Initrd *DownloadableResource `json:"initrd,omitempty"`

	// iso is an ISO image source containing kernel and initrd.
	// +optional
	ISO *ISOSource `json:"iso,omitempty"`

	// firmware is an optional firmware/driver archive to combine with initrd.
	// +optional
	Firmware *DownloadableResource `json:"firmware,omitempty"`
}

// ResourceStatus represents the status of an individual downloaded resource.
type ResourceStatus struct {
	// url is the URL the resource was downloaded from.
	// +optional
	URL string `json:"url,omitempty"`

	// shasum is the verified SHA256 checksum.
	// +optional
	Shasum string `json:"shasum,omitempty"`

	// size is the file size in bytes.
	// +optional
	Size int64 `json:"size,omitempty"`

	// path is the local file path.
	// +optional
	Path string `json:"path,omitempty"`
}

// BootSourceStatus defines the observed state of BootSource.
type BootSourceStatus struct {
	// phase is the current phase of the BootSource.
	// +optional
	Phase BootSourcePhase `json:"phase,omitempty"`

	// message provides human-readable details about the current phase.
	// +optional
	Message string `json:"message,omitempty"`

	// resources contains per-resource status information.
	// Keys include: kernel, initrd, iso, firmware, initrdWithFirmware.
	// +optional
	Resources map[string]ResourceStatus `json:"resources,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Message",type=string,JSONPath=`.status.message`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:resource:shortName=bs

// BootSource is the Schema for the bootsources API.
type BootSource struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of BootSource.
	// +required
	Spec BootSourceSpec `json:"spec"`

	// status defines the observed state of BootSource.
	// +optional
	Status BootSourceStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// BootSourceList contains a list of BootSource.
type BootSourceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []BootSource `json:"items"`
}

func init() {
	SchemeBuilder.Register(&BootSource{}, &BootSourceList{})
}
