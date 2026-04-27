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
)

// PodCounts holds pod-level aggregates for one node or the whole cluster.
type PodCounts struct {
	Total       int
	Running     int
	Failed      int
	Pending     int
	CrashLoop   int
	NotReady    int
}

// CollectPodCounts partitions pods by node, returning per-node and cluster-wide counts.
func CollectPodCounts(pods []corev1.Pod) (perNode map[string]PodCounts, cluster PodCounts) {
	perNode = make(map[string]PodCounts)

	for i := range pods {
		p := &pods[i]
		nodeName := p.Spec.NodeName

		nc := perNode[nodeName]
		nc.Total++
		cluster.Total++

		switch p.Status.Phase {
		case corev1.PodRunning:
			nc.Running++
			cluster.Running++
		case corev1.PodFailed:
			nc.Failed++
			cluster.Failed++
		case corev1.PodPending:
			nc.Pending++
			cluster.Pending++
		}

		if isCrashLooping(p) {
			nc.CrashLoop++
			cluster.CrashLoop++
		}

		if !isPodReady(p) && p.Status.Phase == corev1.PodRunning {
			nc.NotReady++
		}

		perNode[nodeName] = nc
	}

	return perNode, cluster
}

func isCrashLooping(p *corev1.Pod) bool {
	for _, cs := range p.Status.ContainerStatuses {
		if cs.State.Waiting != nil && cs.State.Waiting.Reason == "CrashLoopBackOff" {
			return true
		}
	}
	return false
}

func isPodReady(p *corev1.Pod) bool {
	for _, cond := range p.Status.Conditions {
		if cond.Type == corev1.PodReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}
