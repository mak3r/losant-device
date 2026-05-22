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
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	losantv1alpha1 "github.com/mak3r/losant-device/api/v1alpha1"
	"github.com/mak3r/losant-device/internal/controller"
	"github.com/mak3r/losant-device/internal/rancher"
)

// baseRS returns a minimal RancherSession with the finalizer already applied
// so tests begin after the first (finalizer-only) reconcile.
func baseRS(name string) *losantv1alpha1.RancherSession {
	return &losantv1alpha1.RancherSession{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Namespace:  "default",
			Finalizers: []string{"losant.io/rancher-cleanup"},
		},
		Spec: losantv1alpha1.RancherSessionSpec{
			CredentialsSecretRef: losantv1alpha1.SecretRef{
				Name:      "rancher-creds",
				Namespace: "default",
			},
			TTLSeconds: 3600,
		},
	}
}

func buildRancherFakeClient(objs ...client.Object) client.Client {
	return fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(objs...).
		WithStatusSubresource(&losantv1alpha1.RancherSession{}).
		Build()
}

func newRancherReconciler(c client.Client, rc rancher.RancherClient) *controller.RancherSessionReconciler {
	return &controller.RancherSessionReconciler{
		Client:        c,
		Scheme:        testScheme,
		RancherClient: rc,
	}
}

func rsReqFor(name string) ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{Name: name, Namespace: "default"}}
}

// newManifestServer starts a TLS server that serves an empty manifest body.
// The returned CA PEM can be placed in a credentials Secret so applyManifest
// will trust it during tests.
func newManifestServer(t *testing.T) (*httptest.Server, []byte) {
	t.Helper()
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)

	x509c, err := x509.ParseCertificate(ts.TLS.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatalf("parse TLS cert: %v", err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: x509c.Raw})
	return ts, caPEM
}

// credsSecretWithCA returns a credentials secret whose RANCHER_CA is the
// provided PEM, suitable for passing to a fake client alongside a test TLS server.
func credsSecretWithCA(url string, caPEM []byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "rancher-creds", Namespace: "default"},
		Data: map[string][]byte{
			"RANCHER_URL":   []byte(url),
			"RANCHER_TOKEN": []byte("test-token"),
			"RANCHER_CA":    caPEM,
		},
	}
}

func getRancherSession(t *testing.T, c client.Client, name string) losantv1alpha1.RancherSession {
	t.Helper()
	var rs losantv1alpha1.RancherSession
	if err := c.Get(context.Background(), types.NamespacedName{Name: name, Namespace: "default"}, &rs); err != nil {
		t.Fatalf("get RancherSession %q: %v", name, err)
	}
	return rs
}

// --- Suspend ---

func TestRancherSessionReconciler_Suspend(t *testing.T) {
	rs := baseRS("session-1")
	rs.Spec.Suspend = true

	mc := rancher.NewMockRancherClient()
	c := buildRancherFakeClient(rs)
	r := newRancherReconciler(c, mc)

	result, err := r.Reconcile(context.Background(), rsReqFor("session-1"))
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.RequeueAfter != 0 || result.Requeue {
		t.Errorf("expected empty result, got %+v", result)
	}
	if len(mc.FindClusterCalls)+len(mc.CreateClusterCalls) > 0 {
		t.Error("Rancher API should not be called when suspended")
	}
}

// --- Secret missing (no injected client) ---

func TestRancherSessionReconciler_SecretMissing(t *testing.T) {
	rs := baseRS("session-1")
	c := buildRancherFakeClient(rs)
	r := newRancherReconciler(c, nil) // nil forces secret lookup

	_, err := r.Reconcile(context.Background(), rsReqFor("session-1"))
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	updated := getRancherSession(t, c, "session-1")
	if updated.Status.Phase != losantv1alpha1.RancherSessionPhaseFailed {
		t.Errorf("Phase: got %q, want Failed", updated.Status.Phase)
	}
}

// --- Connect: create new cluster (happy path) ---

