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

package controller_test

import (
	"context"
	"errors"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/mak3r/losant-device/internal/controller"
	"github.com/mak3r/losant-device/internal/monitor"
)

func buildWatcherClient(objs ...client.Object) client.Client {
	return fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(objs...).
		Build()
}

func buildWatcherClientWithInterceptor(funcs interceptor.Funcs, objs ...client.Object) client.Client {
	return fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(objs...).
		WithInterceptorFuncs(funcs).
		Build()
}

func newWatcherReconciler(c client.Client) (*controller.HealthWatcherReconciler, *monitor.HealthStore) {
	store := monitor.NewHealthStore()
	return &controller.HealthWatcherReconciler{
		Client: c,
		Scheme: testScheme,
		Store:  store,
	}, store
}

func condStatus(b bool) corev1.ConditionStatus {
	if b {
		return corev1.ConditionTrue
	}
	return corev1.ConditionFalse
}

func readyNode(name string) *corev1.Node {
	return nodeObj(name, true, false, false, false)
}

func nodeObj(name string, ready, memPressure, diskPressure, pidPressure bool) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: condStatus(ready)},
				{Type: corev1.NodeMemoryPressure, Status: condStatus(memPressure)},
				{Type: corev1.NodeDiskPressure, Status: condStatus(diskPressure)},
				{Type: corev1.NodePIDPressure, Status: condStatus(pidPressure)},
			},
		},
	}
}

func runningPod(name, nodeName string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       corev1.PodSpec{NodeName: nodeName},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: corev1.ConditionTrue},
			},
		},
	}
}

func crashLoopPod(name, nodeName string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       corev1.PodSpec{NodeName: nodeName},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"},
					},
				},
			},
		},
	}
}

// TestHealthWatcher_NodeNotReady verifies that a node with Ready=False is
// reflected as Ready=false in the HealthStore snapshot.
func TestHealthWatcher_NodeNotReady(t *testing.T) {
	node := nodeObj("worker-1", false, false, false, false)
	c := buildWatcherClient(node)
	r, store := newWatcherReconciler(c)

	if _, err := r.Reconcile(context.Background(), ctrl.Request{}); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	_, nodes := store.Snapshot()
	nh, ok := nodes["worker-1"]
	if !ok {
		t.Fatal("worker-1 missing from snapshot")
	}
	if nh.Ready {
		t.Error("Ready: got true, want false")
	}
}

// TestHealthWatcher_NodeConditionFlags verifies that MemoryPressure, DiskPressure,
// and PIDPressure conditions are correctly reflected in the HealthStore.
func TestHealthWatcher_NodeConditionFlags(t *testing.T) {
	node := nodeObj("worker-1", true, true, true, true)
	c := buildWatcherClient(node)
	r, store := newWatcherReconciler(c)

	if _, err := r.Reconcile(context.Background(), ctrl.Request{}); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	_, nodes := store.Snapshot()
	nh := nodes["worker-1"]
	if !nh.MemoryPressure {
		t.Error("MemoryPressure: got false, want true")
	}
	if !nh.DiskPressure {
		t.Error("DiskPressure: got false, want true")
	}
	if !nh.PIDPressure {
		t.Error("PIDPressure: got false, want true")
	}
}

// TestHealthWatcher_PodCountsPerNode verifies that pods scheduled on a node
// are counted in that node's HealthStore entry.
func TestHealthWatcher_PodCountsPerNode(t *testing.T) {
	node := readyNode("worker-1")
	pod1 := runningPod("pod-1", "worker-1")
	pod2 := runningPod("pod-2", "worker-1")
	c := buildWatcherClient(node, pod1, pod2)
	r, store := newWatcherReconciler(c)

	if _, err := r.Reconcile(context.Background(), ctrl.Request{}); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	_, nodes := store.Snapshot()
	nh := nodes["worker-1"]
	if nh.PodCount != 2 {
		t.Errorf("PodCount: got %d, want 2", nh.PodCount)
	}
}

// TestHealthWatcher_CrashLoopPods verifies that a CrashLoopBackOff container
// increments CrashLoopPods in both the node and cluster snapshots.
func TestHealthWatcher_CrashLoopPods(t *testing.T) {
	node := readyNode("worker-1")
	pod := crashLoopPod("bad-pod", "worker-1")
	c := buildWatcherClient(node, pod)
	r, store := newWatcherReconciler(c)

	if _, err := r.Reconcile(context.Background(), ctrl.Request{}); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	cluster, nodes := store.Snapshot()
	if nodes["worker-1"].CrashLoopPods != 1 {
		t.Errorf("NodeHealth.CrashLoopPods: got %d, want 1", nodes["worker-1"].CrashLoopPods)
	}
	if cluster.CrashLoopPods != 1 {
		t.Errorf("ClusterHealth.CrashLoopPods: got %d, want 1", cluster.CrashLoopPods)
	}
}

