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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func makeNode(name string, conditions []corev1.NodeCondition, allocCPU, capCPU, allocMem, capMem string) corev1.Node {
	alloc := corev1.ResourceList{}
	cap := corev1.ResourceList{}
	if allocCPU != "" {
		alloc[corev1.ResourceCPU] = resource.MustParse(allocCPU)
		cap[corev1.ResourceCPU] = resource.MustParse(capCPU)
	}
	if allocMem != "" {
		alloc[corev1.ResourceMemory] = resource.MustParse(allocMem)
		cap[corev1.ResourceMemory] = resource.MustParse(capMem)
	}
	return corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Conditions:  conditions,
			Allocatable: alloc,
			Capacity:    cap,
		},
	}
}

func readyConditions() []corev1.NodeCondition {
	return []corev1.NodeCondition{
		{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
	}
}

func TestCollectNodeHealth_Ready(t *testing.T) {
	node := makeNode("n1", readyConditions(), "900m", "1000m", "900Mi", "1000Mi")
	h := CollectNodeHealth(node)

	if !h.Ready {
		t.Error("expected Ready=true")
	}
	if h.Name != "n1" {
		t.Errorf("Name: got %q, want %q", h.Name, "n1")
	}
	if h.HealthScore != 100 {
		t.Errorf("HealthScore: got %d, want 100", h.HealthScore)
	}
	if h.HealthStatus != "healthy" {
		t.Errorf("HealthStatus: got %q, want %q", h.HealthStatus, "healthy")
	}
}

func TestCollectNodeHealth_Pressures(t *testing.T) {
	conditions := []corev1.NodeCondition{
		{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
		{Type: corev1.NodeMemoryPressure, Status: corev1.ConditionTrue},
		{Type: corev1.NodeDiskPressure, Status: corev1.ConditionTrue},
		{Type: corev1.NodePIDPressure, Status: corev1.ConditionFalse},
	}
	node := makeNode("n2", conditions, "", "", "", "")
	h := CollectNodeHealth(node)

	if !h.MemoryPressure {
		t.Error("expected MemoryPressure=true")
	}
	if !h.DiskPressure {
		t.Error("expected DiskPressure=true")
	}
	if h.PIDPressure {
		t.Error("expected PIDPressure=false")
	}
}

func TestCollectNodeHealth_NotReady(t *testing.T) {
	conditions := []corev1.NodeCondition{
		{Type: corev1.NodeReady, Status: corev1.ConditionFalse},
	}
	node := makeNode("n3", conditions, "", "", "", "")
	h := CollectNodeHealth(node)

	if h.Ready {
		t.Error("expected Ready=false")
	}
	if h.HealthScore >= 100 {
		t.Errorf("not-ready node should score < 100, got %d", h.HealthScore)
	}
}

func TestCollectNodeHealth_ZeroCapacity(t *testing.T) {
	node := makeNode("n4", readyConditions(), "", "", "", "")
	h := CollectNodeHealth(node)

	if h.CPURequestPct != 0 {
		t.Errorf("zero capacity CPU: got %f, want 0", h.CPURequestPct)
	}
	if h.MemRequestPct != 0 {
		t.Errorf("zero capacity Mem: got %f, want 0", h.MemRequestPct)
	}
}

func TestRequestPct_AllocatableExceedsCapacity(t *testing.T) {
	// When allocatable > capacity the pct is negative; clamp returns 0.
	alloc := resource.MustParse("1200m")
	cap := resource.MustParse("1000m")
	if got := requestPct(alloc, cap); got != 0 {
		t.Errorf("requestPct when alloc > cap: got %f, want 0", got)
	}
}

func TestCollectNodeHealth_ResourcePct(t *testing.T) {
	// allocatable=500m out of 1000m means 50% requested
	node := makeNode("n5", readyConditions(), "500m", "1000m", "512Mi", "1024Mi")
	h := CollectNodeHealth(node)

	// requestPct = (1 - alloc/cap) * 100 = (1 - 0.5) * 100 = 50
	if h.CPURequestPct < 49 || h.CPURequestPct > 51 {
		t.Errorf("CPURequestPct: got %f, want ~50", h.CPURequestPct)
	}
	if h.MemRequestPct < 49 || h.MemRequestPct > 51 {
		t.Errorf("MemRequestPct: got %f, want ~50", h.MemRequestPct)
	}
}
