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

// Package losant implements the Losant REST provisioning client.
//
// Auth model: Losant requires a device-scoped bearer token obtained by
// POSTing {"deviceId","key","secret"} to POST /auth/device. The cluster
// Edge Compute device must be pre-provisioned manually (see docs/losant-setup.md)
// before the operator starts; its Losant device ID is read from the provisioning
// Secret under the "device-id" key. Tokens are cached and refreshed automatically
// 1 hour before their 24-hour expiry window.
package losant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"

	losantv1alpha1 "github.com/mak3r/losant-device/api/v1alpha1"
)

const (
	apiBase         = "https://api.losant.com"
	deviceClassEdge = "edgeCompute"
	deviceClassNode = "peripheral"
	tokenRefreshTTL = 23 * time.Hour // refresh well before Losant's 24-hour token expiry
)

// LosantClient manages Losant device lifecycle via the REST provisioning API.
//
// All methods obtain a short-lived bearer token via POST /auth/device before
// making any API call. The token is cached and refreshed transparently.
type LosantClient interface {
	// Ping verifies that the provisioning credentials are valid by performing
	// the POST /auth/device token exchange. Returns nil on success.
	Ping(ctx context.Context) error

	// EnsureClusterDevice creates or retrieves the Edge Compute device representing this cluster.
	EnsureClusterDevice(ctx context.Context, spec losantv1alpha1.LosantSyncSpec) (deviceID string, err error)

	// EnsureNodeDevice creates or retrieves the peripheral device for a k8s node.
	EnsureNodeDevice(ctx context.Context, spec losantv1alpha1.LosantSyncSpec, nodeName string) (deviceID string, err error)

	// UpdateDeviceTags replaces the tag set on an existing Losant device.
	UpdateDeviceTags(ctx context.Context, applicationID, deviceID string, tags map[string]string) error

	// GetDevice returns the current definition of a Losant device.
	GetDevice(ctx context.Context, applicationID, deviceID string) (*Device, error)
}

// Device is a minimal Losant device representation sufficient for the provisioning workflow.
type Device struct {
	DeviceID string `json:"deviceId"`
	Name     string `json:"name"`
	Tags     []Tag  `json:"tags"`
}

// Tag is a Losant key-value tag attached to a device.
type Tag struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// authResponse is the JSON body returned by POST /auth/device.
type authResponse struct {
	Token         string `json:"token"`
	ApplicationID string `json:"applicationId"`
}

// HTTPClient implements LosantClient using the Losant REST API.
type HTTPClient struct {
	deviceID     string
	accessKey    string
	accessSecret string

	mu          sync.RWMutex
	cachedToken string
	tokenExpiry time.Time

	httpClient *http.Client
}

