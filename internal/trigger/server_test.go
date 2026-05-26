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

// Tests are in the same package so they can access the unexported handleRancher method.
package trigger

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	losantv1alpha1 "github.com/mak3r/losant-device/api/v1alpha1"
)

var triggerScheme = func() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = losantv1alpha1.AddToScheme(s)
	return s
}()

// testLosantSync is a minimal LosantSync CR whose name is returned by losantSyncName.
func testLosantSync(name string) *losantv1alpha1.LosantSync {
	return &losantv1alpha1.LosantSync{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: losantv1alpha1.LosantSyncSpec{
			ApplicationID: "app-1",
			ClusterName:   "test-cluster",
		},
	}
}

// testRancherSession is a minimal RancherSession to pre-populate the fake client.
func testRancherSession(name string) *losantv1alpha1.RancherSession {
	return &losantv1alpha1.RancherSession{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: losantv1alpha1.RancherSessionSpec{
			TTLSeconds: 3600,
			CredentialsSecretRef: losantv1alpha1.SecretRef{
				Name:      "rancher-creds",
				Namespace: "default",
			},
		},
	}
}

// postRancher constructs a POST /rancher request with the given JSON body.
func postRancher(body string) *http.Request {
	return httptest.NewRequest(http.MethodPost, "/rancher", strings.NewReader(body))
}

// --- Method guard ---

