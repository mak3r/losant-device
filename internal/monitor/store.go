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

package monitor

import "sync"

// NodeHealth holds the health snapshot for a single k8s node.
type NodeHealth struct {
	Name           string
	Ready          bool
	MemoryPressure bool
	DiskPressure   bool
	PIDPressure    bool
	CPURequestPct  float64
	MemRequestPct  float64
	PodCount       int
	NotReadyPods   int
	CrashLoopPods  int
	HealthScore    int
	HealthStatus   string
}

// ClusterHealth holds the aggregated health snapshot for the entire cluster.
type ClusterHealth struct {
	TotalNodes     int
	ReadyNodes     int
	UnhealthyNodes int
	TotalPods      int
	RunningPods    int
	FailedPods     int
	PendingPods    int
	CrashLoopPods  int
	DegradedPVCs   int
	CoreDNSHealthy bool
	EventWarnings  int
	HealthScore    int
	HealthStatus   string
}

// HealthStore is a thread-safe in-memory snapshot of cluster health.
// Written by HealthWatcherReconciler; read by LosantSyncReconciler.
type HealthStore struct {
	mu      sync.RWMutex
	cluster ClusterHealth
	nodes   map[string]NodeHealth
}

// NewHealthStore returns an initialised, empty HealthStore.
func NewHealthStore() *HealthStore {
	return &HealthStore{nodes: make(map[string]NodeHealth)}
}

// Update atomically replaces the stored snapshot.
func (s *HealthStore) Update(cluster ClusterHealth, nodes map[string]NodeHealth) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cluster = cluster
	s.nodes = nodes
}

// Snapshot returns a deep copy of the current health state.
func (s *HealthStore) Snapshot() (ClusterHealth, map[string]NodeHealth) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	nodes := make(map[string]NodeHealth, len(s.nodes))
	for k, v := range s.nodes {
		nodes[k] = v
	}
	return s.cluster, nodes
}
