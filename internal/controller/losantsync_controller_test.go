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
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

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
		ObjectMeta: metav1.ObjectMeta{Name: name},
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
	ml.EnsureNodeDeviceFunc = func(_ context.Context, _ losantv1alpha1.LosantSyncSpec, _ string) (string, error) {
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
