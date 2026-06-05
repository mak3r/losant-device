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
	"errors"
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
		token:         "test-api-token",
		applicationID: "app-123",
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
	if _, err := NewHTTPClient(secret, "app-456"); err == nil {
		t.Error("NewHTTPClient with missing api-token: expected error, got nil")
	}
}

func TestNewHTTPClient_Success(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "losant-creds", Namespace: "default"},
		Data:       map[string][]byte{"api-token": []byte("tok-abc")},
	}
	c, err := NewHTTPClient(secret, "app-123")
	if err != nil {
		t.Fatalf("NewHTTPClient: unexpected error: %v", err)
	}
	if c.token != "tok-abc" {
		t.Errorf("token: got %q, want %q", c.token, "tok-abc")
	}
	if c.applicationID != "app-123" {
		t.Errorf("applicationID: got %q, want %q", c.applicationID, "app-123")
	}
}

// --- Ping ---

func TestPing_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /applications/app-123", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{"applicationId": "app-123"})
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
	mux.HandleFunc("GET /applications/app-123", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		writeJSON(w, map[string]interface{}{"applicationId": "app-123"})
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

// --- EnsureClusterDevice ---

func TestEnsureClusterDevice_Found(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /applications/app-123/devices", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, struct {
			Items []Device `json:"items"`
		}{Items: []Device{{DeviceID: "cluster-dev-id", Name: "k8s-cluster-test-cluster"}}})
	})
	mux.HandleFunc("PATCH /applications/app-123/devices/cluster-dev-id", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, struct{}{})
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

	id, err := newTestHTTPClient(srv.URL).EnsureNodeDevice(context.Background(), newTestSpec(), "node-1", "gw-123")
	if err != nil {
		t.Fatalf("EnsureNodeDevice: %v", err)
	}
	if id != "node-dev-id" {
		t.Errorf("deviceID: got %q, want %q", id, "node-dev-id")
	}
	tags, _ := postBody["tags"].([]interface{})
	foundNodeName := false
	for _, tag := range tags {
		m, _ := tag.(map[string]interface{})
		if m["key"] == "nodeName" && m["value"] == "node-1" {
			foundNodeName = true
		}
	}
	if !foundNodeName {
		t.Errorf("expected nodeName=node-1 in POST tags, got: %v", tags)
	}
	if postBody["gatewayId"] != "gw-123" {
		t.Errorf("gatewayId: got %v, want %q", postBody["gatewayId"], "gw-123")
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

// --- EnsureNodeDevice: existing device path ---

func TestEnsureNodeDevice_Found(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /applications/app-123/devices", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, struct {
			Items []Device `json:"items"`
		}{Items: []Device{{DeviceID: "existing-node-id", Name: "k8s-node-test-cluster-node-2"}}})
	})
	mux.HandleFunc("PATCH /applications/app-123/devices/existing-node-id", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, struct{}{})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	id, err := newTestHTTPClient(srv.URL).EnsureNodeDevice(context.Background(), newTestSpec(), "node-2", "gw-abc")
	if err != nil {
		t.Fatalf("EnsureNodeDevice: %v", err)
	}
	if id != "existing-node-id" {
		t.Errorf("deviceID: got %q, want %q", id, "existing-node-id")
	}
}

// --- createDevice: recipeID field and empty-deviceId error ---

