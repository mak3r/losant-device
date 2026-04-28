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
	"sync/atomic"
	"testing"

	corev1 "k8s.io/api/core/v1"

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
		deviceID:     "dev-001",
		accessKey:    "key-abc",
		accessSecret: "secret-xyz",
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

// authOK registers a POST /auth/device handler on mux that returns the given token.
// It increments the returned counter on each call, allowing caching tests to
// verify that the token exchange happens exactly once.
func authOK(mux *http.ServeMux, token string) *int32 {
	var calls int32
	mux.HandleFunc("POST /auth/device", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		writeJSON(w, authResponse{Token: token, ApplicationID: "app-123"})
	})
	return &calls
}

// --- Constructor ---

func TestNewHTTPClient_MissingKey(t *testing.T) {
	cases := []struct {
		name string
		data map[string][]byte
	}{
		{"missing device-id", map[string][]byte{"access-key": []byte("k"), "access-secret": []byte("s")}},
		{"missing access-key", map[string][]byte{"device-id": []byte("d"), "access-secret": []byte("s")}},
		{"missing access-secret", map[string][]byte{"device-id": []byte("d"), "access-key": []byte("k")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			secret := &corev1.Secret{Data: tc.data}
			if _, err := NewHTTPClient(secret); err == nil {
				t.Errorf("NewHTTPClient with %s: expected error, got nil", tc.name)
			}
		})
	}
}

// --- Ping ---

func TestPing_Success(t *testing.T) {
	mux := http.NewServeMux()
	authOK(mux, "tok-1")
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if err := newTestHTTPClient(srv.URL).Ping(context.Background()); err != nil {
		t.Fatalf("Ping: unexpected error: %v", err)
	}
}

func TestPing_AuthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"invalid credentials"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	if err := newTestHTTPClient(srv.URL).Ping(context.Background()); err == nil {
		t.Fatal("Ping: expected error on 401, got nil")
	}
}

// --- Token caching ---

func TestTokenCaching_AuthCalledOnce(t *testing.T) {
	mux := http.NewServeMux()
	authCalls := authOK(mux, "tok-cached")
	// Catch-all for device list calls — both EnsureClusterDevice calls return the same device.
	mux.HandleFunc("/applications/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, struct {
			Items []Device `json:"items"`
		}{Items: []Device{{DeviceID: "existing-dev", Name: "k8s-cluster-test-cluster"}}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestHTTPClient(srv.URL)
	spec := newTestSpec()
	for i := range 2 {
		if _, err := c.EnsureClusterDevice(context.Background(), spec); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if got := atomic.LoadInt32(authCalls); got != 1 {
		t.Errorf("POST /auth/device called %d time(s), want 1 (token must be cached)", got)
	}
}

// --- EnsureClusterDevice ---

func TestEnsureClusterDevice_Found(t *testing.T) {
	mux := http.NewServeMux()
	authOK(mux, "tok")
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
	authOK(mux, "tok")
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
	authOK(mux, "tok")
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
	authOK(mux, "tok")
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
	authOK(mux, "tok")
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
