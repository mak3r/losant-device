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

package rancher_test

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/mak3r/losant-device/internal/rancher"
)

// tlsServerAndCA starts a TLS test server and returns its CA certificate in PEM
// format so a rancher.HTTPClient can be constructed to trust it.
func tlsServerAndCA(t *testing.T, handler http.Handler) (*httptest.Server, []byte) {
	t.Helper()
	ts := httptest.NewTLSServer(handler)
	t.Cleanup(ts.Close)

	x509c, err := x509.ParseCertificate(ts.TLS.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatalf("parse test TLS cert: %v", err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: x509c.Raw})
	return ts, caPEM
}

func newSecret(url string, caPEM []byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "rancher-creds", Namespace: "default"},
		Data: map[string][]byte{
			"RANCHER_URL":   []byte(url),
			"RANCHER_TOKEN": []byte("test-token"),
			"RANCHER_CA":    caPEM,
		},
	}
}

// clusterListBody encodes a Rancher cluster list response body.
func clusterListBody(t *testing.T, clusters []map[string]string) []byte {
	t.Helper()
	type resp struct {
		Data []map[string]string `json:"data"`
	}
	b, err := json.Marshal(resp{Data: clusters})
	if err != nil {
		t.Fatalf("marshal cluster list: %v", err)
	}
	return b
}

// --- NewHTTPClient ---

func TestNewHTTPClient_ValidSecret(t *testing.T) {
	ts, caPEM := tlsServerAndCA(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	secret := newSecret(ts.URL, caPEM)
	if _, err := rancher.NewHTTPClient(secret); err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}
}

func TestNewHTTPClient_MissingURL(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: "ns"},
		Data: map[string][]byte{
			"RANCHER_TOKEN": []byte("tok"),
			"RANCHER_CA":    []byte("ignored"),
		},
	}
	_, err := rancher.NewHTTPClient(secret)
	if err == nil {
		t.Fatal("expected error for missing RANCHER_URL")
	}
}

func TestNewHTTPClient_MissingToken(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: "ns"},
		Data: map[string][]byte{
			"RANCHER_URL": []byte("https://rancher.example.com"),
			"RANCHER_CA":  []byte("ignored"),
		},
	}
	_, err := rancher.NewHTTPClient(secret)
	if err == nil {
		t.Fatal("expected error for missing RANCHER_TOKEN")
	}
}

func TestNewHTTPClient_MissingCA(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: "ns"},
		Data: map[string][]byte{
			"RANCHER_URL":   []byte("https://rancher.example.com"),
			"RANCHER_TOKEN": []byte("tok"),
		},
	}
	_, err := rancher.NewHTTPClient(secret)
	if err == nil {
		t.Fatal("expected error for missing RANCHER_CA")
	}
}

func TestNewHTTPClient_InvalidCAHex(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: "ns"},
		Data: map[string][]byte{
			"RANCHER_URL":   []byte("https://rancher.example.com"),
			"RANCHER_TOKEN": []byte("tok"),
			"RANCHER_CA":    []byte("not-valid-pem"),
		},
	}
	_, err := rancher.NewHTTPClient(secret)
	if err == nil {
		t.Fatal("expected error for invalid CA PEM")
	}
}

// --- FindCluster ---

func TestFindCluster_Found(t *testing.T) {
	ts, caPEM := tlsServerAndCA(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(clusterListBody(t, []map[string]string{{"id": "c-abc", "name": "test-cluster"}}))
	}))
	c, _ := rancher.NewHTTPClient(newSecret(ts.URL, caPEM))
	id, found, err := c.FindCluster(context.Background(), "test-cluster")
	if err != nil {
		t.Fatalf("FindCluster: %v", err)
	}
	if !found {
		t.Fatal("expected found=true")
	}
	if id != "c-abc" {
		t.Errorf("id: got %q, want c-abc", id)
	}
}

func TestFindCluster_NotFound(t *testing.T) {
	ts, caPEM := tlsServerAndCA(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(clusterListBody(t, nil))
	}))
	c, _ := rancher.NewHTTPClient(newSecret(ts.URL, caPEM))
	_, found, err := c.FindCluster(context.Background(), "missing")
	if err != nil {
		t.Fatalf("FindCluster: %v", err)
	}
	if found {
		t.Fatal("expected found=false")
	}
}

