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

package controller

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/mak3r/losant-device/internal/monitor"
)

const (
	coreDNSNamespace  = "kube-system"
	coreDNSDeployment = "coredns"
)

// HealthWatcherReconciler watches cluster resources and keeps the HealthStore current.
// It never calls Losant — that is LosantSyncReconciler's responsibility.
type HealthWatcherReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Store  *monitor.HealthStore
}

// +kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;get;list;patch;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch

// Reconcile is triggered by changes to any watched resource. It re-computes
// the full cluster health snapshot and writes it to the HealthStore.
func (r *HealthWatcherReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// --- Collect nodes ---
	var nodeList corev1.NodeList
	if err := r.List(ctx, &nodeList); err != nil {
		return ctrl.Result{}, err
	}

	// --- Collect pods (all namespaces) ---
	var podList corev1.PodList
	if err := r.List(ctx, &podList); err != nil {
		return ctrl.Result{}, err
	}

	// --- Collect PVCs (all namespaces) ---
	var pvcList corev1.PersistentVolumeClaimList
	if err := r.List(ctx, &pvcList); err != nil {
		return ctrl.Result{}, err
	}

	// --- Collect Events (all namespaces) ---
	var eventList corev1.EventList
	if err := r.List(ctx, &eventList); err != nil {
		return ctrl.Result{}, err
	}

	// --- Collect CoreDNS deployment ---
	var coreDNS appsv1.Deployment
	var coreDNSPtr *appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Namespace: coreDNSNamespace, Name: coreDNSDeployment}, &coreDNS); err == nil {
		coreDNSPtr = &coreDNS
	}

	// --- Build per-node pod counts ---
	perNodePods, clusterPods := monitor.CollectPodCounts(podList.Items)

	// --- Build NodeHealth map ---
	nodeHealthMap := make(map[string]monitor.NodeHealth, len(nodeList.Items))
	for _, node := range nodeList.Items {
		nh := monitor.CollectNodeHealth(node)
		if pc, ok := perNodePods[node.Name]; ok {
			nh.PodCount = pc.Total
			nh.NotReadyPods = pc.NotReady
			nh.CrashLoopPods = pc.CrashLoop
			// Recompute score now that pod data is present.
			nh.HealthScore = monitor.ComputeNodeHealthScore(nh)
			nh.HealthStatus = monitor.HealthStatusFromScore(nh.HealthScore)
		}
		nodeHealthMap[node.Name] = nh
	}

	// --- Build ClusterHealth ---
	readyNodes := 0
	for _, nh := range nodeHealthMap {
		if nh.Ready {
			readyNodes++
		}
	}

	cluster := monitor.ClusterHealth{
		TotalNodes:     len(nodeList.Items),
		ReadyNodes:     readyNodes,
		UnhealthyNodes: len(nodeList.Items) - readyNodes,
		TotalPods:      clusterPods.Total,
		RunningPods:    clusterPods.Running,
		FailedPods:     clusterPods.Failed,
		PendingPods:    clusterPods.Pending,
		CrashLoopPods:  clusterPods.CrashLoop,
		DegradedPVCs:   monitor.CountDegradedPVCs(pvcList.Items),
		CoreDNSHealthy: monitor.IsCoreDNSHealthy(coreDNSPtr),
		EventWarnings:  monitor.CountRecentWarnings(eventList.Items),
	}
	cluster.HealthScore = monitor.ComputeClusterHealthScore(cluster, nodeHealthMap)
	cluster.HealthStatus = monitor.HealthStatusFromScore(cluster.HealthScore)

	r.Store.Update(cluster, nodeHealthMap)

	logger.V(1).Info("health store updated",
		"trigger", req.NamespacedName,
		"nodes", cluster.TotalNodes,
		"readyNodes", cluster.ReadyNodes,
		"healthScore", cluster.HealthScore,
		"healthStatus", cluster.HealthStatus,
	)

	return ctrl.Result{}, nil
}

// SetupWithManager registers the HealthWatcherReconciler and adds watches for
// all resource types that affect cluster health. Any change to these resources
// re-enqueues every current Node so the full snapshot is recomputed.
func (r *HealthWatcherReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// allNodesMapper re-enqueues every Node whenever a Pod, PVC, Event, or
	// Deployment changes so the HealthStore always reflects current state.
	allNodesMapper := func(ctx context.Context, _ client.Object) []reconcile.Request {
		var nodeList corev1.NodeList
		if err := mgr.GetClient().List(ctx, &nodeList); err != nil {
			return nil
		}
		reqs := make([]reconcile.Request, len(nodeList.Items))
		for i, n := range nodeList.Items {
			reqs[i] = reconcile.Request{
				NamespacedName: types.NamespacedName{Name: n.Name},
			}
		}
		return reqs
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Node{}).
		Watches(&corev1.Pod{}, handler.EnqueueRequestsFromMapFunc(allNodesMapper)).
		Watches(&corev1.PersistentVolumeClaim{}, handler.EnqueueRequestsFromMapFunc(allNodesMapper)).
		Watches(&corev1.Event{}, handler.EnqueueRequestsFromMapFunc(allNodesMapper)).
		Watches(&appsv1.Deployment{}, handler.EnqueueRequestsFromMapFunc(allNodesMapper)).
		Complete(r)
}