func TestHandleRancher_MethodNotAllowed(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(triggerScheme).Build()
	s := &Server{Client: c, Namespace: "default"}

	req := httptest.NewRequest(http.MethodGet, "/rancher", nil)
	w := httptest.NewRecorder()
	s.handleRancher(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("code: got %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

// --- No LosantSync CR → 500 ---

func TestHandleRancher_NoLosantSync(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(triggerScheme).Build()
	s := &Server{Client: c, Namespace: "default"}

	req := postRancher(`{"action":"connect","ttlSeconds":3600}`)
	w := httptest.NewRecorder()
	s.handleRancher(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("code: got %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

// --- Unknown action → 400 ---

func TestHandleRancher_UnknownAction(t *testing.T) {
	c := fake.NewClientBuilder().
		WithScheme(triggerScheme).
		WithObjects(testLosantSync("ls-1")).
		Build()
	s := &Server{Client: c, Namespace: "default"}

	req := postRancher(`{"action":"restart"}`)
	w := httptest.NewRecorder()
	s.handleRancher(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("code: got %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// --- Invalid JSON body → 400 ---

func TestHandleRancher_InvalidBody(t *testing.T) {
	c := fake.NewClientBuilder().
		WithScheme(triggerScheme).
		WithObjects(testLosantSync("ls-1")).
		Build()
	s := &Server{Client: c, Namespace: "default"}

	req := httptest.NewRequest(http.MethodPost, "/rancher", strings.NewReader("not-json"))
	w := httptest.NewRecorder()
	s.handleRancher(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("code: got %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// --- Connect: success → 202, RancherSession created ---

func TestHandleRancher_ConnectCreates202(t *testing.T) {
	c := fake.NewClientBuilder().
		WithScheme(triggerScheme).
		WithObjects(testLosantSync("ls-1")).
		Build()
	s := &Server{Client: c, Namespace: "default"}

	req := postRancher(`{"action":"connect","ttlSeconds":7200}`)
	w := httptest.NewRecorder()
	s.handleRancher(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("code: got %d, want %d", w.Code, http.StatusAccepted)
	}

	// Verify RancherSession was created in the fake client.
	var rsList losantv1alpha1.RancherSessionList
	if err := c.List(req.Context(), &rsList); err != nil {
		t.Fatalf("list RancherSessions: %v", err)
	}
	if len(rsList.Items) != 1 {
		t.Errorf("RancherSession count: got %d, want 1", len(rsList.Items))
	}
	if rsList.Items[0].Name != "ls-1" {
		t.Errorf("RancherSession name: got %q, want ls-1", rsList.Items[0].Name)
	}
	if rsList.Items[0].Spec.TTLSeconds != 7200 {
		t.Errorf("TTLSeconds: got %d, want 7200", rsList.Items[0].Spec.TTLSeconds)
	}
}

// --- Connect: connect with default TTL when ttlSeconds <= 0 ---

func TestHandleRancher_ConnectDefaultTTL(t *testing.T) {
	c := fake.NewClientBuilder().
		WithScheme(triggerScheme).
		WithObjects(testLosantSync("ls-1")).
		Build()
	s := &Server{Client: c, Namespace: "default"}

	req := postRancher(`{"action":"connect","ttlSeconds":0}`)
	w := httptest.NewRecorder()
	s.handleRancher(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("code: got %d, want %d", w.Code, http.StatusAccepted)
	}

	var rsList losantv1alpha1.RancherSessionList
	_ = c.List(req.Context(), &rsList)
	if len(rsList.Items) == 1 && rsList.Items[0].Spec.TTLSeconds != 3600 {
		t.Errorf("TTLSeconds: got %d, want default 3600", rsList.Items[0].Spec.TTLSeconds)
	}
}

// --- Connect: RancherSession already exists → 409 ---

func TestHandleRancher_ConnectAlreadyExists409(t *testing.T) {
	c := fake.NewClientBuilder().
		WithScheme(triggerScheme).
		WithObjects(testLosantSync("ls-1"), testRancherSession("ls-1")).
		Build()
	s := &Server{Client: c, Namespace: "default"}

	req := postRancher(`{"action":"connect","ttlSeconds":3600}`)
	w := httptest.NewRecorder()
	s.handleRancher(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("code: got %d, want %d", w.Code, http.StatusConflict)
	}
}

// --- Disconnect: success → 202, RancherSession deleted ---

func TestHandleRancher_DisconnectDeletes202(t *testing.T) {
	c := fake.NewClientBuilder().
		WithScheme(triggerScheme).
		WithObjects(testLosantSync("ls-1"), testRancherSession("ls-1")).
		Build()
	s := &Server{Client: c, Namespace: "default"}

	req := postRancher(`{"action":"disconnect"}`)
	w := httptest.NewRecorder()
	s.handleRancher(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("code: got %d, want %d", w.Code, http.StatusAccepted)
	}

	var rsList losantv1alpha1.RancherSessionList
	_ = c.List(req.Context(), &rsList)
	if len(rsList.Items) != 0 {
		t.Errorf("RancherSession count after disconnect: got %d, want 0", len(rsList.Items))
	}
}

// --- Disconnect: RancherSession not found → 404 ---

func TestHandleRancher_DisconnectNotFound404(t *testing.T) {
	c := fake.NewClientBuilder().
		WithScheme(triggerScheme).
		WithObjects(testLosantSync("ls-1")).
		Build()
	s := &Server{Client: c, Namespace: "default"}

	req := postRancher(`{"action":"disconnect"}`)
	w := httptest.NewRecorder()
	s.handleRancher(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("code: got %d, want %d", w.Code, http.StatusNotFound)
	}
}

// testClusterScopedLosantSync returns a LosantSync with no namespace set,
// matching how a cluster-scoped CR is stored in the informer cache.
func testClusterScopedLosantSync(name string) *losantv1alpha1.LosantSync {
	return &losantv1alpha1.LosantSync{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: losantv1alpha1.LosantSyncSpec{
			ApplicationID: "app-1",
			ClusterName:   "test-cluster",
		},
	}
}

// --- losantSyncName unit tests (issue #453) ---

func TestLosantSyncName_HappyPath(t *testing.T) {
	c := fake.NewClientBuilder().
		WithScheme(triggerScheme).
		WithObjects(testClusterScopedLosantSync("my-sync")).
		Build()
	s := &Server{Client: c, Namespace: "default"}

	name, err := s.losantSyncName(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "my-sync" {
		t.Errorf("name: got %q, want %q", name, "my-sync")
	}
}

// TestLosantSyncName_EmptyList is the regression case for issue #450:
// if InNamespace filtering is applied to a cluster-scoped List, the result
// is empty and losantSyncName must return an error.
func TestLosantSyncName_EmptyList(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(triggerScheme).Build()
	s := &Server{Client: c, Namespace: "default"}

	_, err := s.losantSyncName(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no LosantSync CR found") {
		t.Errorf("error %q does not contain expected message", err.Error())
	}
}

func TestLosantSyncName_ListError(t *testing.T) {
	listErr := fmt.Errorf("etcd unavailable")
	inner := fake.NewClientBuilder().WithScheme(triggerScheme).Build()
	c := interceptor.NewClient(inner, interceptor.Funcs{
		List: func(_ context.Context, _ client.WithWatch, _ client.ObjectList, _ ...client.ListOption) error {
			return listErr
		},
	})
	s := &Server{Client: c, Namespace: "default"}

	_, err := s.losantSyncName(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "etcd unavailable") {
		t.Errorf("error %q does not wrap original error", err.Error())
	}
}

// --- Cluster-scoped regression test (issue #451) ---

// TestHandleRancher_ConnectClusterScopedLosantSync verifies that POST /rancher
// returns 202 when LosantSync exists as a cluster-scoped resource (no namespace).
// This test would have failed against the pre-fix code that applied InNamespace
// filtering on a cluster-scoped List.
func TestHandleRancher_ConnectClusterScopedLosantSync(t *testing.T) {
	c := fake.NewClientBuilder().
		WithScheme(triggerScheme).
		WithObjects(testClusterScopedLosantSync("ls-cluster")).
		Build()
	s := &Server{Client: c, Namespace: "default"}

	req := postRancher(`{"action":"connect","ttlSeconds":3600}`)
	w := httptest.NewRecorder()
	s.handleRancher(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("code: got %d, want %d", w.Code, http.StatusAccepted)
	}

	var rsList losantv1alpha1.RancherSessionList
	if err := c.List(req.Context(), &rsList); err != nil {
		t.Fatalf("list RancherSessions: %v", err)
	}
	if len(rsList.Items) != 1 {
		t.Errorf("RancherSession count: got %d, want 1", len(rsList.Items))
	}
	if rsList.Items[0].Name != "ls-cluster" {
		t.Errorf("RancherSession name: got %q, want %q", rsList.Items[0].Name, "ls-cluster")
	}
}