func TestCreateDevice_WithRecipeID(t *testing.T) {
	var postBody map[string]interface{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /applications/app-123/devices", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, struct {
			Items []Device `json:"items"`
		}{})
	})
	mux.HandleFunc("POST /applications/app-123/devices", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&postBody)
		writeJSON(w, Device{DeviceID: "recipe-dev-id"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	spec := newTestSpec()
	spec.DeviceRecipeID = "recipe-xyz"
	id, err := newTestHTTPClient(srv.URL).EnsureClusterDevice(context.Background(), spec)
	if err != nil {
		t.Fatalf("EnsureClusterDevice: %v", err)
	}
	if id != "recipe-dev-id" {
		t.Errorf("deviceID: got %q, want %q", id, "recipe-dev-id")
	}
	if postBody["deviceRecipeId"] != "recipe-xyz" {
		t.Errorf("deviceRecipeId: got %v, want %q", postBody["deviceRecipeId"], "recipe-xyz")
	}
}

func TestCreateDevice_EmptyDeviceIDError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /applications/app-123/devices", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, struct {
			Items []Device `json:"items"`
		}{})
	})
	mux.HandleFunc("POST /applications/app-123/devices", func(w http.ResponseWriter, r *http.Request) {
		// Return a device with empty deviceId — should trigger an error.
		writeJSON(w, Device{DeviceID: "", Name: "some-name"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := newTestHTTPClient(srv.URL).EnsureClusterDevice(context.Background(), newTestSpec())
	if err == nil {
		t.Fatal("expected error when deviceId is empty, got nil")
	}
}

// --- tagsFromSpec: ClusterTags ---

func TestTagsFromSpec_AllClusterTagsSet(t *testing.T) {
	spec := newTestSpec()
	spec.Tags = losantv1alpha1.ClusterTags{
		Manager: "https://management.example.com",
		UID:     "cluster-uid-abc",
		GPS:     "37.7749,-122.4194",
	}
	tags := tagsFromSpec(spec)

	tagMap := make(map[string]string, len(tags))
	for _, tag := range tags {
		tagMap[tag.Key] = tag.Value
	}

	if tagMap["manager"] != "https://management.example.com" {
		t.Errorf("manager: got %q, want %q", tagMap["manager"], "https://management.example.com")
	}
	if tagMap["uid"] != "cluster-uid-abc" {
		t.Errorf("uid: got %q, want %q", tagMap["uid"], "cluster-uid-abc")
	}
	if tagMap["gps"] != "37.7749,-122.4194" {
		t.Errorf("gps: got %q, want %q", tagMap["gps"], "37.7749,-122.4194")
	}
}

func TestTagsFromSpec_PartialClusterTags_OnlyUID(t *testing.T) {
	spec := newTestSpec()
	spec.Tags = losantv1alpha1.ClusterTags{UID: "uid-xyz"}
	tags := tagsFromSpec(spec)

	tagMap := make(map[string]string, len(tags))
	for _, tag := range tags {
		tagMap[tag.Key] = tag.Value
	}

	if tagMap["uid"] != "uid-xyz" {
		t.Errorf("uid: got %q, want %q", tagMap["uid"], "uid-xyz")
	}
	if _, ok := tagMap["manager"]; ok {
		t.Error("manager tag must not appear when Manager field is empty")
	}
	if _, ok := tagMap["gps"]; ok {
		t.Error("gps tag must not appear when GPS field is empty")
	}
}

func TestTagsFromSpec_ZeroValueClusterTags_BaseTagsPreserved(t *testing.T) {
	spec := newTestSpec()
	tags := tagsFromSpec(spec)

	tagMap := make(map[string]string, len(tags))
	for _, tag := range tags {
		tagMap[tag.Key] = tag.Value
	}

	if tagMap["clusterName"] != "test-cluster" {
		t.Errorf("clusterName: got %q, want %q", tagMap["clusterName"], "test-cluster")
	}
	if tagMap["region"] != "us-west-1" {
		t.Errorf("region: got %q, want %q", tagMap["region"], "us-west-1")
	}
	for _, k := range []string{"manager", "uid", "gps"} {
		if _, ok := tagMap[k]; ok {
			t.Errorf("%s tag must not appear when ClusterTags is zero-value", k)
		}
	}
}

func TestTagsFromSpec_AllFieldsSet_FullTagSet(t *testing.T) {
	spec := newTestSpec()
	spec.RancherURL = "https://rancher.example.com"
	spec.Tags = losantv1alpha1.ClusterTags{
		Manager: "https://mgmt.example.com",
		UID:     "uid-123",
		GPS:     "40.7128,-74.0060",
	}
	tags := tagsFromSpec(spec)

	tagMap := make(map[string]string, len(tags))
	for _, tag := range tags {
		tagMap[tag.Key] = tag.Value
	}

	expected := map[string]string{
		"clusterName": "test-cluster",
		"region":      "us-west-1",
		"rancherURL":  "https://rancher.example.com",
		"manager":     "https://mgmt.example.com",
		"uid":         "uid-123",
		"gps":         "40.7128,-74.0060",
	}
	for k, v := range expected {
		if got := tagMap[k]; got != v {
			t.Errorf("tag %q: got %q, want %q", k, got, v)
		}
	}
	if len(tags) != len(expected) {
		t.Errorf("tag count: got %d, want %d; tags: %v", len(tags), len(expected), tags)
	}
}

// --- tagsFromSpec: RancherURL branch ---

func TestTagsFromSpec_WithRancherURL(t *testing.T) {
	spec := newTestSpec()
	spec.RancherURL = "https://rancher.example.com"
	tags := tagsFromSpec(spec)

	found := false
	for _, tag := range tags {
		if tag.Key == "rancherURL" && tag.Value == "https://rancher.example.com" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected rancherURL tag with value %q, got: %v", spec.RancherURL, tags)
	}
}

// --- findDeviceByName: invalid JSON response ---

func TestFindDeviceByName_InvalidJSON(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /applications/app-123/devices", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("not-json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := newTestHTTPClient(srv.URL).EnsureClusterDevice(context.Background(), newTestSpec())
	if err == nil {
		t.Fatal("expected error on invalid JSON device list, got nil")
	}
}

// --- CreateDeviceAccessKey ---

// TestCreateDeviceAccessKey_VerifiesPostToKeysEndpoint asserts that the corrected
// client sends POST to /applications/{appId}/keys (not the old device-scoped path
// that returned 405) and sends filterType "all" (not "whitelist") in the body.
func TestCreateDeviceAccessKey_VerifiesPostToKeysEndpoint(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]interface{}
	mux := http.NewServeMux()
	mux.HandleFunc("/applications/app-123/keys", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		writeJSON(w, map[string]interface{}{
			"keyId":  "kid-1",
			"key":    "k1",
			"secret": "s1",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if _, _, _, err := newTestHTTPClient(srv.URL).CreateDeviceAccessKey(context.Background(), "app-123", "dev-xyz", "my-key"); err != nil {
		t.Fatalf("CreateDeviceAccessKey: unexpected error: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("HTTP method: got %q, want POST", gotMethod)
	}
	if gotPath != "/applications/app-123/keys" {
		t.Errorf("path: got %q, want /applications/app-123/keys", gotPath)
	}
	if gotBody["filterType"] != "all" {
		t.Errorf("filterType: got %v, want \"all\"", gotBody["filterType"])
	}
}

func TestCreateDeviceAccessKey_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /applications/app-123/keys", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{
			"keyId":  "key-id-42",
			"key":    "access-key-value",
			"secret": "access-secret-value",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	keyID, key, secret, err := newTestHTTPClient(srv.URL).CreateDeviceAccessKey(context.Background(), "app-123", "dev-abc", "controller-key")
	if err != nil {
		t.Fatalf("CreateDeviceAccessKey: unexpected error: %v", err)
	}
	if keyID != "key-id-42" {
		t.Errorf("keyID: got %q, want %q", keyID, "key-id-42")
	}
	if key != "access-key-value" {
		t.Errorf("key: got %q, want %q", key, "access-key-value")
	}
	if secret != "access-secret-value" {
		t.Errorf("secret: got %q, want %q", secret, "access-secret-value")
	}
}

// TestCreateDeviceAccessKey_Non2xxError verifies that a non-2xx response surfaces as an error.
func TestCreateDeviceAccessKey_Non2xxError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"type":"MethodNotAllowed","message":"POST is not allowed"}`, http.StatusMethodNotAllowed)
	}))
	defer srv.Close()

	_, _, _, err := newTestHTTPClient(srv.URL).CreateDeviceAccessKey(context.Background(), "app-123", "dev-xyz", "k")
	if err == nil {
		t.Fatal("expected error on 405 response, got nil")
	}
}

// TestCreateDeviceAccessKey_HTTP400SchemaError verifies that the client propagates a 400
// schema-validation error from the Losant API. This is a regression guard: the original
// implementation sent filterType "whitelist" which the live API rejected with 400.
func TestCreateDeviceAccessKey_HTTP400SchemaError(t *testing.T) {
	var gotBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		http.Error(w, `{"type":"ValidationError","message":"filterType must be all or none","statusCode":400}`, http.StatusBadRequest)
	}))
	defer srv.Close()

	_, _, _, err := newTestHTTPClient(srv.URL).CreateDeviceAccessKey(context.Background(), "app-123", "dev-xyz", "k")
	if err == nil {
		t.Fatal("expected error on 400 response, got nil")
	}

	if gotBody["filterType"] != "all" {
		t.Errorf("filterType sent to server: got %v, want \"all\" — regression: client must not send \"whitelist\"", gotBody["filterType"])
	}
}

func TestCreateDeviceAccessKey_EmptyKeyInResponse(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /applications/app-123/keys", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{"keyId": "kid", "key": "", "secret": ""})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, _, _, err := newTestHTTPClient(srv.URL).CreateDeviceAccessKey(context.Background(), "app-123", "dev-abc", "k")
	if err == nil {
		t.Fatal("expected error when key/secret are empty in response, got nil")
	}
}

// --- GetDevice: invalid JSON response ---

func TestGetDevice_InvalidJSON(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /applications/app-123/devices/dev-bad", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("not-json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := newTestHTTPClient(srv.URL).GetDevice(context.Background(), "app-123", "dev-bad")
	if err == nil {
		t.Fatal("expected error on invalid JSON device response, got nil")
	}
}

// --- DeleteDevice ---

func TestDeleteDevice_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /applications/app-123/devices/dev-to-delete", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, struct{}{})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if err := newTestHTTPClient(srv.URL).DeleteDevice(context.Background(), "app-123", "dev-to-delete"); err != nil {
		t.Fatalf("DeleteDevice on 200: unexpected error: %v", err)
	}
}

func TestDeleteDevice_NotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /applications/app-123/devices/already-gone", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"type":"ResourceNotFound","message":"Device not found"}`, http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if err := newTestHTTPClient(srv.URL).DeleteDevice(context.Background(), "app-123", "already-gone"); err != nil {
		t.Fatalf("DeleteDevice on 404: expected nil (idempotent), got: %v", err)
	}
}

func TestDeleteDevice_ServerError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /applications/app-123/devices/bad-device", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"internal error"}`, http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if err := newTestHTTPClient(srv.URL).DeleteDevice(context.Background(), "app-123", "bad-device"); err == nil {
		t.Fatal("DeleteDevice on 500: expected error, got nil")
	}
}

// --- Attribute population on device creation ---

func TestEnsureClusterDevice_Created_HasAttributes(t *testing.T) {
	var postBody map[string]interface{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /applications/app-123/devices", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, struct {
			Items []Device `json:"items"`
		}{})
	})
	mux.HandleFunc("POST /applications/app-123/devices", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&postBody)
		writeJSON(w, Device{DeviceID: "new-cluster-attr-id"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if _, err := newTestHTTPClient(srv.URL).EnsureClusterDevice(context.Background(), newTestSpec()); err != nil {
		t.Fatalf("EnsureClusterDevice: %v", err)
	}
	attrs, ok := postBody["attributes"].([]interface{})
	if !ok {
		t.Fatalf("POST body missing 'attributes' field; body: %v", postBody)
	}
	if len(attrs) != 16 {
		t.Errorf("cluster attribute count: got %d, want 16", len(attrs))
	}
}

func TestEnsureNodeDevice_Created_HasAttributes(t *testing.T) {
	var postBody map[string]interface{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /applications/app-123/devices", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, struct {
			Items []Device `json:"items"`
		}{})
	})
	mux.HandleFunc("POST /applications/app-123/devices", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&postBody)
		writeJSON(w, Device{DeviceID: "new-node-attr-id"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if _, err := newTestHTTPClient(srv.URL).EnsureNodeDevice(context.Background(), newTestSpec(), "node-1", "gw-123"); err != nil {
		t.Fatalf("EnsureNodeDevice: %v", err)
	}
	attrs, ok := postBody["attributes"].([]interface{})
	if !ok {
		t.Fatalf("POST body missing 'attributes' field; body: %v", postBody)
	}
	if len(attrs) != 11 {
		t.Errorf("node attribute count: got %d, want 11", len(attrs))
	}
}

// --- Attribute patching when device already exists ---

func TestEnsureClusterDevice_Found_PatchHasAttributes(t *testing.T) {
	var patchBody map[string]interface{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /applications/app-123/devices", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, struct {
			Items []Device `json:"items"`
		}{Items: []Device{{DeviceID: "existing-cluster-id", Name: "k8s-cluster-test-cluster"}}})
	})
	mux.HandleFunc("PATCH /applications/app-123/devices/existing-cluster-id", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&patchBody)
		writeJSON(w, struct{}{})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if _, err := newTestHTTPClient(srv.URL).EnsureClusterDevice(context.Background(), newTestSpec()); err != nil {
		t.Fatalf("EnsureClusterDevice: %v", err)
	}
	if patchBody == nil {
		t.Fatal("PATCH handler was not called")
	}
	attrs, ok := patchBody["attributes"].([]interface{})
	if !ok || len(attrs) != 16 {
		t.Errorf("PATCH body: expected 16 cluster attributes, got: %v", patchBody["attributes"])
	}
}

