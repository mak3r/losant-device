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
// Auth model: Losant REST API calls require an Application API Token obtained
// from the Losant dashboard (Application > Security > API Tokens). The token
// is stored in the provisioning Secret under the "api-token" key and is sent
// as a Bearer token on every request. No token exchange is needed.
// Device access keys (MQTT credentials) are not used here.
package losant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"

	losantv1alpha1 "github.com/mak3r/losant-device/api/v1alpha1"
)

const (
	apiBase         = "https://api.losant.com"
	deviceClassEdge = "edgeCompute"
	deviceClassNode = "peripheral"
)

// ErrWorkflowNotFound is returned by ReleaseWorkflow when the flowId does not exist in the application.
var ErrWorkflowNotFound = fmt.Errorf("workflow not found")

// EdgeDeploymentStatus holds the current and desired workflow versions for one Edge Workflow on a device.
type EdgeDeploymentStatus struct {
	FlowID         string
	CurrentVersion string
	DesiredVersion string
}

// LosantClient manages Losant device lifecycle via the REST provisioning API.
type LosantClient interface {
	// Ping verifies that the Application API Token is valid by calling
	// GET /applications/{applicationId}. Returns nil on success.
	Ping(ctx context.Context) error

	// EnsureClusterDevice creates or retrieves the Edge Compute device representing this cluster.
	EnsureClusterDevice(ctx context.Context, spec losantv1alpha1.LosantSyncSpec) (deviceID string, err error)

	// EnsureNodeDevice creates or retrieves the peripheral device for a k8s node.
	// gatewayID must be the Losant device ID of the cluster Edge Compute device;
	// Losant requires peripheral devices to declare their gateway at creation time.
	EnsureNodeDevice(ctx context.Context, spec losantv1alpha1.LosantSyncSpec, nodeName, gatewayID string) (deviceID string, err error)

	// UpdateDeviceTags replaces the tag set on an existing Losant device.
	UpdateDeviceTags(ctx context.Context, applicationID, deviceID string, tags map[string]string) error

	// GetDevice returns the current definition of a Losant device.
	GetDevice(ctx context.Context, applicationID, deviceID string) (*Device, error)

	// CreateDeviceAccessKey creates a new Losant access key for the given device.
	// Returns keyID, key, and secret. The secret is only available in this response.
	CreateDeviceAccessKey(ctx context.Context, applicationID, deviceID, name string) (keyID, key, secret string, err error)

	// PatchDeviceAttributes adds missing attributes to an existing Losant device.
	// Existing attributes are not removed. Idempotent.
	PatchDeviceAttributes(ctx context.Context, applicationID, deviceID string, attrs []DeviceAttribute) error

	// DeleteDevice removes a device from Losant.
	// Returns nil if the device does not exist (404 treated as success).
	DeleteDevice(ctx context.Context, applicationID, deviceID string) error

	// ReleaseWorkflow deploys the given workflow version to a specific Edge Compute device.
	// Safe to call repeatedly; Losant updates desiredVersion idempotently.
	// Returns ErrWorkflowNotFound if the flowId does not exist in the application.
	ReleaseWorkflow(ctx context.Context, applicationID, deviceID, flowID, version string) error

	// GetEdgeDeployments returns current and desired workflow versions for a specific device.
	GetEdgeDeployments(ctx context.Context, applicationID, deviceID string) ([]EdgeDeploymentStatus, error)
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

// DeviceAttribute declares one telemetry attribute on a Losant device.
// DataType must be one of "number", "string", or "boolean".
type DeviceAttribute struct {
	Name     string `json:"name"`
	DataType string `json:"dataType"`
}

// HTTPClient implements LosantClient using the Losant REST API.
type HTTPClient struct {
	token         string
	applicationID string
	httpClient    *http.Client
}

// NewHTTPClient constructs an HTTPClient from a provisioning Secret and the target application ID.
//
// The Secret must contain:
//   - "api-token" — Losant Application API Token (from Application > Security > API Tokens)
func NewHTTPClient(secret *corev1.Secret, applicationID string) (*HTTPClient, error) {
	token, ok := secret.Data["api-token"]
	if !ok {
		return nil, fmt.Errorf("provisioning secret %s/%s missing key \"api-token\"",
			secret.Namespace, secret.Name)
	}
	return &HTTPClient{
		token:         string(token),
		applicationID: applicationID,
		httpClient:    &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// Ping verifies the Application API Token by calling GET /applications/{applicationID}.
// This endpoint is accessible to Application API Tokens (unlike GET /me, which is user-only).
func (c *HTTPClient) Ping(ctx context.Context) error {
	path := fmt.Sprintf("%s/applications/%s", apiBase, c.applicationID)
	_, err := c.doRequest(ctx, http.MethodGet, path, nil)
	return err
}

// EnsureClusterDevice looks up the cluster Edge Compute device by name and creates it if absent.
func (c *HTTPClient) EnsureClusterDevice(ctx context.Context, spec losantv1alpha1.LosantSyncSpec) (string, error) {
	name := clusterDeviceName(spec.ClusterName)
	attrs := clusterDeviceAttributes()
	existing, err := c.findDeviceByName(ctx, spec.ApplicationID, name)
	if err != nil {
		return "", err
	}
	if existing != nil {
		if err := c.PatchDeviceAttributes(ctx, spec.ApplicationID, existing.DeviceID, attrs); err != nil {
			return "", err
		}
		return existing.DeviceID, nil
	}

	tags := tagsFromSpec(spec)
	return c.createDevice(ctx, spec.ApplicationID, name, deviceClassEdge, spec.DeviceRecipeID, "", tags, attrs)
}

// EnsureNodeDevice looks up the peripheral device for a node by name and creates it if absent.
func (c *HTTPClient) EnsureNodeDevice(ctx context.Context, spec losantv1alpha1.LosantSyncSpec, nodeName, gatewayID string) (string, error) {
	name := nodeDeviceName(spec.ClusterName, nodeName)
	attrs := nodeDeviceAttributes()
	existing, err := c.findDeviceByName(ctx, spec.ApplicationID, name)
	if err != nil {
		return "", err
	}
	if existing != nil {
		if err := c.PatchDeviceAttributes(ctx, spec.ApplicationID, existing.DeviceID, attrs); err != nil {
			return "", err
		}
		return existing.DeviceID, nil
	}

	tags := tagsFromSpec(spec)
	tags = append(tags, Tag{Key: "nodeName", Value: nodeName})
	return c.createDevice(ctx, spec.ApplicationID, name, deviceClassNode, spec.DeviceRecipeID, gatewayID, tags, attrs)
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

// CreateDeviceAccessKey creates a Losant access key and returns the keyID, key, and secret.
// The secret is shown only in this response. filterType "all" is used because the live API
// rejects "whitelist" with a schema-mismatch error (400).
func (c *HTTPClient) CreateDeviceAccessKey(ctx context.Context, applicationID, deviceID, name string) (string, string, string, error) {
	payload := map[string]interface{}{
		"description": name,
		"filterType":  "all",
	}
	path := fmt.Sprintf("%s/applications/%s/keys", apiBase, applicationID)
	body, err := c.doRequest(ctx, http.MethodPost, path, payload)
	if err != nil {
		return "", "", "", err
	}

	var resp struct {
		KeyID  string `json:"keyId"`
		Key    string `json:"key"`
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", "", "", fmt.Errorf("decode access key response: %w", err)
	}
	if resp.Key == "" || resp.Secret == "" {
		return "", "", "", fmt.Errorf("create device access key: empty key or secret in response")
	}
	return resp.KeyID, resp.Key, resp.Secret, nil
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
func (c *HTTPClient) createDevice(ctx context.Context, applicationID, name, class, recipeID, gatewayID string, tags []Tag, attrs []DeviceAttribute) (string, error) {
	payload := map[string]interface{}{
		"name":        name,
		"deviceClass": class,
		"tags":        tags,
	}
	if recipeID != "" {
		payload["deviceRecipeId"] = recipeID
	}
	if gatewayID != "" {
		payload["gatewayId"] = gatewayID
	}
	if len(attrs) > 0 {
		payload["attributes"] = attrs
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
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("losant api %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("losant api %s %s: status %d: %s", method, path, resp.StatusCode, respBody)
	}
	return respBody, nil
}

// PatchDeviceAttributes adds missing attributes to an existing Losant device.
func (c *HTTPClient) PatchDeviceAttributes(ctx context.Context, applicationID, deviceID string, attrs []DeviceAttribute) error {
	payload := map[string]interface{}{"attributes": attrs}
	path := fmt.Sprintf("%s/applications/%s/devices/%s", apiBase, applicationID, deviceID)
	_, err := c.doRequest(ctx, http.MethodPatch, path, payload)
	return err
}

// DeleteDevice removes a device from Losant. Returns nil on 404 (already gone).
func (c *HTTPClient) DeleteDevice(ctx context.Context, applicationID, deviceID string) error {
	path := fmt.Sprintf("%s/applications/%s/devices/%s", apiBase, applicationID, deviceID)
	_, err := c.doRequest(ctx, http.MethodDelete, path, nil)
	if err != nil && strings.Contains(err.Error(), "status 404") {
		return nil
	}
	return err
}

// ReleaseWorkflow deploys a workflow version to an Edge Compute device via the Losant release API.
// Returns ErrWorkflowNotFound (wrapped with the Losant error body) when the API responds with 404.
// Note: Losant may return 404 when the version does not exist for the given flow, not only when
// the flow itself is absent.
func (c *HTTPClient) ReleaseWorkflow(ctx context.Context, applicationID, deviceID, flowID, version string) error {
	payload := map[string]interface{}{
		"flowId":    flowID,
		"version":   version,
		"deviceIds": []string{deviceID},
	}
	path := fmt.Sprintf("%s/applications/%s/edge/deployments/release", apiBase, applicationID)
	_, err := c.doRequest(ctx, http.MethodPost, path, payload)
	if err != nil && strings.Contains(err.Error(), "status 404") {
		return fmt.Errorf("%w: %s", ErrWorkflowNotFound, err.Error())
	}
	return err
}

// GetEdgeDeployments fetches current and desired workflow versions for a device.
func (c *HTTPClient) GetEdgeDeployments(ctx context.Context, applicationID, deviceID string) ([]EdgeDeploymentStatus, error) {
	path := fmt.Sprintf("%s/applications/%s/edge/deployments?deviceId=%s",
		apiBase, applicationID, url.QueryEscape(deviceID))
	body, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		Items []struct {
			FlowID         string `json:"flowId"`
			CurrentVersion string `json:"currentVersion"`
			DesiredVersion string `json:"desiredVersion"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode edge deployments response: %w", err)
	}

	out := make([]EdgeDeploymentStatus, len(result.Items))
	for i, item := range result.Items {
		out[i] = EdgeDeploymentStatus{
			FlowID:         item.FlowID,
			CurrentVersion: item.CurrentVersion,
			DesiredVersion: item.DesiredVersion,
		}
	}
	return out, nil
}

// clusterDeviceAttributes returns the standard attribute definitions for a cluster Edge Compute device.
func clusterDeviceAttributes() []DeviceAttribute {
	return []DeviceAttribute{
		{Name: "health_score", DataType: "number"},
		{Name: "health_status", DataType: "string"},
		{Name: "total_nodes", DataType: "number"},
		{Name: "ready_nodes", DataType: "number"},
		{Name: "unhealthy_nodes", DataType: "number"},
		{Name: "total_pods", DataType: "number"},
		{Name: "running_pods", DataType: "number"},
		{Name: "failed_pods", DataType: "number"},
		{Name: "pending_pods", DataType: "number"},
		{Name: "crashloop_pods", DataType: "number"},
		{Name: "degraded_pvcs", DataType: "number"},
		{Name: "coredns_healthy", DataType: "boolean"},
		{Name: "event_warnings", DataType: "number"},
	}
}

// nodeDeviceAttributes returns the standard attribute definitions for a node peripheral device.
func nodeDeviceAttributes() []DeviceAttribute {
	return []DeviceAttribute{
		{Name: "health_score", DataType: "number"},
		{Name: "health_status", DataType: "string"},
		{Name: "ready", DataType: "boolean"},
		{Name: "memory_pressure", DataType: "boolean"},
		{Name: "disk_pressure", DataType: "boolean"},
		{Name: "pid_pressure", DataType: "boolean"},
		{Name: "pod_count", DataType: "number"},
		{Name: "not_ready_pods", DataType: "number"},
		{Name: "crashloop_pods", DataType: "number"},
		{Name: "cpu_request_pct", DataType: "number"},
		{Name: "mem_request_pct", DataType: "number"},
	}
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
