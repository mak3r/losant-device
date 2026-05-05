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
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	losantv1alpha1 "github.com/mak3r/losant-device/api/v1alpha1"
	"github.com/mak3r/losant-device/internal/gea"
	"github.com/mak3r/losant-device/internal/losant"
	"github.com/mak3r/losant-device/internal/monitor"
	"github.com/mak3r/losant-device/internal/provisioner"
)

const requeueOnDegradedFallback = time.Minute

// LosantSyncReconciler reconciles a LosantSync object.
type LosantSyncReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	LosantClient losant.LosantClient
	GEAClient    gea.GEAClient
	HealthStore  *monitor.HealthStore
}

// +kubebuilder:rbac:groups=losant.io,resources=losantsyncs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=losant.io,resources=losantsyncs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update
// +kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;get;list;patch;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;patch

func (r *LosantSyncReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var ls losantv1alpha1.LosantSync
	if err := r.Get(ctx, req.NamespacedName, &ls); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Deletion: run device cleanup before removing finalizer.
	if !ls.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, &ls)
	}

	// Add finalizer on first non-deleted reconcile. Explicit requeue is required
	// because GenerationChangedPredicate filters out metadata-only watch events
	// (finalizer writes do not bump .metadata.generation), so without Requeue the
	// reconciler would stall permanently after this Update.
	if controllerutil.AddFinalizer(&ls, "losant.io/device-cleanup") {
		if err := r.Update(ctx, &ls); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Suspend: halt all reconciliation.
	if ls.Spec.Suspend {
		logger.Info("reconciliation suspended", "name", ls.Name)
		if ls.Status.Phase != losantv1alpha1.PhaseSuspended {
			ls.Status.Phase = losantv1alpha1.PhaseSuspended
			if err := r.Status().Update(ctx, &ls); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// First reconcile: set phase to Provisioning and create credential placeholder.
	if ls.Status.Phase == "" {
		logger.Info("first reconcile, setting phase to Provisioning", "name", ls.Name)
		ls.Status.Phase = losantv1alpha1.PhaseProvisioning
		ls.Status.NextScheduledTime = &metav1.Time{Time: time.Now()}
		if err := r.Status().Update(ctx, &ls); err != nil {
			return ctrl.Result{}, err
		}
		// Create an empty Secret so the GEA pod can start before provisioning completes.
		// Non-fatal: bootstrap will create it during provisioning if this fails.
		if err := provisioner.EnsureCredentialPlaceholder(ctx, r.Client, ls.Spec.ProvisioningSecretRef.Namespace); err != nil {
			logger.Error(err, "failed to ensure GEA credential placeholder")
		}
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	// Schedule check: only defer when Active and next sync is still in the future.
	// Degraded and Provisioning phases must proceed immediately on each reconcile.
	if ls.Status.Phase == losantv1alpha1.PhaseActive &&
		ls.Status.NextScheduledTime != nil && time.Now().Before(ls.Status.NextScheduledTime.Time) {
		wait := time.Until(ls.Status.NextScheduledTime.Time)
		logger.V(1).Info("next sync not yet due", "name", ls.Name, "in", wait.Round(time.Second))
		return ctrl.Result{RequeueAfter: wait}, nil
	}

	logger.Info("sync due, starting provisioning and reporting", "name", ls.Name)

	// Step 1: read the provisioning Secret.
	var secret corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{
		Name:      ls.Spec.ProvisioningSecretRef.Name,
		Namespace: ls.Spec.ProvisioningSecretRef.Namespace,
	}, &secret); err != nil {
		logger.Error(err, "failed to read provisioning secret")
		return r.setDegraded(ctx, &ls, "LastSyncSucceeded", "SecretNotFound", err.Error())
	}

	// Step 2: build clients (injected in tests; constructed from spec in production).
	lc := r.LosantClient
	if lc == nil {
		var err error
		lc, err = losant.NewHTTPClient(&secret, ls.Spec.ApplicationID)
		if err != nil {
			logger.Error(err, "failed to construct Losant client from secret")
			return r.setDegraded(ctx, &ls, "LastSyncSucceeded", "InvalidSecret", err.Error())
		}
	}
	gc := r.GEAClient
	if gc == nil {
		gc = gea.NewHTTPClient(ls.Spec.GEA)
	}

	// Step 3: verify Losant credentials via token exchange.
	if err := lc.Ping(ctx); err != nil {
		logger.Error(err, "Losant ping failed")
		return r.setDegraded(ctx, &ls, "LastSyncSucceeded", "LosantUnreachable", err.Error())
	}

	// Step 4: ensure the cluster Edge Compute device exists in Losant.
	clusterDeviceID, err := lc.EnsureClusterDevice(ctx, ls.Spec)
	if err != nil {
		logger.Error(err, "failed to ensure cluster device")
		return r.setDegraded(ctx, &ls, "DevicesProvisioned", "ClusterDeviceError", err.Error())
	}
	if ls.Status.ClusterDeviceID != "" && ls.Status.ClusterDeviceID != clusterDeviceID {
		logger.Info("cluster identity changed: old device is now orphaned in Losant; update clusterName is effectively a destructive operation",
			"oldDeviceID", ls.Status.ClusterDeviceID, "newDeviceID", clusterDeviceID)
	}
	ls.Status.ClusterDeviceID = clusterDeviceID

	// Step 4.5: one-time GEA credential bootstrap (idempotent; skipped once GEABootstrapped=True).
	// Falls through on success so the GEA connectivity check in Step 6 validates immediately.
	if !apimeta.IsStatusConditionTrue(ls.Status.Conditions, "GEABootstrapped") {
		b := &provisioner.GEABootstrapper{
			Client:       r.Client,
			LosantClient: lc,
		}
		if err := b.Bootstrap(ctx, &ls, clusterDeviceID); err != nil {
			logger.Error(err, "GEA bootstrap failed")
			return r.setDegraded(ctx, &ls, "GEABootstrapped", "BootstrapFailed", err.Error())
		}
		setCondition(&ls, "GEABootstrapped", metav1.ConditionTrue, "BootstrapComplete", "GEA credentials provisioned")
	}

	// Step 5: ensure peripheral devices exist for every node in the health snapshot.
	clusterSnapshot, nodeSnapshot := r.healthSnapshot()
	if ls.Status.NodeDevices == nil {
		ls.Status.NodeDevices = make(map[string]string)
	}
	for nodeName := range nodeSnapshot {
		nodeDeviceID, nodeErr := lc.EnsureNodeDevice(ctx, ls.Spec, nodeName, clusterDeviceID)
		if nodeErr != nil {
			logger.Error(nodeErr, "failed to ensure node device", "node", nodeName)
			return r.setDegraded(ctx, &ls, "DevicesProvisioned", "NodeDeviceError",
				fmt.Sprintf("node %s: %v", nodeName, nodeErr))
		}
		ls.Status.NodeDevices[nodeName] = nodeDeviceID
	}
	setCondition(&ls, "DevicesProvisioned", metav1.ConditionTrue, "Provisioned", "all devices confirmed in Losant")

	// Step 6: report cluster state to GEA.
	if err := gc.ReportState(ctx, gea.StatePayload{
		DeviceID:   clusterDeviceID,
		Attributes: clusterAttributes(clusterSnapshot),
	}); err != nil {
		logger.Info("GEA unreachable, will retry", "error", err.Error())
		return r.setDegraded(ctx, &ls, "GEAReachable", "GEAUnreachable", err.Error())
	}
	setCondition(&ls, "GEAReachable", metav1.ConditionTrue, "Reachable", "GEA accepted cluster state")

	// Step 7: report per-node state to GEA (best-effort; failures are logged but not fatal).
	for nodeName, nh := range nodeSnapshot {
		nodeDeviceID := ls.Status.NodeDevices[nodeName]
		if nodeDeviceID == "" {
			continue
		}
		if err := gc.ReportState(ctx, gea.StatePayload{
			DeviceID:   nodeDeviceID,
			Attributes: nodeAttributes(nh),
		}); err != nil {
			logger.Error(err, "failed to report node state to GEA (non-fatal)", "node", nodeName)
		}
	}

	// Step 8: compute next scheduled time.
	next, _, err := nextScheduleAndDuration(ls.Spec)
	if err != nil {
		logger.Error(err, "failed to compute next schedule")
		return r.setDegraded(ctx, &ls, "LastSyncSucceeded", "ScheduleError", err.Error())
	}

	// Step 9: all steps succeeded — mark Active.
	now := metav1.Now()
	ls.Status.LastSyncTime = &now
	ls.Status.NextScheduledTime = &metav1.Time{Time: next}
	ls.Status.Phase = losantv1alpha1.PhaseActive
	setCondition(&ls, "LastSyncSucceeded", metav1.ConditionTrue, "SyncComplete", "sync completed successfully")

	if err := r.Status().Update(ctx, &ls); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("sync complete", "name", ls.Name, "nextSync", next.Round(time.Second))
	return ctrl.Result{RequeueAfter: time.Until(next)}, nil
}

// handleDeletion deletes all Losant devices registered by this CR, then removes the finalizer.
func (r *LosantSyncReconciler) handleDeletion(ctx context.Context, ls *losantv1alpha1.LosantSync) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(ls, "losant.io/device-cleanup") {
		return ctrl.Result{}, nil
	}

	var secret corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{
		Name:      ls.Spec.ProvisioningSecretRef.Name,
		Namespace: ls.Spec.ProvisioningSecretRef.Namespace,
	}, &secret); err != nil {
		return ctrl.Result{}, fmt.Errorf("deletion: read provisioning secret: %w", err)
	}

	lc := r.LosantClient
	if lc == nil {
		var err error
		lc, err = losant.NewHTTPClient(&secret, ls.Spec.ApplicationID)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("deletion: construct losant client: %w", err)
		}
	}

	for nodeName, nodeDeviceID := range ls.Status.NodeDevices {
		if err := lc.DeleteDevice(ctx, ls.Spec.ApplicationID, nodeDeviceID); err != nil {
			logger.Error(err, "failed to delete node device", "node", nodeName, "deviceID", nodeDeviceID)
			return ctrl.Result{}, err
		}
	}

	if ls.Status.ClusterDeviceID != "" {
		if err := lc.DeleteDevice(ctx, ls.Spec.ApplicationID, ls.Status.ClusterDeviceID); err != nil {
			logger.Error(err, "failed to delete cluster device", "deviceID", ls.Status.ClusterDeviceID)
			return ctrl.Result{}, err
		}
	}

	controllerutil.RemoveFinalizer(ls, "losant.io/device-cleanup")
	return ctrl.Result{}, r.Update(ctx, ls)
}

