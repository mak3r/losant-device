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

package losant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	losantv1alpha1 "github.com/mak3r/losant-device/api/v1alpha1"
)

// redirectTransport rewrites the scheme and host of every outbound request to
// point at the given test server URL, allowing httptest servers to intercept
// calls that client.go hardcodes to apiBase.
type redirectTransport struct {
	serverURL string
}

func (rt *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	parsed, err := url.Parse(rt.serverURL)
	if err != nil {
		return nil, err
	}
	clone := req.Clone(req.Context())
	clone.URL.Scheme = parsed.Scheme
	clone.URL.Host = parsed.Host
	clone.Host = parsed.Host
	return http.DefaultTransport.RoundTrip(clone)
}

// newTestHTTPClient constructs an HTTPClient wired to the given httptest server URL.
// It bypasses NewHTTPClient (which requires a corev1.Secret) so tests can focus on
// HTTP behaviour without Kubernetes fixtures.
func newTestHTTPClient(serverURL string) *HTTPClient {
	return &HTTPClient{
		token: "test-api-token",
		httpClient: &http.Client{
			Transport: &redirectTransport{serverURL: serverURL},
		},
	}
}

// newTestSpec returns a minimal LosantSyncSpec for use in tests.
func newTestSpec() losantv1alpha1.LosantSyncSpec {
	return losantv1alpha1.LosantSyncSpec{
		ApplicationID: "app-123",
		ClusterName:   "test-cluster",
		Region:        "us-west-1",
	}
}

// writeJSON encodes v as JSON with an implicit 200 status.
func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// --- Constructor ---

func TestNewHTTPClient_MissingAPIToken(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "losant-creds", Namespace: "default"},
		Data:       map[string][]byte{"wrong-key": []byte("value")},
	}
	if _, err := NewHTTPClient(secret); err == nil {
		t.Error("NewHTTPClient with missing api-token: expected error, got nil")
	}
}

func TestNewHTTPClient_Success(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "losant-creds", Namespace: "default"},
		Data:       map[string][]byte{"api-token": []byte("tok-abc")},
	}
	c, err := NewHTTPClient(secret)
	if err != nil {
		t.Fatalf("NewHTTPClient: unexpected error: %v", err)
	}
	if c.token != "tok-abc" {
		t.Errorf("token: got %q, want %q", c.token, "tok-abc")
	}
}

// --- Ping ---

func TestPing_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /me", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"userId": "u-1"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if err := newTestHTTPClient(srv.URL).Ping(context.Background()); err != nil {
		t.Fatalf("Ping: unexpected error: %v", err)
	}
}

func TestPing_AuthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"invalid token"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	if err := newTestHTTPClient(srv.URL).Ping(context.Background()); err == nil {
		t.Fatal("Ping: expected error on 401, got nil")
	}
}

func TestPing_BearerTokenSent(t *testing.T) {
	var gotAuth string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /me", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		writeJSON(w, map[string]string{"userId": "u-1"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if err := newTestHTTPClient(srv.URL).Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if gotAuth != "Bearer test-api-token" {
		t.Errorf("Authorization header: got %q, want %q", gotAuth, "Bearer test-api-token")
	}
}

