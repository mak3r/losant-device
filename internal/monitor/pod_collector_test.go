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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func makePod(name, nodeName string, phase corev1.PodPhase, ready bool, crashLoop bool) corev1.Pod {
	var containerStatuses []corev1.ContainerStatus
	if crashLoop {
		containerStatuses = []corev1.ContainerStatus{
			{
				State: corev1.ContainerState{
					Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"},
				},
			},
		}
	}
	readyCondStatus := corev1.ConditionFalse
	if ready {
		readyCondStatus = corev1.ConditionTrue
	}
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       corev1.PodSpec{NodeName: nodeName},
		Status: corev1.PodStatus{
			Phase:             phase,
			ContainerStatuses: containerStatuses,
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: readyCondStatus},
			},
		},
	}
}

func TestCollectPodCounts_Basic(t *testing.T) {
	pods := []corev1.Pod{
		makePod("p1", "node-a", corev1.PodRunning, true, false),
		makePod("p2", "node-a", corev1.PodRunning, false, false),
		makePod("p3", "node-a", corev1.PodFailed, false, false),
		makePod("p4", "node-b", corev1.PodPending, false, false),
		makePod("p5", "node-b", corev1.PodRunning, true, true),
	}

	perNode, cluster := CollectPodCounts(pods)

	if cluster.Total != 5 {
		t.Errorf("cluster.Total: got %d, want 5", cluster.Total)
	}
	if cluster.Running != 3 {
		t.Errorf("cluster.Running: got %d, want 3", cluster.Running)
	}
	if cluster.Failed != 1 {
		t.Errorf("cluster.Failed: got %d, want 1", cluster.Failed)
	}
	if cluster.Pending != 1 {
		t.Errorf("cluster.Pending: got %d, want 1", cluster.Pending)
	}
	if cluster.CrashLoop != 1 {
		t.Errorf("cluster.CrashLoop: got %d, want 1", cluster.CrashLoop)
	}

	nodeA := perNode["node-a"]
	if nodeA.Total != 3 {
		t.Errorf("node-a Total: got %d, want 3", nodeA.Total)
	}
	if nodeA.NotReady != 1 {
		t.Errorf("node-a NotReady: got %d, want 1 (running but not ready)", nodeA.NotReady)
	}

	nodeB := perNode["node-b"]
	if nodeB.CrashLoop != 1 {
		t.Errorf("node-b CrashLoop: got %d, want 1", nodeB.CrashLoop)
	}
}

func TestCollectPodCounts_Empty(t *testing.T) {
	perNode, cluster := CollectPodCounts(nil)
	if cluster.Total != 0 {
		t.Errorf("empty pods: cluster.Total = %d", cluster.Total)
	}
	if len(perNode) != 0 {
		t.Errorf("empty pods: perNode has %d entries", len(perNode))
	}
}

func TestIsPodReady_NoConditions(t *testing.T) {
	pod := &corev1.Pod{} // no PodReady condition — falls through to return false
	if isPodReady(pod) {
		t.Error("isPodReady: expected false for pod with no conditions")
	}
}

func TestCollectPodCounts_CrashLoopNotCountedAsNotReady(t *testing.T) {
	// A CrashLoopBackOff pod is pending/waiting — not in Running phase.
	// NotReady counter only increments for Running pods that are not Ready.
	pods := []corev1.Pod{
		makePod("p1", "node-a", corev1.PodPending, false, true),
	}
	perNode, cluster := CollectPodCounts(pods)
	nodeA := perNode["node-a"]
	if nodeA.NotReady != 0 {
		t.Errorf("pending crash-loop pod should not increment NotReady, got %d", nodeA.NotReady)
	}
	if cluster.CrashLoop != 1 {
		t.Errorf("cluster.CrashLoop: got %d, want 1", cluster.CrashLoop)
	}
}
