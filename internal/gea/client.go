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

package gea

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	losantv1alpha1 "github.com/mak3r/losant-device/api/v1alpha1"
)

// GEAClient sends metric state payloads to the in-cluster Losant Gateway Edge Agent.
type GEAClient interface {
	// ReportState delivers a device state payload to the GEA for buffering and MQTT forwarding.
	ReportState(ctx context.Context, payload StatePayload) error
}

// StatePayload carries the metric snapshot for a single Losant device.
type StatePayload struct {
	// DeviceID is the Losant peripheral or edge-compute device receiving this state.
	DeviceID string

	// Attributes maps Losant attribute names to their current values.
	Attributes map[string]interface{}

	// Timestamp is the observation time. If nil, the GEA applies its own clock.
	Timestamp *time.Time
}

// geaStateBody is the JSON wire format expected by the GEA HTTP trigger.
type geaStateBody struct {
	DeviceID string                 `json:"deviceId"`
	Time     *time.Time             `json:"time,omitempty"`
	Data     map[string]interface{} `json:"data"`
}

// HTTPClient implements GEAClient by POSTing to the in-cluster GEA service.
type HTTPClient struct {
	endpoint   string
	httpClient *http.Client
}

// NewHTTPClient constructs a GEA HTTPClient from a GEASpec.
// The endpoint is resolved as http://{spec.ServiceRef}:{spec.Port}/state.
func NewHTTPClient(spec losantv1alpha1.GEASpec) *HTTPClient {
	return &HTTPClient{
		endpoint:   fmt.Sprintf("http://%s:%d/state", spec.ServiceRef, spec.Port),
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// ReportState marshals the payload and POSTs it to the GEA trigger endpoint.
func (c *HTTPClient) ReportState(ctx context.Context, payload StatePayload) error {
	wire := geaStateBody{
		DeviceID: payload.DeviceID,
		Time:     payload.Timestamp,
		Data:     payload.Attributes,
	}

	b, err := json.Marshal(wire)
	if err != nil {
		return fmt.Errorf("marshal state payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("build gea request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("gea post %s: %w", c.endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("gea post %s: status %d: %s", c.endpoint, resp.StatusCode, body)
	}
	return nil
}
