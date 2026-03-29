package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MySQLSpec defines the desired state of MySQL
type MySQLSpec struct {
	// MySQL container image
	// +kubebuilder:default="mysql:8.0"
	Image string `json:"image,omitempty"`

	// Total number of MySQL pods
	// primary 1 + replicas N
	// +kubebuilder:default=3
	// +kubebuilder:validation:Minimum=1
	Replicas int32 `json:"replicas,omitempty"`

	// Application database name
	// +kubebuilder:default="appdb"
	Database string `json:"database,omitempty"`

	// Application user name
	// +kubebuilder:default="appuser"
	AppUser string `json:"appUser,omitempty"`

	// Secret name containing app user password
	AppPasswordSecretName string `json:"appPasswordSecretName,omitempty"`

	// Secret name containing root password
	RootPasswordSecretName string `json:"rootPasswordSecretName,omitempty"`

	// Secret name containing replication password
	ReplPasswordSecretName string `json:"replPasswordSecretName,omitempty"`

	// Storage size for each pod
	// +kubebuilder:default="10Gi"
	StorageSize string `json:"storageSize,omitempty"`

	// MySQL port
	// +kubebuilder:default=3306
	// +kubebuilder:validation:Minimum=1
	Port int32 `json:"port,omitempty"`
}

// MySQLStatus defines the observed state of MySQL
type MySQLStatus struct {
	ReadyReplicas  int32  `json:"readyReplicas,omitempty"`
	PrimaryPod     string `json:"primaryPod,omitempty"`
	PrimaryService string `json:"primaryService,omitempty"`
	ReplicaService string `json:"replicaService,omitempty"`
	Phase          string `json:"phase,omitempty"`
	Message        string `json:"message,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// MySQL is the Schema for the mysqls API
type MySQL struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MySQLSpec   `json:"spec,omitempty"`
	Status MySQLStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MySQLList contains a list of MySQL
type MySQLList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MySQL `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MySQL{}, &MySQLList{})
}