// NewHTTPClient constructs an HTTPClient from a provisioning Secret.
//
// The Secret must contain three keys:
//   - "device-id"      — Losant device ID of the pre-provisioned cluster Edge Compute device
//   - "access-key"     — Losant application access key
//   - "access-secret"  — Losant application access secret
//
// The cluster device must exist in Losant before the operator starts; see docs/losant-setup.md.
func NewHTTPClient(secret *corev1.Secret) (*HTTPClient, error) {
	deviceID, ok := secret.Data["device-id"]
	if !ok {
		return nil, fmt.Errorf("provisioning secret %s/%s missing key \"device-id\"",
			secret.Namespace, secret.Name)
	}
	key, ok := secret.Data["access-key"]
	if !ok {
		return nil, fmt.Errorf("provisioning secret %s/%s missing key \"access-key\"",
			secret.Namespace, secret.Name)
	}
	sec, ok := secret.Data["access-secret"]
	if !ok {
		return nil, fmt.Errorf("provisioning secret %s/%s missing key \"access-secret\"",
			secret.Namespace, secret.Name)
	}
	return &HTTPClient{
		deviceID:     string(deviceID),
		accessKey:    string(key),
		accessSecret: string(sec),
		httpClient:   &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// Ping verifies credentials by performing the token exchange.
func (c *HTTPClient) Ping(ctx context.Context) error {
	_, err := c.bearerToken(ctx)
	return err
}

// EnsureClusterDevice looks up the cluster Edge Compute device by name and creates it if absent.
func (c *HTTPClient) EnsureClusterDevice(ctx context.Context, spec losantv1alpha1.LosantSyncSpec) (string, error) {
	name := clusterDeviceName(spec.ClusterName)
	existing, err := c.findDeviceByName(ctx, spec.ApplicationID, name)
	if err != nil {
		return "", err
	}
	if existing != nil {
		return existing.DeviceID, nil
	}

	tags := tagsFromSpec(spec)
	return c.createDevice(ctx, spec.ApplicationID, name, deviceClassEdge, spec.DeviceRecipeID, tags)
}

// EnsureNodeDevice looks up the peripheral device for a node by name and creates it if absent.
func (c *HTTPClient) EnsureNodeDevice(ctx context.Context, spec losantv1alpha1.LosantSyncSpec, nodeName string) (string, error) {
	name := nodeDeviceName(spec.ClusterName, nodeName)
	existing, err := c.findDeviceByName(ctx, spec.ApplicationID, name)
	if err != nil {
		return "", err
	}
	if existing != nil {
		return existing.DeviceID, nil
	}

	tags := tagsFromSpec(spec)
	tags = append(tags, Tag{Key: "nodeName", Value: nodeName})
	return c.createDevice(ctx, spec.ApplicationID, name, deviceClassNode, spec.DeviceRecipeID, tags)
}

// UpdateDeviceTags replaces the tags on the given device.
func (c *HTTPClient) UpdateDeviceTags(ctx context.Context, applicationID, deviceID string, tags map[string]string) error {
	tagList := make([]Tag, 0, len(tags))
	for k, v := range tags {
		tagList = append(tagList, Tag{Key: k, Value: v})
	}

	payload := map[string]interface{}{"tags": tagList}
	path := fmt.Sprintf("%s/applications/%s/devices/%s", apiBase, applicationID, deviceID)
	_, err := c.doRequest(ctx, http.MethodPatch, path, payload)
	return err
}

// GetDevice returns the current Losant device definition.
func (c *HTTPClient) GetDevice(ctx context.Context, applicationID, deviceID string) (*Device, error) {
	path := fmt.Sprintf("%s/applications/%s/devices/%s", apiBase, applicationID, deviceID)
	body, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	var dev Device
	if err := json.Unmarshal(body, &dev); err != nil {
		return nil, fmt.Errorf("decode device response: %w", err)
	}
	return &dev, nil
}

// bearerToken returns a valid bearer token, refreshing via POST /auth/device when needed.
func (c *HTTPClient) bearerToken(ctx context.Context) (string, error) {
	// Fast path: cached token still valid.
	c.mu.RLock()
	if c.cachedToken != "" && time.Now().Before(c.tokenExpiry) {
		t := c.cachedToken
		c.mu.RUnlock()
		return t, nil
	}
	c.mu.RUnlock()

	// Slow path: exchange credentials for a fresh token.
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check under write lock in case another goroutine already refreshed.
	if c.cachedToken != "" && time.Now().Before(c.tokenExpiry) {
		return c.cachedToken, nil
	}

	payload := map[string]string{
		"deviceId": c.deviceID,
		"key":      c.accessKey,
		"secret":   c.accessSecret,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal auth payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBase+"/auth/device", bytes.NewReader(b))
	if err != nil {
		return "", fmt.Errorf("build auth request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("losant auth: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read auth response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("losant auth: status %d: %s", resp.StatusCode, respBody)
	}

	var authResp authResponse
	if err := json.Unmarshal(respBody, &authResp); err != nil {
		return "", fmt.Errorf("decode auth response: %w", err)
	}
	if authResp.Token == "" {
		return "", fmt.Errorf("losant auth: empty token in response")
	}

	c.cachedToken = authResp.Token
	c.tokenExpiry = time.Now().Add(tokenRefreshTTL)
	return c.cachedToken, nil
}

// findDeviceByName returns the first Losant device matching name, or nil if not found.
func (c *HTTPClient) findDeviceByName(ctx context.Context, applicationID, name string) (*Device, error) {
	path := fmt.Sprintf("%s/applications/%s/devices?filterField=name&filter=%s",
		apiBase, applicationID, url.QueryEscape(name))
	body, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		Items []Device `json:"items"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode device list response: %w", err)
	}
	if len(result.Items) == 0 {
		return nil, nil
	}
	return &result.Items[0], nil
}

// createDevice POSTs a new device and returns the assigned device ID.
func (c *HTTPClient) createDevice(ctx context.Context, applicationID, name, class, recipeID string, tags []Tag) (string, error) {
	payload := map[string]interface{}{
		"name":        name,
		"deviceClass": class,
		"tags":        tags,
	}
	if recipeID != "" {
		payload["deviceRecipeId"] = recipeID
	}

	path := fmt.Sprintf("%s/applications/%s/devices", apiBase, applicationID)
	body, err := c.doRequest(ctx, http.MethodPost, path, payload)
	if err != nil {
		return "", err
	}

	var dev Device
	if err := json.Unmarshal(body, &dev); err != nil {
		return "", fmt.Errorf("decode create device response: %w", err)
	}
	if dev.DeviceID == "" {
		return "", fmt.Errorf("create device: empty deviceId in response")
	}
	return dev.DeviceID, nil
}

// doRequest executes an authenticated HTTP request and returns the response body.
func (c *HTTPClient) doRequest(ctx context.Context, method, path string, payload interface{}) ([]byte, error) {
	token, err := c.bearerToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}

	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("marshal request payload: %w", err)
		}
		body = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, path, body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("losant api %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("losant api %s %s: status %d: %s", method, path, resp.StatusCode, respBody)
	}
	return respBody, nil
}

// clusterDeviceName returns the canonical Losant device name for a cluster.
func clusterDeviceName(clusterName string) string {
	return fmt.Sprintf("k8s-cluster-%s", clusterName)
}

// nodeDeviceName returns the canonical Losant device name for a node within a cluster.
func nodeDeviceName(clusterName, nodeName string) string {
	return fmt.Sprintf("k8s-node-%s-%s", clusterName, nodeName)
}

// tagsFromSpec builds the base tag list from the LosantSyncSpec fields.
func tagsFromSpec(spec losantv1alpha1.LosantSyncSpec) []Tag {
	tags := []Tag{
		{Key: "clusterName", Value: spec.ClusterName},
		{Key: "region", Value: spec.Region},
	}
	if spec.RancherURL != "" {
		tags = append(tags, Tag{Key: "rancherURL", Value: spec.RancherURL})
	}
	return tags
}
