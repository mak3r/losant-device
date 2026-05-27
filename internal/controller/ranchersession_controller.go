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
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	losantv1alpha1 "github.com/mak3r/losant-device/api/v1alpha1"
	"github.com/mak3r/losant-device/internal/rancher"
)

const (
	rancherFinalizer             = "losant.io/rancher-cleanup"
	cattleSystemNamespace        = "cattle-system"
	cattleAgentDeployment        = "cattle-cluster-agent"
	agentPollInterval            = 5 * time.Second
	agentReadyTimeout            = 120 * time.Second
	rancherErrorRequeueWait      = 30 * time.Second
	rancherConditionAPIReachable = "RancherAPIReachable"
)

// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=create;get;patch
// +kubebuilder:rbac:groups=core,resources=serviceaccounts,verbs=create;get;patch
// +kubebuilder:rbac:groups=core,resources=namespaces,verbs=create;delete;get;patch
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=create;get;list;patch;update;watch
// +kubebuilder:rbac:groups=apps,resources=daemonsets,verbs=create;get;patch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=create;get;list;patch;watch
// +kubebuilder:rbac:groups=losant.io,resources=ranchersessions,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups=losant.io,resources=ranchersessions/status,verbs=get;patch;update

// RancherSessionReconciler reconciles RancherSession objects.
type RancherSessionReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	RancherClient rancher.RancherClient
}

func (r *RancherSessionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var rs losantv1alpha1.RancherSession
	if err := r.Get(ctx, req.NamespacedName, &rs); err != nil {
		if k8serrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !rs.DeletionTimestamp.IsZero() {
		return r.handleDisconnect(ctx, &rs)
	}

	if controllerutil.AddFinalizer(&rs, rancherFinalizer) {
		if err := r.Update(ctx, &rs); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	if rs.Spec.Suspend {
		logger.Info("reconciliation suspended", "name", rs.Name)
		return ctrl.Result{}, nil
	}

	rc, err := r.rancherClient(ctx, &rs)
	if err != nil {
		return r.setFailed(ctx, &rs, "InvalidSecret", err.Error())
	}

	switch rs.Status.Phase {
	case "":
		return r.handleConnect(ctx, &rs, rc)
	case losantv1alpha1.RancherSessionPhaseConnecting:
		if rs.Status.ManifestApplied {
			return r.pollAgentReady(ctx, &rs)
		}
		return r.handleConnect(ctx, &rs, rc)
	case losantv1alpha1.RancherSessionPhaseConnected:
		return r.handleConnected(ctx, &rs, rc)
	case losantv1alpha1.RancherSessionPhaseFailed:
		return ctrl.Result{}, nil
	case losantv1alpha1.RancherSessionPhaseDisconnected:
		return ctrl.Result{}, nil
	default:
		return ctrl.Result{}, nil
	}
}

// handleConnect runs the full connect sequence from scratch or resumes from
// a Connecting phase where the manifest has not yet been applied.
func (r *RancherSessionReconciler) handleConnect(ctx context.Context, rs *losantv1alpha1.RancherSession, rc rancher.RancherClient) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	rs.Status.Phase = losantv1alpha1.RancherSessionPhaseConnecting
	if err := r.Status().Update(ctx, rs); err != nil {
		return ctrl.Result{}, err
	}

	displayName := rs.Spec.ClusterDisplayName
	if displayName == "" {
		displayName = rs.Name
	}

	// Check for an orphaned cluster from a prior failed disconnect.
	clusterID, found, err := rc.FindCluster(ctx, displayName)
	if err != nil {
		return r.setAPIUnreachable(ctx, rs, "FindClusterFailed", err.Error())
	}
	if !found {
		clusterID, err = rc.CreateCluster(ctx, displayName)
		if err != nil {
			return r.setAPIUnreachable(ctx, rs, "CreateClusterFailed", err.Error())
		}
		logger.Info("created Rancher cluster", "clusterID", clusterID, "name", displayName)
	} else {
		logger.Info("reusing existing Rancher cluster", "clusterID", clusterID, "name", displayName)
	}
	rs.Status.RancherClusterID = clusterID

	manifestURL, err := rc.GetRegistrationToken(ctx, clusterID)
	if err != nil {
		return r.setAPIUnreachable(ctx, rs, "GetTokenFailed", err.Error())
	}

	if err := r.applyManifest(ctx, rs, manifestURL); err != nil {
		return r.setFailed(ctx, rs, "ManifestApplyFailed", err.Error())
	}

	now := metav1.Now()
	rs.Status.ManifestApplied = true
	rs.Status.ConnectedAt = &now
	rs.Status.ExpiresAt = &metav1.Time{Time: now.Add(agentReadyTimeout)}
	setRancherCondition(rs, rancherConditionAPIReachable, metav1.ConditionTrue, "APIReachable", "Rancher API reachable")
	if err := r.Status().Update(ctx, rs); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: agentPollInterval}, nil
}

