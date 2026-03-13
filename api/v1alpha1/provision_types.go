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

// ProvisionSpec defines the desired state of Provision.
type ProvisionSpec struct {
	// machineRef is the name of the Machine resource for this provision.
	// +required
	// +kubebuilder:validation:MinLength=1
	MachineRef string `json:"machineRef"`

	// bootConfigRef is the name of the BootConfig resource for this provision.
	// +required
	// +kubebuilder:validation:MinLength=1
	BootConfigRef string `json:"bootConfigRef"`

	// provisionAnswerRef is the name of the ProvisionAnswer resource for this provision.
	// +required
	// +kubebuilder:validation:MinLength=1
	ProvisionAnswerRef string `json:"provisionAnswerRef"`

	// configMaps is an optional list of ConfigMap names to mount during provisioning.
	// +optional
	ConfigMaps []string `json:"configMaps,omitempty"`

	// secrets is an optional list of Secret names to mount during provisioning.
	// +optional
	Secrets []string `json:"secrets,omitempty"`

	// machineId is an optional machine identifier (32-character hex string).
	// +optional
	// +kubebuilder:validation:Pattern="^[0-9a-f]{32}$"
	MachineId *string `json:"machineId,omitempty"`
}

// ProvisionPhase describes the current phase of a Provision.
// +kubebuilder:validation:Enum=Pending;WaitingForBootSource;InProgress;Complete;Failed;ConfigError
type ProvisionPhase string

const (
	ProvisionPhasePending              ProvisionPhase = "Pending"
	ProvisionPhaseWaitingForBootSource ProvisionPhase = "WaitingForBootSource"
	ProvisionPhaseInProgress           ProvisionPhase = "InProgress"
	ProvisionPhaseComplete             ProvisionPhase = "Complete"
	ProvisionPhaseFailed               ProvisionPhase = "Failed"
	ProvisionPhaseConfigError          ProvisionPhase = "ConfigError"
)

// ProvisionStatus defines the observed state of Provision.
type ProvisionStatus struct {
	// phase is the current phase of the provision.
	// +optional
	Phase ProvisionPhase `json:"phase,omitempty"`

	// message provides human-readable details about the current phase.
	// +optional
	Message string `json:"message,omitempty"`

	// lastUpdated is the timestamp of the last status update.
	// +optional
	LastUpdated *metav1.Time `json:"lastUpdated,omitempty"`

	// ip is the IP address assigned to the machine during provisioning.
	// +optional
	IP string `json:"ip,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=prov
// +kubebuilder:printcolumn:name="Machine",type=string,JSONPath=".spec.machineRef"
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// Provision is the Schema for the provisions API.
type Provision struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Provision
	// +required
	Spec ProvisionSpec `json:"spec"`

	// status defines the observed state of Provision
	// +optional
	Status ProvisionStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ProvisionList contains a list of Provision
type ProvisionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Provision `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Provision{}, &ProvisionList{})
}