// SetupWithManager registers the controller with the manager.
func (r *LosantSyncReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&losantv1alpha1.LosantSync{},
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
}

// setDegraded sets the given condition to False, phase to Degraded, advances
// NextScheduledTime by spec.interval (so the user can see when the next retry
// will happen), updates status, and requeues for that duration.
func (r *LosantSyncReconciler) setDegraded(
	ctx context.Context,
	ls *losantv1alpha1.LosantSync,
	condType, reason, message string,
) (ctrl.Result, error) {
	ls.Status.Phase = losantv1alpha1.PhaseDegraded
	setCondition(ls, condType, metav1.ConditionFalse, reason, message)
	setCondition(ls, "LastSyncSucceeded", metav1.ConditionFalse, reason, message)

	requeueAfter := requeueOnDegradedFallback
	if next, d, err := nextScheduleAndDuration(ls.Spec); err == nil {
		ls.Status.NextScheduledTime = &metav1.Time{Time: next}
		requeueAfter = d
	}

	if err := r.Status().Update(ctx, ls); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// healthSnapshot returns the current health state from the store, or empty structs if unset.
func (r *LosantSyncReconciler) healthSnapshot() (monitor.ClusterHealth, map[string]monitor.NodeHealth) {
	if r.HealthStore == nil {
		return monitor.ClusterHealth{}, map[string]monitor.NodeHealth{}
	}
	return r.HealthStore.Snapshot()
}

// nextScheduleAndDuration computes the next sync time and the requeue duration
// from spec's CronSchedule or Interval. For cron specs the duration is
// time.Until(next); for interval specs the duration is the parsed interval
// (exact, not subject to sub-millisecond drift).
func nextScheduleAndDuration(spec losantv1alpha1.LosantSyncSpec) (time.Time, time.Duration, error) {
	if spec.CronSchedule != "" {
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		schedule, err := parser.Parse(spec.CronSchedule)
		if err != nil {
			return time.Time{}, 0, fmt.Errorf("invalid cron expression %q: %w", spec.CronSchedule, err)
		}
		next := schedule.Next(time.Now())
		return next, time.Until(next), nil
	}
	d, err := time.ParseDuration(spec.Interval)
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("invalid interval %q: %w", spec.Interval, err)
	}
	return time.Now().Add(d), d, nil
}

