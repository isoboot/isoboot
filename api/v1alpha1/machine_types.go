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

// MachineSpec defines the desired state of Machine.
type MachineSpec struct {
	// mac is the MAC address for this machine (dash-separated, e.g., aa-bb-cc-dd-ee-ff).
	// +required
	// +kubebuilder:validation:Pattern="^([0-9A-Fa-f]{2}-){5}([0-9A-Fa-f]{2})$"
	MAC string `json:"mac"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:shortName=mach
// +kubebuilder:printcolumn:name="MAC",type=string,JSONPath=".spec.mac"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// Machine is the Schema for the machines API.
type Machine struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Machine
	// +required
	Spec MachineSpec `json:"spec"`
}

// +kubebuilder:object:root=true

// MachineList contains a list of Machine
type MachineList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Machine `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Machine{}, &MachineList{})
}
