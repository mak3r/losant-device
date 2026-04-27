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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// CollectNodeHealth extracts condition flags and resource utilisation from a Node.
// Pod-level counts (PodCount, NotReadyPods, CrashLoopPods) are filled in by
// CollectPodHealth and must be merged by the caller.
func CollectNodeHealth(node corev1.Node) NodeHealth {
	h := NodeHealth{Name: node.Name}

	for _, cond := range node.Status.Conditions {
		switch cond.Type {
		case corev1.NodeReady:
			h.Ready = cond.Status == corev1.ConditionTrue
		case corev1.NodeMemoryPressure:
			h.MemoryPressure = cond.Status == corev1.ConditionTrue
		case corev1.NodeDiskPressure:
			h.DiskPressure = cond.Status == corev1.ConditionTrue
		case corev1.NodePIDPressure:
			h.PIDPressure = cond.Status == corev1.ConditionTrue
		}
	}

	h.CPURequestPct = requestPct(
		node.Status.Allocatable[corev1.ResourceCPU],
		node.Status.Capacity[corev1.ResourceCPU],
	)
	h.MemRequestPct = requestPct(
		node.Status.Allocatable[corev1.ResourceMemory],
		node.Status.Capacity[corev1.ResourceMemory],
	)

	score := ComputeNodeHealthScore(h)
	h.HealthScore = score
	h.HealthStatus = HealthStatusFromScore(score)

	return h
}

// requestPct returns (1 - allocatable/capacity) * 100 as a rough proxy for
// requested-resource percentage. Returns 0 if capacity is zero.
func requestPct(allocatable, capacity resource.Quantity) float64 {
	cap := capacity.AsApproximateFloat64()
	if cap == 0 {
		return 0
	}
	alloc := allocatable.AsApproximateFloat64()
	pct := (1 - alloc/cap) * 100
	if pct < 0 {
		pct = 0
	}
	return pct
}
