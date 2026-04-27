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

// LosantSyncPhase represents the current lifecycle phase of a LosantSync resource.
type LosantSyncPhase string

const (
	PhaseProvisioning LosantSyncPhase = "Provisioning"
	PhaseActive       LosantSyncPhase = "Active"
	PhaseDegraded     LosantSyncPhase = "Degraded"
	PhaseSuspended    LosantSyncPhase = "Suspended"
)

// SecretRef identifies a Kubernetes Secret by name and namespace.
type SecretRef struct {
	// +kubebuilder:validation:Required
	Name string `json:"name"`
	// +kubebuilder:validation:Required
	Namespace string `json:"namespace"`
}

// GEASpec configures the in-cluster Losant Gateway Edge Agent service.
type GEASpec struct {
	// ServiceRef is the name of the in-cluster Service fronting the GEA pod.
	// +kubebuilder:validation:Required
	ServiceRef string `json:"serviceRef"`

	// Port is the HTTP trigger port on the GEA pod (default 8080).
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +kubebuilder:default=8080
	Port int32 `json:"port"`
}

// LosantSyncSpec defines the desired state of LosantSync.
//
// Exactly one of cronSchedule or interval must be set.
// +kubebuilder:validation:XValidation:rule="has(self.cronSchedule) != has(self.interval)",message="exactly one of cronSchedule or interval must be set"
type LosantSyncSpec struct {
	// ApplicationID is the Losant Application ID that owns all devices for this cluster.
	// +kubebuilder:validation:Required
	ApplicationID string `json:"applicationID"`

	// ProvisioningSecretRef references the Secret containing Losant API credentials
	// used for device provisioning (not used by the GEA).
	// +kubebuilder:validation:Required
	ProvisioningSecretRef SecretRef `json:"provisioningSecretRef"`

	// ClusterName is the human-readable name of this k8s cluster, used as a Losant device tag.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ClusterName string `json:"clusterName"`

	// Region identifies the geographic region of this cluster, used as a Losant device tag.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Region string `json:"region"`

	// RancherURL is the base URL of the Rancher Manager instance managing this cluster.
	// Used to render a direct link on the Losant Level 2 dashboard.
	// +optional
	RancherURL string `json:"rancherURL,omitempty"`

	// CronSchedule is a standard cron expression controlling how often metrics are reported.
	// Mutually exclusive with Interval.
	// +optional
	CronSchedule string `json:"cronSchedule,omitempty"`

	// Interval is a Go duration string (e.g. "1m", "5m") controlling report frequency.
	// Mutually exclusive with CronSchedule.
	// +optional
	Interval string `json:"interval,omitempty"`

	// GEA configures the in-cluster Gateway Edge Agent service.
	// +kubebuilder:validation:Required
	GEA GEASpec `json:"gea"`

	// DeviceRecipeID is an optional Losant device recipe ID used for bulk peripheral provisioning.
	// +optional
	DeviceRecipeID string `json:"deviceRecipeID,omitempty"`

	// Suspend disables all reconciliation when true.
	// +optional
	// +kubebuilder:default=false
	Suspend bool `json:"suspend,omitempty"`
}

// LosantSyncStatus defines the observed state of LosantSync.
// +kubebuilder:object:generate=true
type LosantSyncStatus struct {
	// Phase is the current lifecycle phase of this resource.
	// +optional
	Phase LosantSyncPhase `json:"phase,omitempty"`

	// Conditions represent the latest available observations of the resource's state.
	// Known condition types: GEAReachable, DevicesProvisioned, LastSyncSucceeded.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ClusterDeviceID is the Losant device ID of the Edge Compute device representing this cluster.
	// +optional
	ClusterDeviceID string `json:"clusterDeviceID,omitempty"`

	// NodeDevices maps Kubernetes node names to their Losant peripheral device IDs.
	// +optional
	NodeDevices map[string]string `json:"nodeDevices,omitempty"`

	// LastSyncTime is the timestamp of the most recent successful metric report to the GEA.
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// NextScheduledTime is the timestamp when the next metric report is due.
	// Persisted so controller restarts re-arm the schedule without missing a cycle.
	// +optional
	NextScheduledTime *metav1.Time `json:"nextScheduledTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Cluster",type=string,JSONPath=`.spec.clusterName`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Last Sync",type=date,JSONPath=`.status.lastSyncTime`
// +kubebuilder:printcolumn:name="Next Sync",type=date,JSONPath=`.status.nextScheduledTime`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// LosantSync is the Schema for the losantsyncs API.
// It configures the operator for a single k3s cluster, controlling how cluster health
// metrics are collected and reported to the Losant IoT platform via the GEA hybrid model.
type LosantSync struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   LosantSyncSpec   `json:"spec,omitempty"`
	Status LosantSyncStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// LosantSyncList contains a list of LosantSync.
type LosantSyncList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LosantSync `json:"items"`
}

func init() {
	SchemeBuilder.Register(&LosantSync{}, &LosantSyncList{})
}
