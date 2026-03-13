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

// ProvisionAnswerSpec defines the desired state of ProvisionAnswer.
type ProvisionAnswerSpec struct {
	// files is a map of filename to template content with Go template placeholders.
	// +required
	// +kubebuilder:validation:MinProperties=1
	// +kubebuilder:validation:XValidation:rule="self.all(k, k.matches('^[A-Za-z0-9][-A-Za-z0-9_.]*$'))",message="File names must be valid path components (no slashes or path traversal)"
	Files map[string]string `json:"files"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:shortName=pa
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// ProvisionAnswer is the Schema for the provisionanswers API.
type ProvisionAnswer struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of ProvisionAnswer
	// +required
	Spec ProvisionAnswerSpec `json:"spec"`
}

// +kubebuilder:object:root=true

// ProvisionAnswerList contains a list of ProvisionAnswer
type ProvisionAnswerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []ProvisionAnswer `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ProvisionAnswer{}, &ProvisionAnswerList{})
}