func TestRancherSessionReconciler_ConnectHappyPath(t *testing.T) {
	manifestSrv, caPEM := newManifestServer(t)

	mc := rancher.NewMockRancherClient()
	mc.GetRegistrationTokenFunc = func(_ context.Context, _ string) (string, error) {
		return manifestSrv.URL + "/manifest.yaml", nil
	}

	rs := baseRS("session-1")
	creds := credsSecretWithCA(manifestSrv.URL, caPEM)
	c := buildRancherFakeClient(rs, creds)
	r := newRancherReconciler(c, mc)

	result, err := r.Reconcile(context.Background(), rsReqFor("session-1"))
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	// After manifest applied, reconciler requeues at agentPollInterval (5s).
	if result.RequeueAfter != 5*time.Second {
		t.Errorf("RequeueAfter: got %v, want 5s", result.RequeueAfter)
	}

	updated := getRancherSession(t, c, "session-1")
	if updated.Status.Phase != losantv1alpha1.RancherSessionPhaseConnecting {
		t.Errorf("Phase: got %q, want Connecting", updated.Status.Phase)
	}
	if !updated.Status.ManifestApplied {
		t.Error("ManifestApplied: got false, want true")
	}
	if updated.Status.RancherClusterID != "mock-cluster-id" {
		t.Errorf("RancherClusterID: got %q, want mock-cluster-id", updated.Status.RancherClusterID)
	}
	if len(mc.CreateClusterCalls) != 1 {
		t.Errorf("CreateCluster calls: got %d, want 1", len(mc.CreateClusterCalls))
	}
}

// --- Connect: reuse orphaned cluster ---

func TestRancherSessionReconciler_FindClusterOrphanReuse(t *testing.T) {
	manifestSrv, caPEM := newManifestServer(t)

	mc := rancher.NewMockRancherClient()
	mc.FindClusterFunc = func(_ context.Context, name string) (string, bool, error) {
		return "orphan-cluster-id", true, nil
	}
	mc.GetRegistrationTokenFunc = func(_ context.Context, _ string) (string, error) {
		return manifestSrv.URL + "/manifest.yaml", nil
	}

	rs := baseRS("session-1")
	creds := credsSecretWithCA(manifestSrv.URL, caPEM)
	c := buildRancherFakeClient(rs, creds)
	r := newRancherReconciler(c, mc)

	_, err := r.Reconcile(context.Background(), rsReqFor("session-1"))
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	// Orphan reuse: CreateCluster should NOT be called.
	if len(mc.CreateClusterCalls) != 0 {
		t.Errorf("CreateCluster calls: got %d, want 0", len(mc.CreateClusterCalls))
	}
	updated := getRancherSession(t, c, "session-1")
	if updated.Status.RancherClusterID != "orphan-cluster-id" {
		t.Errorf("RancherClusterID: got %q, want orphan-cluster-id", updated.Status.RancherClusterID)
	}
}

// --- Connect: Rancher API error → Failed + requeue ---

func TestRancherSessionReconciler_ConnectAPIError(t *testing.T) {
	mc := rancher.NewMockRancherClient()
	mc.CreateClusterFunc = func(_ context.Context, _ string) (string, error) {
		return "", fmt.Errorf("rancher api unreachable")
	}

	rs := baseRS("session-1")
	// No credentials secret in fake client — applyManifest won't be reached.
	c := buildRancherFakeClient(rs)
	r := newRancherReconciler(c, mc)

	result, err := r.Reconcile(context.Background(), rsReqFor("session-1"))
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	// API error → setAPIUnreachable → requeue after rancherErrorRequeueWait (30s).
	if result.RequeueAfter != 30*time.Second {
		t.Errorf("RequeueAfter: got %v, want 30s", result.RequeueAfter)
	}
}

// --- Connecting + ManifestApplied=true: agent not yet ready ---