func TestFindCluster_APIError(t *testing.T) {
	ts, caPEM := tlsServerAndCA(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	c, _ := rancher.NewHTTPClient(newSecret(ts.URL, caPEM))
	_, _, err := c.FindCluster(context.Background(), "any")
	if err == nil {
		t.Fatal("expected error from 500 response")
	}
}

// --- CreateCluster ---

func TestCreateCluster_Success(t *testing.T) {
	ts, caPEM := tlsServerAndCA(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"c-new","name":"test-cluster"}`)
	}))
	c, _ := rancher.NewHTTPClient(newSecret(ts.URL, caPEM))
	id, err := c.CreateCluster(context.Background(), "test-cluster")
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
	if id != "c-new" {
		t.Errorf("id: got %q, want c-new", id)
	}
}

func TestCreateCluster_APIError(t *testing.T) {
	ts, caPEM := tlsServerAndCA(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	c, _ := rancher.NewHTTPClient(newSecret(ts.URL, caPEM))
	_, err := c.CreateCluster(context.Background(), "cluster")
	if err == nil {
		t.Fatal("expected error from 403 response")
	}
}

func TestCreateCluster_EmptyIDInResponse(t *testing.T) {
	ts, caPEM := tlsServerAndCA(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"","name":"test"}`)
	}))
	c, _ := rancher.NewHTTPClient(newSecret(ts.URL, caPEM))
	_, err := c.CreateCluster(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error for empty ID in response")
	}
}

// --- GetRegistrationToken ---

func TestGetRegistrationToken_Success(t *testing.T) {
	ts, caPEM := tlsServerAndCA(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"data":[{"manifestUrl":"https://rancher.example.com/import.yaml"}]}`)
	}))
	c, _ := rancher.NewHTTPClient(newSecret(ts.URL, caPEM))
	url, err := c.GetRegistrationToken(context.Background(), "c-abc")
	if err != nil {
		t.Fatalf("GetRegistrationToken: %v", err)
	}
	if url != "https://rancher.example.com/import.yaml" {
		t.Errorf("url: got %q", url)
	}
}

func TestGetRegistrationToken_NoTokensReturned(t *testing.T) {
	ts, caPEM := tlsServerAndCA(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"data":[]}`)
	}))
	c, _ := rancher.NewHTTPClient(newSecret(ts.URL, caPEM))
	_, err := c.GetRegistrationToken(context.Background(), "c-abc")
	if err == nil {
		t.Fatal("expected error for empty token list")
	}
}

func TestGetRegistrationToken_APIError(t *testing.T) {
	ts, caPEM := tlsServerAndCA(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	c, _ := rancher.NewHTTPClient(newSecret(ts.URL, caPEM))
	_, err := c.GetRegistrationToken(context.Background(), "c-abc")
	if err == nil {
		t.Fatal("expected error from 404 response")
	}
}

// --- GetCluster ---

func TestGetCluster_Success(t *testing.T) {
	ts, caPEM := tlsServerAndCA(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"id":"c-abc","name":"test"}`)
	}))
	c, _ := rancher.NewHTTPClient(newSecret(ts.URL, caPEM))
	if err := c.GetCluster(context.Background(), "c-abc"); err != nil {
		t.Fatalf("GetCluster: %v", err)
	}
}

func TestGetCluster_NotFound(t *testing.T) {
	ts, caPEM := tlsServerAndCA(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	c, _ := rancher.NewHTTPClient(newSecret(ts.URL, caPEM))
	err := c.GetCluster(context.Background(), "c-abc")
	if !errors.Is(err, rancher.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestGetCluster_APIError(t *testing.T) {
	ts, caPEM := tlsServerAndCA(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gateway timeout", http.StatusGatewayTimeout)
	}))
	c, _ := rancher.NewHTTPClient(newSecret(ts.URL, caPEM))
	err := c.GetCluster(context.Background(), "c-abc")
	if err == nil {
		t.Fatal("expected error from 504 response")
	}
	if errors.Is(err, rancher.ErrNotFound) {
		t.Fatal("should not be ErrNotFound for 504")
	}
}

// --- DeleteCluster ---

func TestDeleteCluster_Success(t *testing.T) {
	ts, caPEM := tlsServerAndCA(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	c, _ := rancher.NewHTTPClient(newSecret(ts.URL, caPEM))
	if err := c.DeleteCluster(context.Background(), "c-abc"); err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}
}

func TestDeleteCluster_404TreatedAsSuccess(t *testing.T) {
	ts, caPEM := tlsServerAndCA(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	c, _ := rancher.NewHTTPClient(newSecret(ts.URL, caPEM))
	if err := c.DeleteCluster(context.Background(), "c-abc"); err != nil {
		t.Fatalf("DeleteCluster on 404 should return nil, got %v", err)
	}
}

func TestDeleteCluster_APIError(t *testing.T) {
	ts, caPEM := tlsServerAndCA(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	c, _ := rancher.NewHTTPClient(newSecret(ts.URL, caPEM))
	if err := c.DeleteCluster(context.Background(), "c-abc"); err == nil {
		t.Fatal("expected error from 500 response")
	}
}
