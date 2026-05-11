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
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	losantv1alpha1 "github.com/mak3r/losant-device/api/v1alpha1"
	"github.com/mak3r/losant-device/internal/controller"
	"github.com/mak3r/losant-device/internal/gea"
	"github.com/mak3r/losant-device/internal/losant"
	"github.com/mak3r/losant-device/internal/monitor"
)

var testScheme = func() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = losantv1alpha1.AddToScheme(s)
	return s
}()

func baseLS(name string) *losantv1alpha1.LosantSync {
	return &losantv1alpha1.LosantSync{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Finalizers: []string{"losant.io/device-cleanup"},
		},
		Spec: losantv1alpha1.LosantSyncSpec{
			ApplicationID: "app-123",
			ClusterName:   "test-cluster",
			Region:        "us-east",
			Interval:      "1m",
			ProvisioningSecretRef: losantv1alpha1.SecretRef{
				Name:      "losant-creds",
				Namespace: "default",
			},
			GEA: losantv1alpha1.GEASpec{ServiceRef: "losant-gea", Port: 8080},
		},
	}
}

func credsSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "losant-creds", Namespace: "default"},
		Data:       map[string][]byte{"accessKey": []byte("k"), "accessSecret": []byte("s")},
	}
}

func buildClient(objs ...client.Object) client.Client {
	return fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(objs...).
		WithStatusSubresource(&losantv1alpha1.LosantSync{}).
		Build()
}

func newReconciler(c client.Client, lc losant.LosantClient, gc gea.GEAClient, hs *monitor.HealthStore) *controller.LosantSyncReconciler {
	return &controller.LosantSyncReconciler{
		Client:       c,
		Scheme:       testScheme,
		LosantClient: lc,
		GEAClient:    gc,
		HealthStore:  hs,
	}
}

func reqFor(name string) ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{Name: name}}
}

// reconcileTwice drives the reconciler through the first (Provisioning) cycle and
// then through the full sync cycle.  It is the standard setup for tests that want
// to exercise sync-phase behaviour.
func reconcileTwice(t *testing.T, r *controller.LosantSyncReconciler, req ctrl.Request) (ctrl.Result, error) {
	t.Helper()
	ctx := context.Background()
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("first reconcile error: %v", err)
	}
	return r.Reconcile(ctx, req)
}

