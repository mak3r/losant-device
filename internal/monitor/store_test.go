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

import (
	"sync"
	"testing"
)

func TestNewHealthStore(t *testing.T) {
	s := NewHealthStore()
	if s == nil {
		t.Fatal("NewHealthStore returned nil")
	}
	cluster, nodes := s.Snapshot()
	if nodes == nil {
		t.Error("initial nodes map should be non-nil")
	}
	if cluster.TotalNodes != 0 {
		t.Errorf("initial TotalNodes: got %d, want 0", cluster.TotalNodes)
	}
}

func TestHealthStore_UpdateAndSnapshot(t *testing.T) {
	s := NewHealthStore()

	cluster := ClusterHealth{TotalNodes: 3, ReadyNodes: 2, HealthScore: 75}
	nodes := map[string]NodeHealth{
		"n1": {Name: "n1", Ready: true, HealthScore: 100},
		"n2": {Name: "n2", Ready: false, HealthScore: 80},
	}
	s.Update(cluster, nodes)

	gotCluster, gotNodes := s.Snapshot()

	if gotCluster.TotalNodes != 3 {
		t.Errorf("TotalNodes: got %d, want 3", gotCluster.TotalNodes)
	}
	if gotCluster.HealthScore != 75 {
		t.Errorf("HealthScore: got %d, want 75", gotCluster.HealthScore)
	}
	if len(gotNodes) != 2 {
		t.Errorf("nodes count: got %d, want 2", len(gotNodes))
	}
	if gotNodes["n1"].HealthScore != 100 {
		t.Errorf("n1 HealthScore: got %d, want 100", gotNodes["n1"].HealthScore)
	}
}

func TestHealthStore_SnapshotIsDeepCopy(t *testing.T) {
	s := NewHealthStore()
	nodes := map[string]NodeHealth{
		"n1": {Name: "n1", Ready: true},
	}
	s.Update(ClusterHealth{}, nodes)

	_, gotNodes := s.Snapshot()
	// Mutating the returned map must not affect the store.
	delete(gotNodes, "n1")

	_, gotNodes2 := s.Snapshot()
	if _, ok := gotNodes2["n1"]; !ok {
		t.Error("mutating snapshot affected the store (not a deep copy)")
	}
}

func TestHealthStore_ConcurrentAccess(t *testing.T) {
	s := NewHealthStore()
	var wg sync.WaitGroup
	const goroutines = 20

	// Writers
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			s.Update(ClusterHealth{HealthScore: n}, map[string]NodeHealth{
				"n1": {Name: "n1", HealthScore: n},
			})
		}(i)
	}
	// Readers
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, nodes := s.Snapshot()
			_ = c.HealthScore
			_ = nodes
		}()
	}
	wg.Wait()
}