func TestRancherSessionReconciler_PollAgentReady_NoDeployment(t *testing.T) {
	rs := baseRS("session-1")
	rs.Status.Phase = losantv1alpha1.RancherSessionPhaseConnecting
	rs.Status.ManifestApplied = true
	future := metav1.Time{Time: time.Now().Add(2 * time.Minute)}
	rs.Status.ExpiresAt = &future

	mc := rancher.NewMockRancherClient()
	c := buildRancherFakeClient(rs)
	r := newRancherReconciler(c, mc)

	result, err := r.Reconcile(context.Background(), rsReqFor("session-1"))
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.RequeueAfter != 5*time.Second {
		t.Errorf("RequeueAfter: got %v, want 5s", result.RequeueAfter)
	}
	// No Rancher API calls during polling.
	if len(mc.FindClusterCalls)+len(mc.GetClusterCalls) > 0 {
		t.Error("unexpected Rancher API calls during agent poll")
	}
}

// --- Connecting + ManifestApplied=true: agent ready → Connected ---

func TestRancherSessionReconciler_PollAgentReady_AgentReady(t *testing.T) {
	rs := baseRS("session-1")
	rs.Status.Phase = losantv1alpha1.RancherSessionPhaseConnecting
	rs.Status.ManifestApplied = true
	future := metav1.Time{Time: time.Now().Add(2 * time.Minute)}
	rs.Status.ExpiresAt = &future

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "cattle-cluster-agent", Namespace: "cattle-system"},
		Status:     appsv1.DeploymentStatus{AvailableReplicas: 1},
	}

	mc := rancher.NewMockRancherClient()
	c := buildRancherFakeClient(rs, dep)
	r := newRancherReconciler(c, mc)

	_, err := r.Reconcile(context.Background(), rsReqFor("session-1"))
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	updated := getRancherSession(t, c, "session-1")
	if updated.Status.Phase != losantv1alpha1.RancherSessionPhaseConnected {
		t.Errorf("Phase: got %q, want Connected", updated.Status.Phase)
	}
}

// --- Connecting + ManifestApplied=true: agent timeout → Failed ---

func TestRancherSessionReconciler_PollAgentReady_Timeout(t *testing.T) {
	rs := baseRS("session-1")
	rs.Status.Phase = losantv1alpha1.RancherSessionPhaseConnecting
	rs.Status.ManifestApplied = true
	past := metav1.Time{Time: time.Now().Add(-1 * time.Minute)}
	rs.Status.ExpiresAt = &past

	mc := rancher.NewMockRancherClient()
	c := buildRancherFakeClient(rs)
	r := newRancherReconciler(c, mc)

	_, err := r.Reconcile(context.Background(), rsReqFor("session-1"))
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	updated := getRancherSession(t, c, "session-1")
	if updated.Status.Phase != losantv1alpha1.RancherSessionPhaseFailed {
		t.Errorf("Phase: got %q, want Failed", updated.Status.Phase)
	}
}

// --- Connected: TTL expired → cleanup → Disconnected ---

func TestRancherSessionReconciler_TTLExpired(t *testing.T) {
	rs := baseRS("session-1")
	rs.Status.Phase = losantv1alpha1.RancherSessionPhaseConnected
	rs.Status.RancherClusterID = "cluster-abc"
	past := metav1.Time{Time: time.Now().Add(-1 * time.Hour)}
	rs.Status.ExpiresAt = &past

	mc := rancher.NewMockRancherClient()
	c := buildRancherFakeClient(rs)
	r := newRancherReconciler(c, mc)

	_, err := r.Reconcile(context.Background(), rsReqFor("session-1"))
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if len(mc.DeleteClusterCalls) != 1 {
		t.Errorf("DeleteCluster calls: got %d, want 1", len(mc.DeleteClusterCalls))
	}
	if mc.DeleteClusterCalls[0].ClusterID != "cluster-abc" {
		t.Errorf("DeleteCluster clusterID: got %q, want cluster-abc", mc.DeleteClusterCalls[0].ClusterID)
	}

	updated := getRancherSession(t, c, "session-1")
	if updated.Status.Phase != losantv1alpha1.RancherSessionPhaseDisconnected {
		t.Errorf("Phase: got %q, want Disconnected", updated.Status.Phase)
	}
}