// TestHappyPath verifies the provisioning happy path: the reconciler transitions
// Provisioning → Active, sets ClusterDeviceID, and populates NodeDevices.
func TestHappyPath(t *testing.T) {
	ls := baseLS("happy")
	hs := monitor.NewHealthStore()
	hs.Update(monitor.ClusterHealth{}, map[string]monitor.NodeHealth{"node-1": {}})
	ml := losant.NewMockClient()
	mg := gea.NewMockClient()
	c := buildClient(ls, credsSecret())
	r := newReconciler(c, ml, mg, hs)

	_, err := reconcileTwice(t, r, reqFor(ls.Name))
	if err != nil {
		t.Fatalf("second reconcile error: %v", err)
	}

	var got losantv1alpha1.LosantSync
	if err := c.Get(context.Background(), reqFor(ls.Name).NamespacedName, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != losantv1alpha1.PhaseActive {
		t.Errorf("Phase: got %q, want Active", got.Status.Phase)
	}
	if got.Status.ClusterDeviceID != "mock-cluster-device-id" {
		t.Errorf("ClusterDeviceID: got %q, want mock-cluster-device-id", got.Status.ClusterDeviceID)
	}
	if got.Status.NodeDevices["node-1"] != "mock-node-node-1" {
		t.Errorf("NodeDevices[node-1]: got %q, want mock-node-node-1", got.Status.NodeDevices["node-1"])
	}
}

// TestPingFailure verifies that a Ping error sets Phase=Degraded and
// does not proceed to device provisioning.
func TestPingFailure(t *testing.T) {
	ls := baseLS("ping-fail")
	ml := losant.NewMockClient()
	ml.PingFunc = func(_ context.Context) error { return errors.New("auth failed") }
	mg := gea.NewMockClient()
	c := buildClient(ls, credsSecret())
	r := newReconciler(c, ml, mg, monitor.NewHealthStore())

	result, err := reconcileTwice(t, r, reqFor(ls.Name))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter != time.Minute {
		t.Errorf("RequeueAfter: got %v, want 1m", result.RequeueAfter)
	}

	var got losantv1alpha1.LosantSync
	if err := c.Get(context.Background(), reqFor(ls.Name).NamespacedName, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != losantv1alpha1.PhaseDegraded {
		t.Errorf("Phase: got %q, want Degraded", got.Status.Phase)
	}
	if len(ml.EnsureClusterDeviceCalls) != 0 {
		t.Errorf("EnsureClusterDevice should not be called on Ping failure, got %d calls", len(ml.EnsureClusterDeviceCalls))
	}
	if !conditionFalse(got.Status.Conditions, "LastSyncSucceeded") {
		t.Error("expected LastSyncSucceeded=False condition")
	}
}

// TestEnsureClusterDeviceFailure verifies that a cluster device provisioning error
// sets Phase=Degraded and DevicesProvisioned=False.
func TestEnsureClusterDeviceFailure(t *testing.T) {
	ls := baseLS("cluster-fail")
	ml := losant.NewMockClient()
	ml.EnsureClusterDeviceFunc = func(_ context.Context, _ losantv1alpha1.LosantSyncSpec) (string, error) {
		return "", errors.New("api error")
	}
	mg := gea.NewMockClient()
	c := buildClient(ls, credsSecret())
	r := newReconciler(c, ml, mg, monitor.NewHealthStore())

	_, err := reconcileTwice(t, r, reqFor(ls.Name))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var got losantv1alpha1.LosantSync
	if err := c.Get(context.Background(), reqFor(ls.Name).NamespacedName, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != losantv1alpha1.PhaseDegraded {
		t.Errorf("Phase: got %q, want Degraded", got.Status.Phase)
	}
	if !conditionFalse(got.Status.Conditions, "DevicesProvisioned") {
		t.Error("expected DevicesProvisioned=False condition")
	}
}

// TestEnsureNodeDeviceFailure verifies that a node device provisioning error
// sets Phase=Degraded and does not persist partial NodeDevices.
func TestEnsureNodeDeviceFailure(t *testing.T) {
	ls := baseLS("node-fail")
	hs := monitor.NewHealthStore()
	hs.Update(monitor.ClusterHealth{}, map[string]monitor.NodeHealth{"bad-node": {}})
	ml := losant.NewMockClient()
	ml.EnsureNodeDeviceFunc = func(_ context.Context, _ losantv1alpha1.LosantSyncSpec, _, _ string) (string, error) {
		return "", errors.New("node api error")
	}
	mg := gea.NewMockClient()
	c := buildClient(ls, credsSecret())
	r := newReconciler(c, ml, mg, hs)

	_, err := reconcileTwice(t, r, reqFor(ls.Name))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var got losantv1alpha1.LosantSync
	if err := c.Get(context.Background(), reqFor(ls.Name).NamespacedName, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != losantv1alpha1.PhaseDegraded {
		t.Errorf("Phase: got %q, want Degraded", got.Status.Phase)
	}
	if len(got.Status.NodeDevices) != 0 {
		t.Errorf("NodeDevices should not be persisted on failure, got %v", got.Status.NodeDevices)
	}
}

// TestGEAReportStateFailure verifies that a GEA reporting error sets
// Phase=Degraded with GEAReachable=False while DevicesProvisioned=True.
func TestGEAReportStateFailure(t *testing.T) {
	ls := baseLS("gea-fail")
	ml := losant.NewMockClient()
	mg := gea.NewMockClient()
	mg.ReportStateFunc = func(_ context.Context, _ gea.StatePayload) error {
		return errors.New("gea down")
	}
	c := buildClient(ls, credsSecret())
	r := newReconciler(c, ml, mg, monitor.NewHealthStore())

	_, err := reconcileTwice(t, r, reqFor(ls.Name))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var got losantv1alpha1.LosantSync
	if err := c.Get(context.Background(), reqFor(ls.Name).NamespacedName, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != losantv1alpha1.PhaseDegraded {
		t.Errorf("Phase: got %q, want Degraded", got.Status.Phase)
	}
	if !conditionFalse(got.Status.Conditions, "GEAReachable") {
		t.Error("expected GEAReachable=False condition")
	}
	if !conditionTrue(got.Status.Conditions, "DevicesProvisioned") {
		t.Error("expected DevicesProvisioned=True (devices were provisioned before GEA failure)")
	}
}

// TestRecovery verifies that a successful sync after a prior failure
// transitions Phase from Degraded back to Active.
func TestRecovery(t *testing.T) {
	ls := baseLS("recovery")
	ml := losant.NewMockClient()
	mg := gea.NewMockClient()
	c := buildClient(ls, credsSecret())
	r := newReconciler(c, ml, mg, monitor.NewHealthStore())
	ctx := context.Background()
	req := reqFor(ls.Name)

	// Drive to Degraded via Ping failure.
	ml.SetError(errors.New("temporary failure"))
	_, _ = r.Reconcile(ctx, req) // first reconcile: Provisioning
	_, _ = r.Reconcile(ctx, req) // second reconcile: Degraded

	// Restore mock to success and reconcile again.
	ml.Reset()
	_, err := r.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("recovery reconcile error: %v", err)
	}

	var got losantv1alpha1.LosantSync
	if err := c.Get(ctx, req.NamespacedName, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != losantv1alpha1.PhaseActive {
		t.Errorf("Phase after recovery: got %q, want Active", got.Status.Phase)
	}
}

// TestSecretNotFound verifies that a missing provisioning secret sets Phase=Degraded
// with a SecretNotFound condition and makes no Losant API calls.
func TestSecretNotFound(t *testing.T) {
	ls := baseLS("no-secret")
	ml := losant.NewMockClient()
	mg := gea.NewMockClient()
	// No secret added to the fake client.
	c := buildClient(ls)
	r := newReconciler(c, ml, mg, monitor.NewHealthStore())

	_, err := reconcileTwice(t, r, reqFor(ls.Name))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var got losantv1alpha1.LosantSync
	if err := c.Get(context.Background(), reqFor(ls.Name).NamespacedName, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != losantv1alpha1.PhaseDegraded {
		t.Errorf("Phase: got %q, want Degraded", got.Status.Phase)
	}
	if !conditionFalse(got.Status.Conditions, "LastSyncSucceeded") {
		t.Error("expected LastSyncSucceeded=False condition")
	}
	if ml.CallCount() != 0 {
		t.Errorf("no Losant client calls expected when secret is missing, got %d", ml.CallCount())
	}
}

// TestSuspend verifies that Spec.Suspend=true immediately sets Phase=Suspended
// and makes no client calls.
func TestSuspend(t *testing.T) {
	ls := baseLS("suspend")
	ls.Spec.Suspend = true
	ml := losant.NewMockClient()
	mg := gea.NewMockClient()
	c := buildClient(ls, credsSecret())
	r := newReconciler(c, ml, mg, monitor.NewHealthStore())

	_, err := r.Reconcile(context.Background(), reqFor(ls.Name))
	if err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	var got losantv1alpha1.LosantSync
	if err := c.Get(context.Background(), reqFor(ls.Name).NamespacedName, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != losantv1alpha1.PhaseSuspended {
		t.Errorf("Phase: got %q, want Suspended", got.Status.Phase)
	}
	if ml.CallCount() != 0 {
		t.Errorf("no client calls expected when suspended, got %d", ml.CallCount())
	}
}

// TestIntervalScheduling verifies that NextScheduledTime advances by the
// configured interval duration after a successful sync.
func TestIntervalScheduling(t *testing.T) {
	ls := baseLS("interval")
	ls.Spec.Interval = "5m"
	c := buildClient(ls, credsSecret())
	r := newReconciler(c, losant.NewMockClient(), gea.NewMockClient(), monitor.NewHealthStore())

	before := time.Now()
	_, err := reconcileTwice(t, r, reqFor(ls.Name))
	if err != nil {
		t.Fatalf("reconcile error: %v", err)
	}
	after := time.Now()

	var got losantv1alpha1.LosantSync
	if err := c.Get(context.Background(), reqFor(ls.Name).NamespacedName, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.NextScheduledTime == nil {
		t.Fatal("NextScheduledTime is nil")
	}
	next := got.Status.NextScheduledTime.Time
	// metav1.Time serialises to RFC3339 (second precision), so subtract 1s on
	// the lower bound to account for truncation.
	lo := before.Add(5*time.Minute - time.Second)
	hi := after.Add(5 * time.Minute)
	if next.Before(lo) || next.After(hi) {
		t.Errorf("NextScheduledTime %v not in expected window [%v, %v]", next, lo, hi)
	}
}

// TestCronScheduling verifies that NextScheduledTime is computed from the
// CronSchedule expression rather than the Interval field.
func TestCronScheduling(t *testing.T) {
	ls := baseLS("cron")
	ls.Spec.CronSchedule = "0 * * * *" // every hour on the hour
	c := buildClient(ls, credsSecret())
	r := newReconciler(c, losant.NewMockClient(), gea.NewMockClient(), monitor.NewHealthStore())

	_, err := reconcileTwice(t, r, reqFor(ls.Name))
	if err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	var got losantv1alpha1.LosantSync
	if err := c.Get(context.Background(), reqFor(ls.Name).NamespacedName, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.NextScheduledTime == nil {
		t.Fatal("NextScheduledTime is nil")
	}
	// The next-on-the-hour should be within 60 minutes from now.
	next := got.Status.NextScheduledTime.Time
	if d := time.Until(next); d <= 0 || d > time.Hour {
		t.Errorf("next cron time %v is not within the next hour (d=%v)", next, d)
	}
	// And the minute should be 0.
	if next.Minute() != 0 {
		t.Errorf("expected next cron time at minute 0, got %d", next.Minute())
	}
}

// TestInvalidCronExpression verifies that a malformed CronSchedule expression causes
// the reconciler to set Phase=Degraded with reason ScheduleError.
func TestInvalidCronExpression(t *testing.T) {
	ls := baseLS("bad-cron")
	ls.Spec.CronSchedule = "not-a-cron"
	ls.Spec.Interval = "" // clear interval so cron path is used
	c := buildClient(ls, credsSecret())
	r := newReconciler(c, losant.NewMockClient(), gea.NewMockClient(), monitor.NewHealthStore())

	_, err := reconcileTwice(t, r, reqFor(ls.Name))
	if err != nil {
		t.Fatalf("reconcile returned unexpected error: %v", err)
	}

	var got losantv1alpha1.LosantSync
	if err := c.Get(context.Background(), reqFor(ls.Name).NamespacedName, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != losantv1alpha1.PhaseDegraded {
		t.Errorf("Phase: got %v, want PhaseDegraded", got.Status.Phase)
	}
	if !conditionFalse(got.Status.Conditions, "LastSyncSucceeded") {
		t.Error("expected LastSyncSucceeded=False on schedule error")
	}
}

// TestInvalidInterval verifies that a malformed Interval string causes the
// reconciler to set Phase=Degraded with reason ScheduleError.
func TestInvalidInterval(t *testing.T) {
	ls := baseLS("bad-interval")
	ls.Spec.Interval = "notaduration"
	c := buildClient(ls, credsSecret())
	r := newReconciler(c, losant.NewMockClient(), gea.NewMockClient(), monitor.NewHealthStore())

	_, err := reconcileTwice(t, r, reqFor(ls.Name))
	if err != nil {
		t.Fatalf("reconcile returned unexpected error: %v", err)
	}

	var got losantv1alpha1.LosantSync
	if err := c.Get(context.Background(), reqFor(ls.Name).NamespacedName, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != losantv1alpha1.PhaseDegraded {
		t.Errorf("Phase: got %v, want PhaseDegraded", got.Status.Phase)
	}
	if !conditionFalse(got.Status.Conditions, "LastSyncSucceeded") {
		t.Error("expected LastSyncSucceeded=False on invalid interval")
	}
}

// TestNilHealthStore verifies the reconciler doesn't panic when HealthStore is nil,
// and still completes the happy path using empty health data.
func TestNilHealthStore(t *testing.T) {
	ls := baseLS("nil-store")
	c := buildClient(ls, credsSecret())
	r := newReconciler(c, losant.NewMockClient(), gea.NewMockClient(), nil)

	_, err := reconcileTwice(t, r, reqFor(ls.Name))
	if err != nil {
		t.Fatalf("reconcile returned unexpected error: %v", err)
	}

	var got losantv1alpha1.LosantSync
	if err := c.Get(context.Background(), reqFor(ls.Name).NamespacedName, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != losantv1alpha1.PhaseActive {
		t.Errorf("Phase: got %v, want PhaseActive", got.Status.Phase)
	}
}

// TestScheduleNotYetDue verifies that the reconciler exits early with a
// RequeueAfter when NextScheduledTime is still in the future.
func TestScheduleNotYetDue(t *testing.T) {
	ls := baseLS("not-yet-due")
	ls.Status.Phase = losantv1alpha1.PhaseActive
	ls.Status.NextScheduledTime = &metav1.Time{Time: time.Now().Add(time.Hour)}
	ml := losant.NewMockClient()
	mg := gea.NewMockClient()
	c := buildClient(ls, credsSecret())
	r := newReconciler(c, ml, mg, monitor.NewHealthStore())

	result, err := r.Reconcile(context.Background(), reqFor(ls.Name))
	if err != nil {
		t.Fatalf("reconcile error: %v", err)
	}
	if result.RequeueAfter <= 0 || result.RequeueAfter > time.Hour {
		t.Errorf("RequeueAfter: got %v, want (0, 1h]", result.RequeueAfter)
	}
	if ml.CallCount() != 0 {
		t.Errorf("no Losant calls expected when sync not yet due, got %d", ml.CallCount())
	}
}

// TestClientConstructionInvalidSecret verifies that when no LosantClient is
// injected and the provisioning secret lacks the "api-token" key, the
// reconciler sets Phase=Degraded with reason InvalidSecret.
func TestClientConstructionInvalidSecret(t *testing.T) {
	ls := baseLS("bad-secret-keys")
	ls.Status.Phase = losantv1alpha1.PhaseProvisioning
	ls.Status.NextScheduledTime = &metav1.Time{Time: time.Now().Add(-time.Second)}
	// credsSecret has "accessKey"/"accessSecret" but NewHTTPClient expects "api-token".
	c := buildClient(ls, credsSecret())
	// Pass nil LosantClient so the reconciler constructs one from the secret.
	r := newReconciler(c, nil, gea.NewMockClient(), monitor.NewHealthStore())

	_, err := r.Reconcile(context.Background(), reqFor(ls.Name))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var got losantv1alpha1.LosantSync
	if err := c.Get(context.Background(), reqFor(ls.Name).NamespacedName, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != losantv1alpha1.PhaseDegraded {
		t.Errorf("Phase: got %q, want Degraded", got.Status.Phase)
	}
	if !conditionFalse(got.Status.Conditions, "LastSyncSucceeded") {
		t.Error("expected LastSyncSucceeded=False when client construction fails")
	}
}

// TestFinalizerAddedOnFirstReconcile verifies that a CR created without the
// device-cleanup finalizer gets the finalizer added on the first reconcile.
func TestFinalizerAddedOnFirstReconcile(t *testing.T) {
	ls := baseLS("add-finalizer")
	ls.Finalizers = nil // clear the pre-populated finalizer to exercise the add path
	c := buildClient(ls, credsSecret())
	r := newReconciler(c, losant.NewMockClient(), gea.NewMockClient(), monitor.NewHealthStore())

	if _, err := r.Reconcile(context.Background(), reqFor(ls.Name)); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	var got losantv1alpha1.LosantSync
	if err := c.Get(context.Background(), reqFor(ls.Name).NamespacedName, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	found := false
	for _, f := range got.Finalizers {
		if f == "losant.io/device-cleanup" {
			found = true
		}
	}
	if !found {
		t.Error("expected losant.io/device-cleanup finalizer to be added on first reconcile")
	}
}

// TestFinalizerAddRequeues is a regression test for the stall described in #329:
// after adding the losant.io/device-cleanup finalizer the reconciler must return
// Requeue=true so that the sync cycle proceeds despite GenerationChangedPredicate
// filtering out the metadata-only watch event that the finalizer write produces.
func TestFinalizerAddRequeues(t *testing.T) {
	ls := baseLS("add-finalizer-requeue")
	ls.Finalizers = nil // exercise the finalizer-add branch
	c := buildClient(ls, credsSecret())
	r := newReconciler(c, losant.NewMockClient(), gea.NewMockClient(), monitor.NewHealthStore())

	result, err := r.Reconcile(context.Background(), reqFor(ls.Name))
	if err != nil {
		t.Fatalf("reconcile error: %v", err)
	}
	if !result.Requeue {
		t.Error("expected Requeue=true after finalizer add; without it the reconciler stalls permanently (issue #329)")
	}
}

// TestFinalizerNotDuplicatedIfPresent verifies that when the CR already has the
// device-cleanup finalizer, Reconcile does not call r.Update (no metadata patch).
func TestFinalizerNotDuplicatedIfPresent(t *testing.T) {
	ls := baseLS("no-dup-finalizer") // already has finalizer via baseLS
	var metaUpdateCalls int
	c := buildClientWithInterceptor(interceptor.Funcs{
		Update: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			if _, ok := obj.(*losantv1alpha1.LosantSync); ok {
				metaUpdateCalls++
			}
			return cl.Update(ctx, obj, opts...)
		},
	}, ls, credsSecret())
	r := newReconciler(c, losant.NewMockClient(), gea.NewMockClient(), monitor.NewHealthStore())

	if _, err := r.Reconcile(context.Background(), reqFor(ls.Name)); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}
	if metaUpdateCalls != 0 {
		t.Errorf("r.Update called %d time(s), want 0 (finalizer already present, no metadata patch needed)", metaUpdateCalls)
	}
}

// TestTeardownOnDeletion verifies that when a CR has a DeletionTimestamp set,
// the reconciler calls DeleteDevice for each registered node and cluster device,
// then removes the finalizer.
func TestTeardownOnDeletion(t *testing.T) {
	ls := baseLS("teardown")
	ts := metav1.Now()
	ls.DeletionTimestamp = &ts
	ls.Status.ClusterDeviceID = "cluster-dev-id"
	ls.Status.NodeDevices = map[string]string{
		"node-1": "node-dev-1",
		"node-2": "node-dev-2",
	}
	ml := losant.NewMockClient()
	c := buildClient(ls, credsSecret())
	r := newReconciler(c, ml, gea.NewMockClient(), monitor.NewHealthStore())

	if _, err := r.Reconcile(context.Background(), reqFor(ls.Name)); err != nil {
		t.Fatalf("teardown reconcile: %v", err)
	}
	if len(ml.DeleteDeviceCalls) != 3 {
		t.Errorf("DeleteDevice calls: got %d, want 3 (2 nodes + 1 cluster)", len(ml.DeleteDeviceCalls))
	}
	// After finalizer removal the object may be garbage-collected by the fake client.
	var got losantv1alpha1.LosantSync
	if c.Get(context.Background(), reqFor(ls.Name).NamespacedName, &got) == nil {
		for _, f := range got.Finalizers {
			if f == "losant.io/device-cleanup" {
				t.Error("losant.io/device-cleanup finalizer should have been removed after teardown")
			}
		}
	}
}

// TestTeardownWith404 verifies that when DeleteDevice returns nil (simulating a
// 404-already-gone response), the teardown still completes and the finalizer is removed.
func TestTeardownWith404(t *testing.T) {
	ls := baseLS("teardown-404")
	ts := metav1.Now()
	ls.DeletionTimestamp = &ts
	ls.Status.ClusterDeviceID = "gone-device-id"
	ml := losant.NewMockClient()
	ml.DeleteDeviceFunc = func(_ context.Context, _, _ string) error { return nil }
	c := buildClient(ls, credsSecret())
	r := newReconciler(c, ml, gea.NewMockClient(), monitor.NewHealthStore())

	if _, err := r.Reconcile(context.Background(), reqFor(ls.Name)); err != nil {
		t.Fatalf("teardown reconcile: %v", err)
	}
	var got losantv1alpha1.LosantSync
	if c.Get(context.Background(), reqFor(ls.Name).NamespacedName, &got) == nil {
		for _, f := range got.Finalizers {
			if f == "losant.io/device-cleanup" {
				t.Error("finalizer should be removed even when device returns 404 (already gone)")
			}
		}
	}
}

// TestTeardownRetryOnError verifies that when DeleteDevice returns a network error,
// the finalizer is NOT removed and the reconciler surfaces the error for retry.
func TestTeardownRetryOnError(t *testing.T) {
	ls := baseLS("teardown-err")
	ts := metav1.Now()
	ls.DeletionTimestamp = &ts
	ls.Status.ClusterDeviceID = "cluster-dev-id"
	ml := losant.NewMockClient()
	ml.DeleteDeviceFunc = func(_ context.Context, _, _ string) error {
		return errors.New("connection refused")
	}
	c := buildClient(ls, credsSecret())
	r := newReconciler(c, ml, gea.NewMockClient(), monitor.NewHealthStore())

	_, err := r.Reconcile(context.Background(), reqFor(ls.Name))
	if err == nil {
		t.Fatal("expected error when DeleteDevice fails, got nil")
	}

	var got losantv1alpha1.LosantSync
	if err := c.Get(context.Background(), reqFor(ls.Name).NamespacedName, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	found := false
	for _, f := range got.Finalizers {
		if f == "losant.io/device-cleanup" {
			found = true
		}
	}
	if !found {
		t.Error("losant.io/device-cleanup finalizer must remain after a failed teardown so the reconciler retries")
	}
}

// TestClusterIdentityChange verifies that when EnsureClusterDevice returns a
// different device ID than the one stored in Status, the reconciler updates
// Status.ClusterDeviceID to the new value and still reaches Phase=Active.
func TestClusterIdentityChange(t *testing.T) {
	ls := baseLS("identity-change")
	ls.Status.Phase = losantv1alpha1.PhaseProvisioning
	ls.Status.ClusterDeviceID = "old-cluster-id"
	ls.Status.NextScheduledTime = &metav1.Time{Time: time.Now().Add(-time.Second)}
	ml := losant.NewMockClient()
	ml.EnsureClusterDeviceFunc = func(_ context.Context, _ losantv1alpha1.LosantSyncSpec) (string, error) {
		return "new-cluster-id", nil
	}
	c := buildClient(ls, credsSecret())
	r := newReconciler(c, ml, gea.NewMockClient(), monitor.NewHealthStore())

	if _, err := r.Reconcile(context.Background(), reqFor(ls.Name)); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}
	var got losantv1alpha1.LosantSync
	if err := c.Get(context.Background(), reqFor(ls.Name).NamespacedName, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.ClusterDeviceID != "new-cluster-id" {
		t.Errorf("ClusterDeviceID: got %q, want %q", got.Status.ClusterDeviceID, "new-cluster-id")
	}
	if got.Status.Phase != losantv1alpha1.PhaseActive {
		t.Errorf("Phase: got %q, want Active (reconcile should succeed despite identity change)", got.Status.Phase)
	}
}

// TestNodeAttributesCPUAndMemRequestPct verifies that cpu_request_pct and
// mem_request_pct from NodeHealth are included in the GEA ReportState payload.
func TestNodeAttributesCPUAndMemRequestPct(t *testing.T) {
	ls := baseLS("node-attrs-pct")
	hs := monitor.NewHealthStore()
	hs.Update(monitor.ClusterHealth{}, map[string]monitor.NodeHealth{
		"node-1": {CPURequestPct: 0.75, MemRequestPct: 0.80},
	})
	mg := gea.NewMockClient()
	c := buildClient(ls, credsSecret())
	r := newReconciler(c, losant.NewMockClient(), mg, hs)

	if _, err := reconcileTwice(t, r, reqFor(ls.Name)); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}
	mg.AssertPayloadContains(t, "mock-node-node-1", "cpu_request_pct", 0.75)
	mg.AssertPayloadContains(t, "mock-node-node-1", "mem_request_pct", 0.80)
}

// conditionTrue reports whether a named condition has Status=True.
func conditionTrue(conds []metav1.Condition, condType string) bool {
	for _, c := range conds {
		if c.Type == condType {
			return c.Status == metav1.ConditionTrue
		}
	}
	return false
}

// conditionFalse reports whether a named condition has Status=False.
func conditionFalse(conds []metav1.Condition, condType string) bool {
	for _, c := range conds {
		if c.Type == condType {
			return c.Status == metav1.ConditionFalse
		}
	}
	return false
}

// buildClientWithInterceptor wraps the fake client with an interceptor so
// specific API calls can be made to fail in error-path tests.
func buildClientWithInterceptor(funcs interceptor.Funcs, objs ...client.Object) client.Client {
	return fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(objs...).
		WithStatusSubresource(&losantv1alpha1.LosantSync{}).
		WithInterceptorFuncs(funcs).
		Build()
}

// TestGetError verifies that a non-IsNotFound error from r.Get is propagated
// to the caller rather than swallowed.
func TestGetError(t *testing.T) {
	apiErr := errors.New("api server unavailable")
	c := buildClientWithInterceptor(interceptor.Funcs{
		Get: func(_ context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*losantv1alpha1.LosantSync); ok {
				return apiErr
			}
			return cl.Get(context.Background(), key, obj, opts...)
		},
	}, baseLS("get-err"), credsSecret())
	r := newReconciler(c, losant.NewMockClient(), gea.NewMockClient(), monitor.NewHealthStore())

	_, err := r.Reconcile(context.Background(), reqFor("get-err"))
	if !errors.Is(err, apiErr) {
		t.Errorf("expected api server error, got: %v", err)
	}
}

// TestSetDegraded_StatusUpdateFails verifies that a Status().Update failure
// inside setDegraded is surfaced as a reconcile error rather than silently dropped.
func TestSetDegraded_StatusUpdateFails(t *testing.T) {
	updateErr := errors.New("etcd write failed")

	// Pre-set Phase to Provisioning with an already-elapsed NextScheduledTime so
	// the reconciler skips the first-reconcile preamble and enters the sync phase
	// where Ping fails, triggering setDegraded, whose Status().Update then fails.
	ls := baseLS("degrade-update-fail")
	ls.Status.Phase = losantv1alpha1.PhaseProvisioning
	ls.Status.NextScheduledTime = &metav1.Time{Time: time.Now().Add(-time.Second)}

	ml := losant.NewMockClient()
	ml.PingFunc = func(_ context.Context) error { return errors.New("ping failed") }

	c := buildClientWithInterceptor(interceptor.Funcs{
		SubResourceUpdate: func(_ context.Context, _ client.Client, subResource string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
			if subResource == "status" {
				if _, ok := obj.(*losantv1alpha1.LosantSync); ok {
					return updateErr
				}
			}
			return nil
		},
	}, ls, credsSecret())
	r := newReconciler(c, ml, gea.NewMockClient(), monitor.NewHealthStore())

	_, err := r.Reconcile(context.Background(), reqFor(ls.Name))
	if !errors.Is(err, updateErr) {
		t.Errorf("expected etcd write error from setDegraded, got: %v", err)
	}
}

// TestNodeDeviceIDEmpty verifies that when EnsureNodeDevice returns an empty
// device ID (with nil error), the GEA state report for that node is skipped but
// the sync cycle still completes successfully with Phase=Active.
func TestNodeDeviceIDEmpty(t *testing.T) {
	ls := baseLS("empty-device-id")
	hs := monitor.NewHealthStore()
	hs.Update(monitor.ClusterHealth{}, map[string]monitor.NodeHealth{"node-1": {}})
	ml := losant.NewMockClient()
	ml.EnsureNodeDeviceFunc = func(_ context.Context, _ losantv1alpha1.LosantSyncSpec, _, _ string) (string, error) {
		return "", nil // empty ID, no error
	}
	mg := gea.NewMockClient()
	c := buildClient(ls, credsSecret())
	r := newReconciler(c, ml, mg, hs)

	_, err := reconcileTwice(t, r, reqFor(ls.Name))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var got losantv1alpha1.LosantSync
	if err := c.Get(context.Background(), reqFor(ls.Name).NamespacedName, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != losantv1alpha1.PhaseActive {
		t.Errorf("Phase: got %q, want Active (node GEA skip is non-fatal)", got.Status.Phase)
	}
	// GEA ReportState should be called only once (cluster), not for the node with empty ID.
	if len(mg.Calls) != 1 {
		t.Errorf("ReportState calls: got %d, want 1 (node skipped)", len(mg.Calls))
	}
}

// conditionReason returns the Reason field of the named condition, or "" if absent.
func conditionReason(conds []metav1.Condition, condType string) string {
	for _, c := range conds {
		if c.Type == condType {
			return c.Reason
		}
	}
	return ""
}

// conditionAbsent reports true when no condition with the given type is present.
func conditionAbsent(conds []metav1.Condition, condType string) bool {
	for _, c := range conds {
		if c.Type == condType {
			return false
		}
	}
	return true
}

// --- ensureWorkflowDeployments ---

// TestDeleteRecreate_NewDeviceIDReprovisionsGEA covers the delete+recreate lifecycle regression
// described in issue #374: when a LosantSync CR is deleted and recreated, the cluster gets a new
// Losant device ID.  The losant-gea-credentials Secret from the old CR still holds the stale
// DEVICE_ID.  Bootstrap must detect the mismatch and call CreateDeviceAccessKey with the new ID
// so the GEA pod reconnects to the correct Losant device.
func TestDeleteRecreate_NewDeviceIDReprovisionsGEA(t *testing.T) {
	// Simulate a losant-gea-credentials Secret left behind by the deleted CR.
	staleGEACreds := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "losant-gea-credentials", Namespace: "default"},
		Data: map[string][]byte{
			"DEVICE_ID":     []byte("old-cluster-device-id"),
			"ACCESS_KEY":    []byte("old-key"),
			"ACCESS_SECRET": []byte("old-secret"),
		},
	}

	// Recreated CR has no status conditions (GEABootstrapped is absent → Bootstrap runs).
	ls := baseLS("recreated")
	ml := losant.NewMockClient()
	ml.EnsureClusterDeviceFunc = func(_ context.Context, _ losantv1alpha1.LosantSyncSpec) (string, error) {
		return "new-cluster-device-id", nil
	}

	c := buildClient(ls, credsSecret(), staleGEACreds)
	r := newReconciler(c, ml, gea.NewMockClient(), monitor.NewHealthStore())

	if _, err := reconcileTwice(t, r, reqFor(ls.Name)); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	if len(ml.CreateDeviceAccessKeyCalls) != 1 {
		t.Errorf("expected 1 CreateDeviceAccessKey call (stale DEVICE_ID must be reprovisioned), got %d",
			len(ml.CreateDeviceAccessKeyCalls))
	}
	call := ml.CreateDeviceAccessKeyCalls[0]
	if call.DeviceID != "new-cluster-device-id" {
		t.Errorf("CreateDeviceAccessKey DeviceID: got %q, want %q", call.DeviceID, "new-cluster-device-id")
	}

	var got corev1.Secret
	if err := c.Get(context.Background(),
		types.NamespacedName{Name: "losant-gea-credentials", Namespace: "default"}, &got); err != nil {
		t.Fatalf("losant-gea-credentials not found after reprovision: %v", err)
	}
	if string(got.Data["DEVICE_ID"]) != "new-cluster-device-id" {
		t.Errorf("DEVICE_ID in Secret: got %q, want %q", string(got.Data["DEVICE_ID"]), "new-cluster-device-id")
	}
}

// TestWorkflowDeployments_EmptySpec verifies that when spec.WorkflowDeployments is
// empty the reconciler does not set any WorkflowDeployed condition and still reaches Active.
func TestWorkflowDeployments_EmptySpec(t *testing.T) {
	ls := baseLS("wf-empty-spec")
	// No WorkflowDeployments field — opt-in is off.
	c := buildClient(ls, credsSecret())
	r := newReconciler(c, losant.NewMockClient(), gea.NewMockClient(), monitor.NewHealthStore())

	if _, err := reconcileTwice(t, r, reqFor(ls.Name)); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	var got losantv1alpha1.LosantSync
	if err := c.Get(context.Background(), reqFor(ls.Name).NamespacedName, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != losantv1alpha1.PhaseActive {
		t.Errorf("Phase: got %q, want Active", got.Status.Phase)
	}
	if !conditionAbsent(got.Status.Conditions, losantv1alpha1.ConditionWorkflowDeployed) {
		t.Errorf("expected no WorkflowDeployed condition, but found one: reason=%s",
			conditionReason(got.Status.Conditions, losantv1alpha1.ConditionWorkflowDeployed))
	}
}

// TestWorkflowDeployments_GetEdgeDeploymentsError verifies that a GetEdgeDeployments
// error sets Phase=Degraded with WorkflowDeployed=False/ReleaseFailed.
func TestWorkflowDeployments_GetEdgeDeploymentsError(t *testing.T) {
	ls := baseLS("wf-get-error")
	ls.Spec.WorkflowDeployments = []losantv1alpha1.WorkflowDeployment{
		{FlowID: "flow-1", Version: "v1.0"},
	}
	ml := losant.NewMockClient()
	ml.GetEdgeDeploymentsFunc = func(_ context.Context, _, _ string) ([]losant.EdgeDeploymentStatus, error) {
		return nil, errors.New("losant API unavailable")
	}
	c := buildClient(ls, credsSecret())
	r := newReconciler(c, ml, gea.NewMockClient(), monitor.NewHealthStore())

	if _, err := reconcileTwice(t, r, reqFor(ls.Name)); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	var got losantv1alpha1.LosantSync
	if err := c.Get(context.Background(), reqFor(ls.Name).NamespacedName, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != losantv1alpha1.PhaseDegraded {
		t.Errorf("Phase: got %q, want Degraded", got.Status.Phase)
	}
	if !conditionFalse(got.Status.Conditions, losantv1alpha1.ConditionWorkflowDeployed) {
		t.Error("expected WorkflowDeployed=False")
	}
	if r := conditionReason(got.Status.Conditions, losantv1alpha1.ConditionWorkflowDeployed); r != "ReleaseFailed" {
		t.Errorf("WorkflowDeployed reason: got %q, want ReleaseFailed", r)
	}
}

// TestWorkflowDeployments_AllDeployed verifies that when all workflows report
// currentVersion == desiredVersion == spec.version, the condition is True/Deployed.
func TestWorkflowDeployments_AllDeployed(t *testing.T) {
	ls := baseLS("wf-all-deployed")
	ls.Spec.WorkflowDeployments = []losantv1alpha1.WorkflowDeployment{
		{FlowID: "flow-1", Version: "v1.0"},
		{FlowID: "flow-2", Version: "v2.0"},
	}
	ml := losant.NewMockClient()
	ml.GetEdgeDeploymentsFunc = func(_ context.Context, _, _ string) ([]losant.EdgeDeploymentStatus, error) {
		return []losant.EdgeDeploymentStatus{
			{FlowID: "flow-1", CurrentVersion: "v1.0", DesiredVersion: "v1.0"},
			{FlowID: "flow-2", CurrentVersion: "v2.0", DesiredVersion: "v2.0"},
		}, nil
	}
	c := buildClient(ls, credsSecret())
	r := newReconciler(c, ml, gea.NewMockClient(), monitor.NewHealthStore())

	if _, err := reconcileTwice(t, r, reqFor(ls.Name)); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	var got losantv1alpha1.LosantSync
	if err := c.Get(context.Background(), reqFor(ls.Name).NamespacedName, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != losantv1alpha1.PhaseActive {
		t.Errorf("Phase: got %q, want Active", got.Status.Phase)
	}
	if !conditionTrue(got.Status.Conditions, losantv1alpha1.ConditionWorkflowDeployed) {
		t.Error("expected WorkflowDeployed=True")
	}
	if r := conditionReason(got.Status.Conditions, losantv1alpha1.ConditionWorkflowDeployed); r != "Deployed" {
		t.Errorf("WorkflowDeployed reason: got %q, want Deployed", r)
	}
}

// TestWorkflowDeployments_DesiredMatchesButCurrentDiffers verifies that when
// desiredVersion already matches spec.version but currentVersion doesn't, the
// condition is True/DeploymentPending and no ReleaseWorkflow call is made.
func TestWorkflowDeployments_DesiredMatchesButCurrentDiffers(t *testing.T) {
	ls := baseLS("wf-desired-pending")
	ls.Spec.WorkflowDeployments = []losantv1alpha1.WorkflowDeployment{
		{FlowID: "flow-1", Version: "v1.0"},
	}
	ml := losant.NewMockClient()
	ml.GetEdgeDeploymentsFunc = func(_ context.Context, _, _ string) ([]losant.EdgeDeploymentStatus, error) {
		return []losant.EdgeDeploymentStatus{
			{FlowID: "flow-1", CurrentVersion: "v0.9", DesiredVersion: "v1.0"},
		}, nil
	}
	c := buildClient(ls, credsSecret())
	r := newReconciler(c, ml, gea.NewMockClient(), monitor.NewHealthStore())

	if _, err := reconcileTwice(t, r, reqFor(ls.Name)); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	var got losantv1alpha1.LosantSync
	if err := c.Get(context.Background(), reqFor(ls.Name).NamespacedName, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != losantv1alpha1.PhaseActive {
		t.Errorf("Phase: got %q, want Active", got.Status.Phase)
	}
	if !conditionTrue(got.Status.Conditions, losantv1alpha1.ConditionWorkflowDeployed) {
		t.Error("expected WorkflowDeployed=True")
	}
	if r := conditionReason(got.Status.Conditions, losantv1alpha1.ConditionWorkflowDeployed); r != "DeploymentPending" {
		t.Errorf("WorkflowDeployed reason: got %q, want DeploymentPending", r)
	}
	if len(ml.ReleaseWorkflowCalls) != 0 {
		t.Errorf("ReleaseWorkflow should not be called when desiredVersion already matches; got %d calls", len(ml.ReleaseWorkflowCalls))
	}
}

// TestWorkflowDeployments_WorkflowNotFound verifies that ErrWorkflowNotFound from
// ReleaseWorkflow sets Phase=Degraded with WorkflowDeployed=False/WorkflowNotFound.
func TestWorkflowDeployments_WorkflowNotFound(t *testing.T) {
	ls := baseLS("wf-not-found")
	ls.Spec.WorkflowDeployments = []losantv1alpha1.WorkflowDeployment{
		{FlowID: "flow-missing", Version: "v1.0"},
	}
	ml := losant.NewMockClient()
	ml.GetEdgeDeploymentsFunc = func(_ context.Context, _, _ string) ([]losant.EdgeDeploymentStatus, error) {
		return nil, nil // flow not in Losant yet
	}
	ml.ReleaseWorkflowFunc = func(_ context.Context, _, _, _, _ string) error {
		return losant.ErrWorkflowNotFound
	}
	c := buildClient(ls, credsSecret())
	r := newReconciler(c, ml, gea.NewMockClient(), monitor.NewHealthStore())

	if _, err := reconcileTwice(t, r, reqFor(ls.Name)); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	var got losantv1alpha1.LosantSync
	if err := c.Get(context.Background(), reqFor(ls.Name).NamespacedName, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != losantv1alpha1.PhaseDegraded {
		t.Errorf("Phase: got %q, want Degraded", got.Status.Phase)
	}
	if !conditionFalse(got.Status.Conditions, losantv1alpha1.ConditionWorkflowDeployed) {
		t.Error("expected WorkflowDeployed=False")
	}
	if r := conditionReason(got.Status.Conditions, losantv1alpha1.ConditionWorkflowDeployed); r != "WorkflowNotFound" {
		t.Errorf("WorkflowDeployed reason: got %q, want WorkflowNotFound", r)
	}
}

// TestWorkflowDeployments_ReleaseError verifies that a generic ReleaseWorkflow error
// sets Phase=Degraded with WorkflowDeployed=False/ReleaseFailed.
func TestWorkflowDeployments_ReleaseError(t *testing.T) {
	ls := baseLS("wf-release-error")
	ls.Spec.WorkflowDeployments = []losantv1alpha1.WorkflowDeployment{
		{FlowID: "flow-1", Version: "v1.0"},
	}
	ml := losant.NewMockClient()
	ml.GetEdgeDeploymentsFunc = func(_ context.Context, _, _ string) ([]losant.EdgeDeploymentStatus, error) {
		return nil, nil // no current deployments — triggers release attempt
	}
	ml.ReleaseWorkflowFunc = func(_ context.Context, _, _, _, _ string) error {
		return errors.New("internal server error")
	}
	c := buildClient(ls, credsSecret())
	r := newReconciler(c, ml, gea.NewMockClient(), monitor.NewHealthStore())

	if _, err := reconcileTwice(t, r, reqFor(ls.Name)); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	var got losantv1alpha1.LosantSync
	if err := c.Get(context.Background(), reqFor(ls.Name).NamespacedName, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != losantv1alpha1.PhaseDegraded {
		t.Errorf("Phase: got %q, want Degraded", got.Status.Phase)
	}
	if !conditionFalse(got.Status.Conditions, losantv1alpha1.ConditionWorkflowDeployed) {
		t.Error("expected WorkflowDeployed=False")
	}
	if r := conditionReason(got.Status.Conditions, losantv1alpha1.ConditionWorkflowDeployed); r != "ReleaseFailed" {
		t.Errorf("WorkflowDeployed reason: got %q, want ReleaseFailed", r)
	}
}

// TestWorkflowDeployments_ReleaseSucceedsPending verifies that when ReleaseWorkflow
// succeeds for an out-of-date workflow, the condition is True/DeploymentPending
// (GEA has not confirmed the switch yet).
func TestWorkflowDeployments_ReleaseSucceedsPending(t *testing.T) {
	ls := baseLS("wf-release-pending")
	ls.Spec.WorkflowDeployments = []losantv1alpha1.WorkflowDeployment{
		{FlowID: "flow-1", Version: "v1.0"},
	}
	ml := losant.NewMockClient()
	ml.GetEdgeDeploymentsFunc = func(_ context.Context, _, _ string) ([]losant.EdgeDeploymentStatus, error) {
		// desiredVersion is still old — ReleaseWorkflow will be called.
		return []losant.EdgeDeploymentStatus{
			{FlowID: "flow-1", CurrentVersion: "v0.9", DesiredVersion: "v0.9"},
		}, nil
	}
	// ReleaseWorkflow default returns nil (success).
	c := buildClient(ls, credsSecret())
	r := newReconciler(c, ml, gea.NewMockClient(), monitor.NewHealthStore())

	if _, err := reconcileTwice(t, r, reqFor(ls.Name)); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	var got losantv1alpha1.LosantSync
	if err := c.Get(context.Background(), reqFor(ls.Name).NamespacedName, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != losantv1alpha1.PhaseActive {
		t.Errorf("Phase: got %q, want Active", got.Status.Phase)
	}
	if !conditionTrue(got.Status.Conditions, losantv1alpha1.ConditionWorkflowDeployed) {
		t.Error("expected WorkflowDeployed=True")
	}
	if r := conditionReason(got.Status.Conditions, losantv1alpha1.ConditionWorkflowDeployed); r != "DeploymentPending" {
		t.Errorf("WorkflowDeployed reason: got %q, want DeploymentPending", r)
	}
	if len(ml.ReleaseWorkflowCalls) != 1 {
		t.Errorf("expected 1 ReleaseWorkflow call, got %d", len(ml.ReleaseWorkflowCalls))
	}
}

// TestWorkflowDeployments_WrappedErrWorkflowNotFound verifies that the controller
// uses errors.Is (not ==) when inspecting ErrWorkflowNotFound, so the wrapped form
// returned by the real HTTPClient is handled the same as the sentinel itself.
func TestWorkflowDeployments_WrappedErrWorkflowNotFound(t *testing.T) {
	ls := baseLS("wf-wrapped-not-found")
	ls.Spec.WorkflowDeployments = []losantv1alpha1.WorkflowDeployment{
		{FlowID: "flow-missing", Version: "v1.0"},
	}
	ml := losant.NewMockClient()
	ml.GetEdgeDeploymentsFunc = func(_ context.Context, _, _ string) ([]losant.EdgeDeploymentStatus, error) {
		return nil, nil
	}
	ml.ReleaseWorkflowFunc = func(_ context.Context, _, _, _, _ string) error {
		// Simulate what the real HTTPClient returns: a wrapped sentinel.
		return fmt.Errorf("%w: status 404 — flow not found in Losant", losant.ErrWorkflowNotFound)
	}
	c := buildClient(ls, credsSecret())
	r := newReconciler(c, ml, gea.NewMockClient(), monitor.NewHealthStore())

	if _, err := reconcileTwice(t, r, reqFor(ls.Name)); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	var got losantv1alpha1.LosantSync
	if err := c.Get(context.Background(), reqFor(ls.Name).NamespacedName, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != losantv1alpha1.PhaseDegraded {
		t.Errorf("Phase: got %q, want Degraded", got.Status.Phase)
	}
	if !conditionFalse(got.Status.Conditions, losantv1alpha1.ConditionWorkflowDeployed) {
		t.Error("expected WorkflowDeployed=False")
	}
	if r := conditionReason(got.Status.Conditions, losantv1alpha1.ConditionWorkflowDeployed); r != "WorkflowNotFound" {
		t.Errorf("WorkflowDeployed reason: got %q, want WorkflowNotFound (wrapped error must be detected via errors.Is)", r)
	}
}

// TestWorkflowDeployments_FlowIdAbsentFromLosantList verifies that when a flowId
// declared in spec is not present in the Losant deployment list, ReleaseWorkflow
// is still called. This is a regression guard for the bug in #357 where valid
// flowIds were incorrectly reported as not found.
func TestWorkflowDeployments_FlowIdAbsentFromLosantList(t *testing.T) {
	ls := baseLS("wf-absent-flow")
	ls.Spec.WorkflowDeployments = []losantv1alpha1.WorkflowDeployment{
		{FlowID: "flow-new", Version: "v1.0"},
	}
	ml := losant.NewMockClient()
	// Losant returns deployments for a different flow — spec flow is absent.
	ml.GetEdgeDeploymentsFunc = func(_ context.Context, _, _ string) ([]losant.EdgeDeploymentStatus, error) {
		return []losant.EdgeDeploymentStatus{
			{FlowID: "flow-other", CurrentVersion: "v9.0", DesiredVersion: "v9.0"},
		}, nil
	}
	c := buildClient(ls, credsSecret())
	r := newReconciler(c, ml, gea.NewMockClient(), monitor.NewHealthStore())

	if _, err := reconcileTwice(t, r, reqFor(ls.Name)); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	if len(ml.ReleaseWorkflowCalls) != 1 {
		t.Fatalf("expected 1 ReleaseWorkflow call for absent flowId, got %d", len(ml.ReleaseWorkflowCalls))
	}
	call := ml.ReleaseWorkflowCalls[0]
	if call.FlowID != "flow-new" {
		t.Errorf("ReleaseWorkflow flowID: got %q, want %q", call.FlowID, "flow-new")
	}
	if call.Version != "v1.0" {
		t.Errorf("ReleaseWorkflow version: got %q, want %q", call.Version, "v1.0")
	}
}

// TestWorkflowDeployments_MultipleFlows_OnlyStaleReleased verifies that when
// multiple workflows are declared and some are already at the correct version,
// only the stale ones trigger a ReleaseWorkflow call.
func TestWorkflowDeployments_MultipleFlows_OnlyStaleReleased(t *testing.T) {
	ls := baseLS("wf-multi-partial")
	ls.Spec.WorkflowDeployments = []losantv1alpha1.WorkflowDeployment{
		{FlowID: "flow-current", Version: "v2.0"},
		{FlowID: "flow-stale", Version: "v3.0"},
	}
	ml := losant.NewMockClient()
	ml.GetEdgeDeploymentsFunc = func(_ context.Context, _, _ string) ([]losant.EdgeDeploymentStatus, error) {
		return []losant.EdgeDeploymentStatus{
			{FlowID: "flow-current", CurrentVersion: "v2.0", DesiredVersion: "v2.0"},
			{FlowID: "flow-stale", CurrentVersion: "v1.0", DesiredVersion: "v1.0"},
		}, nil
	}
	c := buildClient(ls, credsSecret())
	r := newReconciler(c, ml, gea.NewMockClient(), monitor.NewHealthStore())

	if _, err := reconcileTwice(t, r, reqFor(ls.Name)); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	if len(ml.ReleaseWorkflowCalls) != 1 {
		t.Fatalf("expected exactly 1 ReleaseWorkflow call, got %d: %v", len(ml.ReleaseWorkflowCalls), ml.ReleaseWorkflowCalls)
	}
	call := ml.ReleaseWorkflowCalls[0]
	if call.FlowID != "flow-stale" {
		t.Errorf("ReleaseWorkflow called for wrong flow: got %q, want flow-stale", call.FlowID)
	}
}