// pollAgentReady checks whether cattle-cluster-agent is ready and transitions
// to Connected or Failed based on elapsed time.
func (r *RancherSessionReconciler) pollAgentReady(ctx context.Context, rs *losantv1alpha1.RancherSession) (ctrl.Result, error) {
	// Timeout: ConnectedAt holds the manifest-apply timestamp during polling.
	if rs.Status.ExpiresAt != nil && time.Now().After(rs.Status.ExpiresAt.Time) {
		return r.setFailed(ctx, rs, "AgentReadyTimeout",
			fmt.Sprintf("cattle-cluster-agent did not become ready within %s", agentReadyTimeout))
	}

	var dep appsv1.Deployment
	err := r.Get(ctx, types.NamespacedName{
		Namespace: cattleSystemNamespace,
		Name:      cattleAgentDeployment,
	}, &dep)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return ctrl.Result{RequeueAfter: agentPollInterval}, nil
		}
		return ctrl.Result{}, err
	}

	if dep.Status.AvailableReplicas == 0 {
		return ctrl.Result{RequeueAfter: agentPollInterval}, nil
	}

	now := metav1.Now()
	ttl := time.Duration(rs.Spec.TTLSeconds) * time.Second
	rs.Status.Phase = losantv1alpha1.RancherSessionPhaseConnected
	rs.Status.ConnectedAt = &now
	rs.Status.ExpiresAt = &metav1.Time{Time: now.Add(ttl)}
	setRancherCondition(rs, rancherConditionAPIReachable, metav1.ConditionTrue, "Connected", "Rancher session established")
	if err := r.Status().Update(ctx, rs); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: ttl}, nil
}

// handleConnected enforces the TTL and detects Rancher-initiated disconnects.
func (r *RancherSessionReconciler) handleConnected(ctx context.Context, rs *losantv1alpha1.RancherSession, rc rancher.RancherClient) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// TTL enforcement.
	if rs.Status.ExpiresAt != nil && time.Now().After(rs.Status.ExpiresAt.Time) {
		logger.Info("TTL expired, disconnecting", "name", rs.Name)
		return r.runCleanup(ctx, rs, rc)
	}

	// Rancher-initiated disconnect detection.
	if rs.Status.RancherClusterID != "" {
		err := rc.GetCluster(ctx, rs.Status.RancherClusterID)
		if err != nil && errors.Is(err, rancher.ErrNotFound) {
			logger.Info("Rancher cluster deleted externally, cleaning up", "clusterID", rs.Status.RancherClusterID)
			return r.runCleanup(ctx, rs, rc)
		}
		if err != nil {
			return r.setAPIUnreachable(ctx, rs, "GetClusterFailed", err.Error())
		}
	}

	// Requeue at the remaining TTL.
	if rs.Status.ExpiresAt != nil {
		remaining := time.Until(rs.Status.ExpiresAt.Time)
		if remaining > 0 {
			return ctrl.Result{RequeueAfter: remaining}, nil
		}
	}
	return ctrl.Result{}, nil
}

// handleDisconnect runs cleanup on CR deletion (DeletionTimestamp set).
func (r *RancherSessionReconciler) handleDisconnect(ctx context.Context, rs *losantv1alpha1.RancherSession) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(rs, rancherFinalizer) {
		return ctrl.Result{}, nil
	}

	rc, err := r.rancherClient(ctx, rs)
	if err != nil {
		// Cannot build client — still attempt cleanup best-effort.
		log.FromContext(ctx).Error(err, "failed to build Rancher client during disconnect; proceeding with namespace cleanup only")
	}
	if rc != nil {
		if _, err2 := r.runCleanup(ctx, rs, rc); err2 != nil {
			return ctrl.Result{}, err2
		}
		return ctrl.Result{}, nil
	}

	// No Rancher client: only clean up the namespace.
	if err := r.deleteNamespace(ctx, cattleSystemNamespace); err != nil {
		return ctrl.Result{}, err
	}
	return r.removeFinalizer(ctx, rs)
}

// runCleanup performs the Rancher disconnect: deletes the cluster record in Rancher
// and the cattle-system namespace, then transitions to Disconnected.
func (r *RancherSessionReconciler) runCleanup(ctx context.Context, rs *losantv1alpha1.RancherSession, rc rancher.RancherClient) (ctrl.Result, error) {
	rs.Status.Phase = losantv1alpha1.RancherSessionPhaseDisconnecting
	if err := r.Status().Update(ctx, rs); err != nil {
		return ctrl.Result{}, err
	}

	if rs.Status.RancherClusterID != "" {
		if err := rc.DeleteCluster(ctx, rs.Status.RancherClusterID); err != nil {
			return r.setAPIUnreachable(ctx, rs, "DeleteClusterFailed", err.Error())
		}
	}

	if err := r.deleteNamespace(ctx, cattleSystemNamespace); err != nil {
		return ctrl.Result{}, err
	}

	rs.Status.Phase = losantv1alpha1.RancherSessionPhaseDisconnected
	rs.Status.LastTransitionMessage = "session disconnected"
	if err := r.Status().Update(ctx, rs); err != nil {
		return ctrl.Result{}, err
	}

	if !rs.DeletionTimestamp.IsZero() {
		return r.removeFinalizer(ctx, rs)
	}
	return ctrl.Result{}, nil
}