// TestHealthWatcher_WarningEvents verifies that a recent Warning event is
// reflected in the EventWarnings cluster counter.
func TestHealthWatcher_WarningEvents(t *testing.T) {
	node := readyNode("worker-1")
	event := &corev1.Event{
		ObjectMeta:    metav1.ObjectMeta{Name: "warn-1", Namespace: "default"},
		Type:          corev1.EventTypeWarning,
		LastTimestamp: metav1.Now(),
	}
	c := buildWatcherClient(node, event)
	r, store := newWatcherReconciler(c)

	if _, err := r.Reconcile(context.Background(), ctrl.Request{}); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	cluster, _ := store.Snapshot()
	if cluster.EventWarnings != 1 {
		t.Errorf("EventWarnings: got %d, want 1", cluster.EventWarnings)
	}
}

// TestHealthWatcher_DegradedPVC verifies that a non-Bound PVC is counted in
// the DegradedPVCs cluster counter.
func TestHealthWatcher_DegradedPVC(t *testing.T) {
	node := readyNode("worker-1")
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "pvc-1", Namespace: "default"},
		Status:     corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimPending},
	}
	c := buildWatcherClient(node, pvc)
	r, store := newWatcherReconciler(c)

	if _, err := r.Reconcile(context.Background(), ctrl.Request{}); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	cluster, _ := store.Snapshot()
	if cluster.DegradedPVCs != 1 {
		t.Errorf("DegradedPVCs: got %d, want 1", cluster.DegradedPVCs)
	}
}

// TestHealthWatcher_CoreDNSNotHealthy verifies that a CoreDNS deployment with
// zero available replicas sets CoreDNSHealthy=false in the cluster snapshot.
func TestHealthWatcher_CoreDNSNotHealthy(t *testing.T) {
	node := readyNode("worker-1")
	coreDNS := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "coredns", Namespace: "kube-system"},
		Status:     appsv1.DeploymentStatus{AvailableReplicas: 0},
	}
	c := buildWatcherClient(node, coreDNS)
	r, store := newWatcherReconciler(c)

	if _, err := r.Reconcile(context.Background(), ctrl.Request{}); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	cluster, _ := store.Snapshot()
	if cluster.CoreDNSHealthy {
		t.Error("CoreDNSHealthy: got true, want false when AvailableReplicas=0")
	}
}

// TestHealthWatcher_EmptyCluster verifies that reconciling against an empty
// cluster (no objects) returns no error.
func TestHealthWatcher_EmptyCluster(t *testing.T) {
	c := buildWatcherClient()
	r, _ := newWatcherReconciler(c)

	_, err := r.Reconcile(context.Background(), ctrl.Request{})
	if err != nil {
		t.Errorf("expected no error for empty cluster, got: %v", err)
	}
}

// listErrFor returns an interceptor.Funcs that fails the List call for the
// given object list type and delegates all other calls to the underlying client.
func listErrFor[T client.ObjectList](listErr error) interceptor.Funcs {
	return interceptor.Funcs{
		List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if _, ok := list.(T); ok {
				return listErr
			}
			return cl.List(ctx, list, opts...)
		},
	}
}

// TestHealthWatcher_ListNodesError verifies that a failure to list Nodes is
// surfaced as a reconcile error.
func TestHealthWatcher_ListNodesError(t *testing.T) {
	listErr := errors.New("nodes list failed")
	c := buildWatcherClientWithInterceptor(listErrFor[*corev1.NodeList](listErr))
	r, _ := newWatcherReconciler(c)

	_, err := r.Reconcile(context.Background(), ctrl.Request{})
	if !errors.Is(err, listErr) {
		t.Errorf("expected node list error, got: %v", err)
	}
}

// TestHealthWatcher_ListPodsError verifies that a failure to list Pods is
// surfaced as a reconcile error.
func TestHealthWatcher_ListPodsError(t *testing.T) {
	listErr := errors.New("pods list failed")
	c := buildWatcherClientWithInterceptor(listErrFor[*corev1.PodList](listErr))
	r, _ := newWatcherReconciler(c)

	_, err := r.Reconcile(context.Background(), ctrl.Request{})
	if !errors.Is(err, listErr) {
		t.Errorf("expected pod list error, got: %v", err)
	}
}

// TestHealthWatcher_ListPVCsError verifies that a failure to list
// PersistentVolumeClaims is surfaced as a reconcile error.
func TestHealthWatcher_ListPVCsError(t *testing.T) {
	listErr := errors.New("pvcs list failed")
	c := buildWatcherClientWithInterceptor(listErrFor[*corev1.PersistentVolumeClaimList](listErr))
	r, _ := newWatcherReconciler(c)

	_, err := r.Reconcile(context.Background(), ctrl.Request{})
	if !errors.Is(err, listErr) {
		t.Errorf("expected pvc list error, got: %v", err)
	}
}

// TestHealthWatcher_ListEventsError verifies that a failure to list Events is
// surfaced as a reconcile error.
func TestHealthWatcher_ListEventsError(t *testing.T) {
	listErr := errors.New("events list failed")
	c := buildWatcherClientWithInterceptor(listErrFor[*corev1.EventList](listErr))
	r, _ := newWatcherReconciler(c)

	_, err := r.Reconcile(context.Background(), ctrl.Request{})
	if !errors.Is(err, listErr) {
		t.Errorf("expected event list error, got: %v", err)
	}
}