// --- Connected: Rancher-initiated disconnect (GetCluster 404) → cleanup ---

func TestRancherSessionReconciler_RancherInitiatedDisconnect(t *testing.T) {
	rs := baseRS("session-1")
	rs.Status.Phase = losantv1alpha1.RancherSessionPhaseConnected
	rs.Status.RancherClusterID = "cluster-abc"
	future := metav1.Time{Time: time.Now().Add(1 * time.Hour)}
	rs.Status.ExpiresAt = &future

	mc := rancher.NewMockRancherClient()
	mc.GetClusterFunc = func(_ context.Context, _ string) error {
		return fmt.Errorf("%w: GET /v3/clusters/cluster-abc", rancher.ErrNotFound)
	}

	c := buildRancherFakeClient(rs)
	r := newRancherReconciler(c, mc)

	_, err := r.Reconcile(context.Background(), rsReqFor("session-1"))
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if len(mc.GetClusterCalls) != 1 {
		t.Errorf("GetCluster calls: got %d, want 1", len(mc.GetClusterCalls))
	}

	updated := getRancherSession(t, c, "session-1")
	if updated.Status.Phase != losantv1alpha1.RancherSessionPhaseDisconnected {
		t.Errorf("Phase: got %q, want Disconnected", updated.Status.Phase)
	}
}

// --- DeletionTimestamp set: finalizer removed after cleanup ---

func TestRancherSessionReconciler_UserInitiatedDisconnect(t *testing.T) {
	rs := baseRS("session-1")
	rs.Status.Phase = losantv1alpha1.RancherSessionPhaseConnected
	rs.Status.RancherClusterID = "cluster-abc"
	now := metav1.Now()
	rs.DeletionTimestamp = &now

	mc := rancher.NewMockRancherClient()
	c := buildRancherFakeClient(rs)
	r := newRancherReconciler(c, mc)

	_, err := r.Reconcile(context.Background(), rsReqFor("session-1"))
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if len(mc.DeleteClusterCalls) != 1 {
		t.Errorf("DeleteCluster calls: got %d, want 1", len(mc.DeleteClusterCalls))
	}

	// After finalizer removal, the fake client may delete the object entirely
	// (simulating the GC's behavior when DeletionTimestamp is set and no finalizers remain).
	var updated losantv1alpha1.RancherSession
	if err := c.Get(context.Background(), types.NamespacedName{Name: "session-1", Namespace: "default"}, &updated); err == nil {
		for _, f := range updated.Finalizers {
			if f == "losant.io/rancher-cleanup" {
				t.Error("finalizer should have been removed")
			}
		}
	}
	// If not found: object was garbage-collected after finalizer removal — expected.
}

// --- DeleteCluster non-404 error: finalizer NOT removed ---

func TestRancherSessionReconciler_DeleteClusterError(t *testing.T) {
	rs := baseRS("session-1")
	rs.Status.Phase = losantv1alpha1.RancherSessionPhaseConnected
	rs.Status.RancherClusterID = "cluster-abc"
	now := metav1.Now()
	rs.DeletionTimestamp = &now

	mc := rancher.NewMockRancherClient()
	mc.DeleteClusterFunc = func(_ context.Context, _ string) error {
		return fmt.Errorf("rancher api error: status 500")
	}

	c := buildRancherFakeClient(rs)
	r := newRancherReconciler(c, mc)

	_, err := r.Reconcile(context.Background(), rsReqFor("session-1"))
	// setAPIUnreachable returns (ctrl.Result{RequeueAfter:30s}, nil), not an error.
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	updated := getRancherSession(t, c, "session-1")
	hasFinalizer := false
	for _, f := range updated.Finalizers {
		if f == "losant.io/rancher-cleanup" {
			hasFinalizer = true
		}
	}
	if !hasFinalizer {
		t.Error("finalizer should be retained when DeleteCluster returns non-404 error")
	}
}

// ensure ErrNotFound wrapping works with errors.Is in tests
var _ = errors.Is(rancher.ErrNotFound, rancher.ErrNotFound)