// setCondition upserts a metav1.Condition on the LosantSync status.
func setCondition(ls *losantv1alpha1.LosantSync, condType string, status metav1.ConditionStatus, reason, message string) {
	apimeta.SetStatusCondition(&ls.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		ObservedGeneration: ls.Generation,
		Reason:             reason,
		Message:            message,
	})
}

// clusterAttributes maps ClusterHealth fields to Losant attribute names.
func clusterAttributes(h monitor.ClusterHealth) map[string]interface{} {
	return map[string]interface{}{
		"health_score":    h.HealthScore,
		"health_status":   h.HealthStatus,
		"total_nodes":     h.TotalNodes,
		"ready_nodes":     h.ReadyNodes,
		"unhealthy_nodes": h.UnhealthyNodes,
		"total_pods":      h.TotalPods,
		"running_pods":    h.RunningPods,
		"failed_pods":     h.FailedPods,
		"pending_pods":    h.PendingPods,
		"crashloop_pods":  h.CrashLoopPods,
		"degraded_pvcs":   h.DegradedPVCs,
		"coredns_healthy": h.CoreDNSHealthy,
		"event_warnings":  h.EventWarnings,
	}
}

// nodeAttributes maps NodeHealth fields to Losant attribute names.
func nodeAttributes(h monitor.NodeHealth) map[string]interface{} {
	return map[string]interface{}{
		"health_score":    h.HealthScore,
		"health_status":   h.HealthStatus,
		"ready":           h.Ready,
		"memory_pressure": h.MemoryPressure,
		"disk_pressure":   h.DiskPressure,
		"pid_pressure":    h.PIDPressure,
		"pod_count":       h.PodCount,
		"not_ready_pods":  h.NotReadyPods,
		"crashloop_pods":  h.CrashLoopPods,
		"cpu_request_pct": h.CPURequestPct,
		"mem_request_pct": h.MemRequestPct,
	}
}
