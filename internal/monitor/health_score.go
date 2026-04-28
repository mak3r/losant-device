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

// HealthStatusFromScore maps a 0-100 score to a Losant tag value.
func HealthStatusFromScore(score int) string {
	switch {
	case score >= 80:
		return "healthy"
	case score >= 50:
		return "degraded"
	default:
		return "critical"
	}
}

// ComputeNodeHealthScore returns a 0-100 score for a single node using the
// deduction table from docs/architecture.md.
func ComputeNodeHealthScore(n NodeHealth) int {
	score := 100
	if !n.Ready {
		score -= 20
	}
	if n.MemoryPressure {
		score -= 10
	}
	if n.DiskPressure {
		score -= 10
	}
	if n.PIDPressure {
		score -= 5
	}
	return clamp(score, 0, 100)
}

// ComputeClusterHealthScore returns a 0-100 score for the cluster.
//
// Per-node deductions are averaged across all nodes, then cluster-level
// deductions are applied, per docs/architecture.md.
func ComputeClusterHealthScore(cluster ClusterHealth, nodes map[string]NodeHealth) int {
	score := 100

	// Per-node deductions averaged across all nodes.
	if len(nodes) > 0 {
		totalNodeDeductions := 0
		for _, n := range nodes {
			d := 0
			if !n.Ready {
				d += 20
			}
			if n.MemoryPressure {
				d += 10
			}
			if n.DiskPressure {
				d += 10
			}
			if n.PIDPressure {
				d += 5
			}
			totalNodeDeductions += d
		}
		score -= totalNodeDeductions / len(nodes)
	}

	// Cluster-level deductions.
	crashLoopDeduction := cluster.CrashLoopPods * 2
	if crashLoopDeduction > 20 {
		crashLoopDeduction = 20
	}
	score -= crashLoopDeduction

	failedDeduction := cluster.FailedPods
	if failedDeduction > 10 {
		failedDeduction = 10
	}
	score -= failedDeduction

	warnDeduction := cluster.EventWarnings
	if warnDeduction > 10 {
		warnDeduction = 10
	}
	score -= warnDeduction

	if !cluster.CoreDNSHealthy {
		score -= 15
	}

	pvcDeduction := cluster.DegradedPVCs
	if pvcDeduction > 10 {
		pvcDeduction = 10
	}
	score -= pvcDeduction

	return clamp(score, 0, 100)
}

func clamp(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