func TestEnsureNodeDevice_Found_PatchHasAttributes(t *testing.T) {
	var patchBody map[string]interface{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /applications/app-123/devices", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, struct {
			Items []Device `json:"items"`
		}{Items: []Device{{DeviceID: "existing-node-attr-id", Name: "k8s-node-test-cluster-node-4"}}})
	})
	mux.HandleFunc("PATCH /applications/app-123/devices/existing-node-attr-id", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&patchBody)
		writeJSON(w, struct{}{})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if _, err := newTestHTTPClient(srv.URL).EnsureNodeDevice(context.Background(), newTestSpec(), "node-4", "gw-xyz"); err != nil {
		t.Fatalf("EnsureNodeDevice: %v", err)
	}
	if patchBody == nil {
		t.Fatal("PATCH handler was not called")
	}
	attrs, ok := patchBody["attributes"].([]interface{})
	if !ok || len(attrs) != 11 {
		t.Errorf("PATCH body: expected 11 node attributes, got: %v", patchBody["attributes"])
	}
}

// --- ReleaseWorkflow ---

func TestReleaseWorkflow_Success(t *testing.T) {
	var called bool
	mux := http.NewServeMux()
	mux.HandleFunc("POST /applications/app-123/edge/deployments/release", func(w http.ResponseWriter, r *http.Request) {
		called = true
		writeJSON(w, struct{}{})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	err := newTestHTTPClient(srv.URL).ReleaseWorkflow(context.Background(), "app-123", "dev-edge-1", "flow-abc", "v1.0.0")
	if err != nil {
		t.Fatalf("ReleaseWorkflow: unexpected error: %v", err)
	}
	if !called {
		t.Error("expected POST /applications/app-123/edge/deployments/release to be called")
	}
}

func TestReleaseWorkflow_WorkflowNotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /applications/app-123/edge/deployments/release", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"type":"ResourceNotFound","message":"flow not found"}`, http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	err := newTestHTTPClient(srv.URL).ReleaseWorkflow(context.Background(), "app-123", "dev-edge-1", "flow-missing", "v1.0.0")
	if !errors.Is(err, ErrWorkflowNotFound) {
		t.Fatalf("ReleaseWorkflow on 404: expected ErrWorkflowNotFound, got: %v", err)
	}
}

func TestReleaseWorkflow_SendsVersionField(t *testing.T) {
	var gotBody map[string]interface{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /applications/app-123/edge/deployments/release", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		writeJSON(w, struct{}{})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	err := newTestHTTPClient(srv.URL).ReleaseWorkflow(context.Background(), "app-123", "dev-edge-1", "flow-abc", "v1.0.0")
	if err != nil {
		t.Fatalf("ReleaseWorkflow: unexpected error: %v", err)
	}
	// Losant release API uses "version", not "flowVersion"
	if gotBody["version"] != "v1.0.0" {
		t.Errorf("version: got %v, want %q", gotBody["version"], "v1.0.0")
	}
	if gotBody["flowVersion"] != nil {
		t.Errorf("flowVersion field must be absent: got %v (wrong field causes 404 from Losant)", gotBody["flowVersion"])
	}
	if gotBody["flowId"] != "flow-abc" {
		t.Errorf("flowId: got %v, want %q", gotBody["flowId"], "flow-abc")
	}
	deviceIds, ok := gotBody["deviceIds"].([]interface{})
	if !ok || len(deviceIds) != 1 || deviceIds[0] != "dev-edge-1" {
		t.Errorf("deviceIds: got %v, want [\"dev-edge-1\"]", gotBody["deviceIds"])
	}
}

// --- GetEdgeDeployments ---

func TestGetEdgeDeployments_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /applications/app-123/edge/deployments", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{
			"items": []map[string]interface{}{
				{"flowId": "flow-1", "currentVersion": "v1.0.0", "desiredVersion": "v1.1.0"},
				{"flowId": "flow-2", "currentVersion": "v2.0.0", "desiredVersion": "v2.0.0"},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	deployments, err := newTestHTTPClient(srv.URL).GetEdgeDeployments(context.Background(), "app-123", "dev-edge-1")
	if err != nil {
		t.Fatalf("GetEdgeDeployments: unexpected error: %v", err)
	}
	if len(deployments) != 2 {
		t.Fatalf("GetEdgeDeployments: got %d items, want 2", len(deployments))
	}
	if deployments[0].FlowID != "flow-1" {
		t.Errorf("deployments[0].FlowID: got %q, want %q", deployments[0].FlowID, "flow-1")
	}
	if deployments[0].CurrentVersion != "v1.0.0" {
		t.Errorf("deployments[0].CurrentVersion: got %q, want %q", deployments[0].CurrentVersion, "v1.0.0")
	}
	if deployments[0].DesiredVersion != "v1.1.0" {
		t.Errorf("deployments[0].DesiredVersion: got %q, want %q", deployments[0].DesiredVersion, "v1.1.0")
	}
	if deployments[1].FlowID != "flow-2" {
		t.Errorf("deployments[1].FlowID: got %q, want %q", deployments[1].FlowID, "flow-2")
	}
}

func TestGetEdgeDeployments_Empty(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /applications/app-123/edge/deployments", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{"items": []interface{}{}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	deployments, err := newTestHTTPClient(srv.URL).GetEdgeDeployments(context.Background(), "app-123", "dev-edge-1")
	if err != nil {
		t.Fatalf("GetEdgeDeployments empty: unexpected error: %v", err)
	}
	if len(deployments) != 0 {
		t.Errorf("GetEdgeDeployments empty: got %d items, want 0", len(deployments))
	}
}
