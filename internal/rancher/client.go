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

// Package rancher implements the Rancher v3 REST API client used by RancherSessionReconciler.
//
// Auth model: requests use a shared service account token provisioned with
// minimum-scope permissions (create/get/delete on /v3/clusters only). The token
// is stored in the credentials Secret under the "RANCHER_TOKEN" key and sent
// as a Bearer token on every request. TLS is verified against the PEM-encoded
// CA stored in the optional "RANCHER_CA" key; when absent the system cert pool
// is used (suitable for publicly-trusted Rancher endpoints). "RANCHER_URL" provides
// the base URL.
package rancher

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// ErrNotFound is returned when a Rancher API call receives a 404 response.
var ErrNotFound = fmt.Errorf("rancher resource not found")

// RancherClient manages Rancher downstream cluster lifecycle via the v3 REST API.
type RancherClient interface {
	// FindCluster returns the Rancher cluster ID for a cluster with the given
	// display name, or ("", false, nil) if no such cluster exists.
	FindCluster(ctx context.Context, name string) (clusterID string, found bool, err error)

	// CreateCluster creates a new import-type cluster in Rancher with the given name.
	CreateCluster(ctx context.Context, name string) (clusterID string, err error)

	// GetRegistrationToken returns the manifest URL for the Rancher import manifest
	// of the specified cluster. The manifest URL is valid for TLS-enabled downloads.
	GetRegistrationToken(ctx context.Context, clusterID string) (manifestURL string, err error)

	// GetCluster checks whether a cluster with the given ID exists.
	// Returns ErrNotFound (wrapped) when the cluster does not exist.
	GetCluster(ctx context.Context, clusterID string) error

	// DeleteCluster removes a downstream cluster from Rancher.
	// Returns nil if the cluster does not exist (404 treated as success).
	DeleteCluster(ctx context.Context, clusterID string) error
}

// HTTPClient implements RancherClient using the Rancher v3 REST API.
type HTTPClient struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// NewHTTPClient constructs an HTTPClient from a credentials Secret.
//
// The Secret must contain:
//   - "RANCHER_URL"   — base URL of the Rancher Manager instance (e.g. "https://rancher.example.com")
//   - "RANCHER_TOKEN" — service account Bearer token
//
// The Secret may optionally contain:
//   - "RANCHER_CA"    — PEM-encoded CA certificate for TLS verification; when absent
//     or empty the OS system cert pool is used (suitable for publicly-trusted endpoints)
func NewHTTPClient(secret *corev1.Secret) (*HTTPClient, error) {
	rawURL, ok := secret.Data["RANCHER_URL"]
	if !ok {
		return nil, fmt.Errorf("credentials secret %s/%s missing key \"RANCHER_URL\"",
			secret.Namespace, secret.Name)
	}
	token, ok := secret.Data["RANCHER_TOKEN"]
	if !ok {
		return nil, fmt.Errorf("credentials secret %s/%s missing key \"RANCHER_TOKEN\"",
			secret.Namespace, secret.Name)
	}
	var tlsConfig *tls.Config
	if caData := secret.Data["RANCHER_CA"]; len(caData) > 0 {
		caPool := x509.NewCertPool()
		if !caPool.AppendCertsFromPEM(caData) {
			return nil, fmt.Errorf("credentials secret %s/%s: RANCHER_CA contains no valid PEM certificates",
				secret.Namespace, secret.Name)
		}
		tlsConfig = &tls.Config{RootCAs: caPool}
	} else {
		pool, err := x509.SystemCertPool()
		if err != nil {
			return nil, fmt.Errorf("load system cert pool: %w", err)
		}
		tlsConfig = &tls.Config{RootCAs: pool}
	}

	tr := &http.Transport{
		TLSClientConfig: tlsConfig,
	}
	return &HTTPClient{
		baseURL: strings.TrimRight(string(rawURL), "/"),
		token:   string(token),
		httpClient: &http.Client{
			Timeout:   30 * time.Second,
			Transport: tr,
		},
	}, nil
}

// cluster is a minimal Rancher cluster representation.
type cluster struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// FindCluster returns the Rancher cluster ID for a cluster matching name,
// or ("", false, nil) if not found.
func (c *HTTPClient) FindCluster(ctx context.Context, name string) (string, bool, error) {
	path := fmt.Sprintf("%s/v3/clusters?name=%s", c.baseURL, name)
	body, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return "", false, err
	}

	var result struct {
		Data []cluster `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", false, fmt.Errorf("decode cluster list response: %w", err)
	}
	if len(result.Data) == 0 {
		return "", false, nil
	}
	return result.Data[0].ID, true, nil
}

// CreateCluster POSTs a new import-type cluster to Rancher and returns its ID.
func (c *HTTPClient) CreateCluster(ctx context.Context, name string) (string, error) {
	payload := map[string]interface{}{
		"name":   name,
		"import": true,
	}
	path := fmt.Sprintf("%s/v3/clusters", c.baseURL)
	body, err := c.doRequest(ctx, http.MethodPost, path, payload)
	if err != nil {
		return "", err
	}

	var cl cluster
	if err := json.Unmarshal(body, &cl); err != nil {
		return "", fmt.Errorf("decode create cluster response: %w", err)
	}
	if cl.ID == "" {
		return "", fmt.Errorf("create cluster: empty id in response")
	}
	return cl.ID, nil
}

// GetRegistrationToken fetches the registration token for a cluster and returns
// the manifest URL to download the import YAML.
func (c *HTTPClient) GetRegistrationToken(ctx context.Context, clusterID string) (string, error) {
	path := fmt.Sprintf("%s/v3/clusterregistrationtokens?clusterId=%s", c.baseURL, clusterID)
	body, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return "", err
	}

	var result struct {
		Data []struct {
			ManifestURL string `json:"manifestUrl"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("decode registration token response: %w", err)
	}
	if len(result.Data) == 0 || result.Data[0].ManifestURL == "" {
		return "", fmt.Errorf("no registration token found for cluster %s", clusterID)
	}
	return result.Data[0].ManifestURL, nil
}

// GetCluster verifies a cluster exists in Rancher by ID.
// Returns ErrNotFound (wrapped) on 404.
func (c *HTTPClient) GetCluster(ctx context.Context, clusterID string) error {
	path := fmt.Sprintf("%s/v3/clusters/%s", c.baseURL, clusterID)
	_, err := c.doRequest(ctx, http.MethodGet, path, nil)
	return err
}

// DeleteCluster removes a cluster from Rancher. Returns nil on 404 (already gone).
func (c *HTTPClient) DeleteCluster(ctx context.Context, clusterID string) error {
	path := fmt.Sprintf("%s/v3/clusters/%s", c.baseURL, clusterID)
	_, err := c.doRequest(ctx, http.MethodDelete, path, nil)
	if err != nil && isNotFound(err) {
		return nil
	}
	return err
}

// doRequest executes an authenticated HTTP request and returns the response body.
func (c *HTTPClient) doRequest(ctx context.Context, method, path string, payload interface{}) ([]byte, error) {
	var bodyReader io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("marshal request payload: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rancher api %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%w: %s %s", ErrNotFound, method, path)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("rancher api %s %s: status %d: %s", method, path, resp.StatusCode, respBody)
	}
	return respBody, nil
}

// isNotFound returns true if err wraps ErrNotFound.
func isNotFound(err error) bool {
	return strings.Contains(err.Error(), ErrNotFound.Error())
}