// deleteNamespace deletes a namespace, treating 404 as success.
func (r *RancherSessionReconciler) deleteNamespace(ctx context.Context, name string) error {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if err := r.Delete(ctx, ns); err != nil && !k8serrors.IsNotFound(err) {
		return fmt.Errorf("delete namespace %s: %w", name, err)
	}
	return nil
}

// removeFinalizer removes rancherFinalizer from rs and persists the change.
func (r *RancherSessionReconciler) removeFinalizer(ctx context.Context, rs *losantv1alpha1.RancherSession) (ctrl.Result, error) {
	controllerutil.RemoveFinalizer(rs, rancherFinalizer)
	if err := r.Update(ctx, rs); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// applyManifest fetches the Rancher import manifest from manifestURL and applies
// all resources to the cluster using server-side apply.
func (r *RancherSessionReconciler) applyManifest(ctx context.Context, rs *losantv1alpha1.RancherSession, manifestURL string) error {
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{
		Name:      rs.Spec.CredentialsSecretRef.Name,
		Namespace: rs.Spec.CredentialsSecretRef.Namespace,
	}, secret); err != nil {
		return fmt.Errorf("read credentials secret: %w", err)
	}

	caData := secret.Data["RANCHER_CA"]
	caPool := x509.NewCertPool()
	if len(caData) > 0 {
		caPool.AppendCertsFromPEM(caData)
	}
	hc := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: caPool},
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return fmt.Errorf("build manifest request: %w", err)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("fetch manifest: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("fetch manifest: status %d", resp.StatusCode)
	}

	manifestData, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read manifest body: %w", err)
	}

	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(manifestData), 4096)
	for {
		obj := &unstructured.Unstructured{}
		if err := decoder.Decode(obj); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("decode manifest document: %w", err)
		}
		if obj.GetKind() == "" {
			continue
		}
		gvk := schema.FromAPIVersionAndKind(obj.GetAPIVersion(), obj.GetKind())
		obj.SetGroupVersionKind(gvk)

		if err := r.Patch(ctx, obj, client.Apply,
			client.ForceOwnership,
			client.FieldOwner("losant-ranchersession")); err != nil {
			return fmt.Errorf("apply %s %s/%s: %w",
				obj.GetKind(), obj.GetNamespace(), obj.GetName(), err)
		}
	}
	return nil
}

// rancherClient builds a RancherClient from the session's credentials Secret,
// or returns the injected client if one was provided (used in tests).
func (r *RancherSessionReconciler) rancherClient(ctx context.Context, rs *losantv1alpha1.RancherSession) (rancher.RancherClient, error) {
	if r.RancherClient != nil {
		return r.RancherClient, nil
	}
	var secret corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{
		Name:      rs.Spec.CredentialsSecretRef.Name,
		Namespace: rs.Spec.CredentialsSecretRef.Namespace,
	}, &secret); err != nil {
		return nil, fmt.Errorf("read credentials secret: %w", err)
	}
	return rancher.NewHTTPClient(&secret)
}

// setFailed transitions the session to the Failed phase.
func (r *RancherSessionReconciler) setFailed(ctx context.Context, rs *losantv1alpha1.RancherSession, reason, message string) (ctrl.Result, error) {
	rs.Status.Phase = losantv1alpha1.RancherSessionPhaseFailed
	rs.Status.LastTransitionMessage = message
	setRancherCondition(rs, rancherConditionAPIReachable, metav1.ConditionFalse, reason, message)
	if err := r.Status().Update(ctx, rs); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// setAPIUnreachable sets the RancherAPIReachable condition to False and requeues.
func (r *RancherSessionReconciler) setAPIUnreachable(ctx context.Context, rs *losantv1alpha1.RancherSession, reason, message string) (ctrl.Result, error) {
	setRancherCondition(rs, rancherConditionAPIReachable, metav1.ConditionFalse, reason, message)
	rs.Status.LastTransitionMessage = message
	if err := r.Status().Update(ctx, rs); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: rancherErrorRequeueWait}, nil
}

// setRancherCondition upserts a metav1.Condition on the RancherSession status.
func setRancherCondition(rs *losantv1alpha1.RancherSession, condType string, status metav1.ConditionStatus, reason, message string) {
	apimeta.SetStatusCondition(&rs.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		ObservedGeneration: rs.Generation,
		Reason:             reason,
		Message:            message,
	})
}

// SetupWithManager registers the reconciler with the controller-runtime Manager.
func (r *RancherSessionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&losantv1alpha1.RancherSession{}).
		Named("ranchersession").
		Complete(r)
}
