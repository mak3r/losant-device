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

// RancherSessionPhase represents the current lifecycle phase of a RancherSession resource.
type RancherSessionPhase string

const (
	RancherSessionPhaseConnecting    RancherSessionPhase = "Connecting"
	RancherSessionPhaseConnected     RancherSessionPhase = "Connected"
	RancherSessionPhaseDisconnecting RancherSessionPhase = "Disconnecting"
	RancherSessionPhaseDisconnected  RancherSessionPhase = "Disconnected"
	RancherSessionPhaseFailed        RancherSessionPhase = "Failed"
)

// RancherSessionSpec defines the desired state of RancherSession.
type RancherSessionSpec struct {
	// CredentialsSecretRef references the Secret containing the Rancher API token
	// and endpoint URL.
	// +kubebuilder:validation:Required
	CredentialsSecretRef SecretRef `json:"credentialsSecretRef"`

	// ClusterDisplayName is the human-readable name of the downstream Rancher cluster.
	// Defaults to the CR name when omitted.
	// +optional
	ClusterDisplayName string `json:"clusterDisplayName,omitempty"`

	// TTLSeconds is how long the Rancher registration session remains active before
	// the controller disconnects and removes the cluster entry.
	// +kubebuilder:default=3600
	// +optional
	TTLSeconds int64 `json:"ttlSeconds,omitempty"`

	// Suspend disables all reconciliation when true.
	// +optional
	// +kubebuilder:default=false
	Suspend bool `json:"suspend,omitempty"`
}

// RancherSessionStatus defines the observed state of RancherSession.
// +kubebuilder:object:generate=true
type RancherSessionStatus struct {
	// Phase is the current lifecycle phase of this resource.
	// +optional
	Phase RancherSessionPhase `json:"phase,omitempty"`

	// RancherClusterID is the ID assigned by Rancher Manager to the downstream cluster
	// after a successful CreateCluster call.
	// +optional
	RancherClusterID string `json:"rancherClusterID,omitempty"`

	// ManifestApplied indicates whether the Rancher import manifest has been applied
	// to the downstream cluster.
	// +optional
	ManifestApplied bool `json:"manifestApplied,omitempty"`

	// ConnectedAt is the timestamp when the session reached the Connected phase.
	// +optional
	ConnectedAt *metav1.Time `json:"connectedAt,omitempty"`

	// ExpiresAt is the timestamp when the TTL expires and the controller will disconnect.
	// +optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`

	// Conditions represent the latest available observations of the resource's state.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// LastTransitionMessage is a human-readable message describing the last phase transition.
	// +optional
	LastTransitionMessage string `json:"lastTransitionMessage,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Cluster ID",type=string,JSONPath=`.status.rancherClusterID`
// +kubebuilder:printcolumn:name="Connected",type=date,JSONPath=`.status.connectedAt`
// +kubebuilder:printcolumn:name="Expires",type=string,JSONPath=`.status.expiresAt`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// RancherSession is the Schema for the ranchersessions API.
// It represents a single dynamic connect/disconnect lifecycle against a Rancher Manager instance.
type RancherSession struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RancherSessionSpec   `json:"spec,omitempty"`
	Status RancherSessionStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RancherSessionList contains a list of RancherSession.
type RancherSessionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RancherSession `json:"items"`
}

func init() {
	SchemeBuilder.Register(&RancherSession{}, &RancherSessionList{})
}
