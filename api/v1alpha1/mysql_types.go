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

// MySQLSpec defines the desired state of MySQL
type MySQLSpec struct {
	Image              string `json:"image,omitempty"`
	Database           string `json:"database,omitempty"`
	User               string `json:"user,omitempty"`
	PasswordSecretName string `json:"passwordSecretName,omitempty"`
	StorageSize        string `json:"storageSize,omitempty"`
	Port               int32  `json:"port,omitempty"`
}

// MySQLStatus defines the observed state of MySQL.
type MySQLStatus struct {
	Ready   bool   `json:"ready,omitempty"`
	Service string `json:"service,omitempty"`
	Message string `json:"message,omitempty"`
	Phase   string `json:"phase,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// MySQL is the Schema for the mysqls API
type MySQL struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of MySQL
	// +required
	Spec MySQLSpec `json:"spec"`

	// status defines the observed state of MySQL
	// +optional
	Status MySQLStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// MySQLList contains a list of MySQL
type MySQLList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []MySQL `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MySQL{}, &MySQLList{})
}
