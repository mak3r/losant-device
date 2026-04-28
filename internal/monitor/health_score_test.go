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
	"testing"
)

func TestHealthStatusFromScore(t *testing.T) {
	cases := []struct {
		score int
		want  string
	}{
		{100, "healthy"},
		{80, "healthy"},
		{79, "degraded"},
		{50, "degraded"},
		{49, "critical"},
		{0, "critical"},
	}
	for _, tc := range cases {
		if got := HealthStatusFromScore(tc.score); got != tc.want {
			t.Errorf("HealthStatusFromScore(%d) = %q, want %q", tc.score, got, tc.want)
		}
	}
}

func TestComputeNodeHealthScore(t *testing.T) {
	cases := []struct {
		name string
		node NodeHealth
		want int
	}{
		{"healthy node", NodeHealth{Ready: true}, 100},
		{"not ready", NodeHealth{Ready: false}, 80},
		{"memory pressure", NodeHealth{Ready: true, MemoryPressure: true}, 90},
		{"disk pressure", NodeHealth{Ready: true, DiskPressure: true}, 90},
		{"PID pressure", NodeHealth{Ready: true, PIDPressure: true}, 95},
		{"all pressures + not ready", NodeHealth{Ready: false, MemoryPressure: true, DiskPressure: true, PIDPressure: true}, 55},
		{"clamped to zero", NodeHealth{Ready: false, MemoryPressure: true, DiskPressure: true, PIDPressure: true}, 55},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ComputeNodeHealthScore(tc.node); got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestComputeClusterHealthScore_Healthy(t *testing.T) {
	cluster := ClusterHealth{
		TotalNodes:     2,
		ReadyNodes:     2,
		CoreDNSHealthy: true,
	}
	nodes := map[string]NodeHealth{
		"node-a": {Ready: true},
		"node-b": {Ready: true},
	}
	got := ComputeClusterHealthScore(cluster, nodes)
	if got != 100 {
		t.Errorf("fully healthy cluster: got %d, want 100", got)
	}
}

func TestComputeClusterHealthScore_Deductions(t *testing.T) {
	cases := []struct {
		name    string
		cluster ClusterHealth
		nodes   map[string]NodeHealth
		wantMin int
		wantMax int
	}{
		{
			"coreDNS down",
			ClusterHealth{CoreDNSHealthy: false},
			map[string]NodeHealth{"n": {Ready: true}},
			84, 86,
		},
		{
			"10 crash loop pods",
			ClusterHealth{CrashLoopPods: 10, CoreDNSHealthy: true},
			map[string]NodeHealth{"n": {Ready: true}},
			79, 81,
		},
		{
			"crash loop cap at 20",
			ClusterHealth{CrashLoopPods: 50, CoreDNSHealthy: true},
			map[string]NodeHealth{"n": {Ready: true}},
			79, 81,
		},
		{
			"failed pods cap at 10",
			ClusterHealth{FailedPods: 20, CoreDNSHealthy: true},
			map[string]NodeHealth{"n": {Ready: true}},
			89, 91,
		},
		{
			"event warnings cap at 10",
			ClusterHealth{EventWarnings: 20, CoreDNSHealthy: true},
			map[string]NodeHealth{"n": {Ready: true}},
			89, 91,
		},
		{
			"degraded PVCs cap at 10",
			ClusterHealth{DegradedPVCs: 20, CoreDNSHealthy: true},
			map[string]NodeHealth{"n": {Ready: true}},
			89, 91,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ComputeClusterHealthScore(tc.cluster, tc.nodes)
			if got < tc.wantMin || got > tc.wantMax {
				t.Errorf("got %d, want in [%d, %d]", got, tc.wantMin, tc.wantMax)
			}
		})
	}
}

func TestComputeClusterHealthScore_NoNodes(t *testing.T) {
	cluster := ClusterHealth{CoreDNSHealthy: true}
	got := ComputeClusterHealthScore(cluster, nil)
	if got != 100 {
		t.Errorf("empty node map: got %d, want 100", got)
	}
}

func TestComputeClusterHealthScore_ClampedToZero(t *testing.T) {
	cluster := ClusterHealth{
		CrashLoopPods:  50,
		FailedPods:     20,
		EventWarnings:  20,
		DegradedPVCs:   20,
		CoreDNSHealthy: false,
	}
	nodes := map[string]NodeHealth{
		"n": {Ready: false, MemoryPressure: true, DiskPressure: true, PIDPressure: true},
	}
	got := ComputeClusterHealthScore(cluster, nodes)
	if got < 0 {
		t.Errorf("score clamped below zero: %d", got)
	}
}