// TestPing_ApplicationEndpoint is a TDD spec for issue #106.
// Application API Tokens are rejected by GET /me (403 Forbidden).
// Ping must be fixed to call GET /applications/{applicationId} instead.
//
// This test fails against the current implementation and will pass once #106 is resolved.
func TestPing_ApplicationEndpoint(t *testing.T) {
	mux := http.NewServeMux()
	// Simulate production: Application API Tokens get 403 on GET /me.
	mux.HandleFunc("GET /me", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"type":"Forbidden","message":"Access is forbidden"}`, http.StatusForbidden)
	})
	// Any application-scoped path returns 200 — the expected endpoint after the fix.
	mux.HandleFunc("/applications/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{"applicationId": "app-123"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if err := newTestHTTPClient(srv.URL).Ping(context.Background()); err != nil {
		t.Fatalf("Ping: unexpected error: %v\n\tissue #106: Ping must use GET /applications/{id}, not GET /me", err)
	}
}

// --- EnsureClusterDevice ---

func TestEnsureClusterDevice_Found(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /applications/app-123/devices", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, struct {
			Items []Device `json:"items"`
		}{Items: []Device{{DeviceID: "cluster-dev-id", Name: "k8s-cluster-test-cluster"}}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	id, err := newTestHTTPClient(srv.URL).EnsureClusterDevice(context.Background(), newTestSpec())
	if err != nil {
		t.Fatalf("EnsureClusterDevice: %v", err)
	}
	if id != "cluster-dev-id" {
		t.Errorf("deviceID: got %q, want %q", id, "cluster-dev-id")
	}
}

func TestEnsureClusterDevice_Created(t *testing.T) {
	var posted bool
	mux := http.NewServeMux()
	mux.HandleFunc("GET /applications/app-123/devices", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, struct {
			Items []Device `json:"items"`
		}{})
	})
	mux.HandleFunc("POST /applications/app-123/devices", func(w http.ResponseWriter, r *http.Request) {
		posted = true
		writeJSON(w, Device{DeviceID: "new-cluster-id", Name: "k8s-cluster-test-cluster"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	id, err := newTestHTTPClient(srv.URL).EnsureClusterDevice(context.Background(), newTestSpec())
	if err != nil {
		t.Fatalf("EnsureClusterDevice: %v", err)
	}
	if id != "new-cluster-id" {
		t.Errorf("deviceID: got %q, want %q", id, "new-cluster-id")
	}
	if !posted {
		t.Error("expected POST /applications/app-123/devices to be called")
	}
}

// --- EnsureNodeDevice ---

func TestEnsureNodeDevice_Created_WithNodeNameTag(t *testing.T) {
	var postBody map[string]interface{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /applications/app-123/devices", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, struct {
			Items []Device `json:"items"`
		}{})
	})
	mux.HandleFunc("POST /applications/app-123/devices", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&postBody)
		writeJSON(w, Device{DeviceID: "node-dev-id", Name: "k8s-node-test-cluster-node-1"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	id, err := newTestHTTPClient(srv.URL).EnsureNodeDevice(context.Background(), newTestSpec(), "node-1")
	if err != nil {
		t.Fatalf("EnsureNodeDevice: %v", err)
	}
	if id != "node-dev-id" {
		t.Errorf("deviceID: got %q, want %q", id, "node-dev-id")
	}
	tags, _ := postBody["tags"].([]interface{})
	found := false
	for _, tag := range tags {
		m, _ := tag.(map[string]interface{})
		if m["key"] == "nodeName" && m["value"] == "node-1" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected nodeName=node-1 in POST tags, got: %v", tags)
	}
}

// --- UpdateDeviceTags ---

func TestUpdateDeviceTags_CallsPatch(t *testing.T) {
	var patchBody map[string]interface{}
	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /applications/app-123/devices/dev-456", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&patchBody)
		writeJSON(w, struct{}{})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	err := newTestHTTPClient(srv.URL).UpdateDeviceTags(context.Background(), "app-123", "dev-456", map[string]string{"env": "prod"})
	if err != nil {
		t.Fatalf("UpdateDeviceTags: %v", err)
	}
	if patchBody == nil {
		t.Fatal("PATCH handler was not called")
	}
}

// --- GetDevice ---

func TestGetDevice_ReturnsDevice(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /applications/app-123/devices/dev-789", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, Device{DeviceID: "dev-789", Name: "my-device"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dev, err := newTestHTTPClient(srv.URL).GetDevice(context.Background(), "app-123", "dev-789")
	if err != nil {
		t.Fatalf("GetDevice: %v", err)
	}
	if dev.DeviceID != "dev-789" {
		t.Errorf("DeviceID: got %q, want %q", dev.DeviceID, "dev-789")
	}
	if dev.Name != "my-device" {
		t.Errorf("Name: got %q, want %q", dev.Name, "my-device")
	}
}

// --- Error propagation ---

func TestNon2xxError_Propagated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"internal server error"}`, http.StatusInternalServerError)
	}))
	defer srv.Close()

	if err := newTestHTTPClient(srv.URL).Ping(context.Background()); err == nil {
		t.Fatal("expected error on 500 response, got nil")
	}
}
