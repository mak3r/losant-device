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
	"sync"

	losantv1alpha1 "github.com/mak3r/losant-device/api/v1alpha1"
)

// MockClient implements LosantClient for use in unit tests.
// All methods are configurable: set a HandlerFunc to override the default
// (which returns a deterministic ID and no error). Call counts and arguments
// are recorded and can be inspected after the fact.
//
// MockClient is safe for concurrent use.
type MockClient struct {
	mu sync.Mutex

	PingFunc                  func(ctx context.Context) error
	EnsureClusterDeviceFunc   func(ctx context.Context, spec losantv1alpha1.LosantSyncSpec) (string, error)
	EnsureNodeDeviceFunc      func(ctx context.Context, spec losantv1alpha1.LosantSyncSpec, nodeName, gatewayID string) (string, error)
	UpdateDeviceTagsFunc      func(ctx context.Context, applicationID, deviceID string, tags map[string]string) error
	GetDeviceFunc             func(ctx context.Context, applicationID, deviceID string) (*Device, error)
	CreateDeviceAccessKeyFunc func(ctx context.Context, applicationID, deviceID, name string) (string, string, string, error)

	// Recorded calls — read after test assertions.
	PingCalls                  []struct{}
	EnsureClusterDeviceCalls   []EnsureClusterDeviceCall
	EnsureNodeDeviceCalls      []EnsureNodeDeviceCall
	UpdateDeviceTagsCalls      []UpdateDeviceTagsCall
	GetDeviceCalls             []GetDeviceCall
	CreateDeviceAccessKeyCalls []CreateDeviceAccessKeyCall
}

// EnsureClusterDeviceCall records one invocation of EnsureClusterDevice.
type EnsureClusterDeviceCall struct {
	Spec losantv1alpha1.LosantSyncSpec
}

// EnsureNodeDeviceCall records one invocation of EnsureNodeDevice.
type EnsureNodeDeviceCall struct {
	Spec      losantv1alpha1.LosantSyncSpec
	NodeName  string
	GatewayID string
}

// UpdateDeviceTagsCall records one invocation of UpdateDeviceTags.
type UpdateDeviceTagsCall struct {
	ApplicationID string
	DeviceID      string
	Tags          map[string]string
}

// GetDeviceCall records one invocation of GetDevice.
type GetDeviceCall struct {
	ApplicationID string
	DeviceID      string
}

// CreateDeviceAccessKeyCall records one invocation of CreateDeviceAccessKey.
type CreateDeviceAccessKeyCall struct {
	ApplicationID string
	DeviceID      string
	Name          string
}

// NewMockClient returns a MockClient with sensible defaults:
// EnsureClusterDevice returns "mock-cluster-device-id"
// EnsureNodeDevice returns "mock-node-<nodeName>"
// UpdateDeviceTags and GetDevice return nil error / empty Device.
func NewMockClient() *MockClient {
	return &MockClient{}
}

// SetError configures all methods to return err, regardless of their HandlerFuncs.
// Useful to simulate a completely unavailable Losant API.
func (m *MockClient) SetError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.PingFunc = func(_ context.Context) error {
		return err
	}
	m.EnsureClusterDeviceFunc = func(_ context.Context, _ losantv1alpha1.LosantSyncSpec) (string, error) {
		return "", err
	}
	m.EnsureNodeDeviceFunc = func(_ context.Context, _ losantv1alpha1.LosantSyncSpec, _, _ string) (string, error) {
		return "", err
	}
	m.UpdateDeviceTagsFunc = func(_ context.Context, _, _ string, _ map[string]string) error {
		return err
	}
	m.GetDeviceFunc = func(_ context.Context, _, _ string) (*Device, error) {
		return nil, err
	}
	m.CreateDeviceAccessKeyFunc = func(_ context.Context, _, _, _ string) (string, string, string, error) {
		return "", "", "", err
	}
}

// CallCount returns the total number of calls recorded across all methods.
func (m *MockClient) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.PingCalls) +
		len(m.EnsureClusterDeviceCalls) +
		len(m.EnsureNodeDeviceCalls) +
		len(m.UpdateDeviceTagsCalls) +
		len(m.GetDeviceCalls) +
		len(m.CreateDeviceAccessKeyCalls)
}

// Reset clears all recorded calls and resets all HandlerFuncs to defaults.
func (m *MockClient) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.PingFunc = nil
	m.EnsureClusterDeviceFunc = nil
	m.EnsureNodeDeviceFunc = nil
	m.UpdateDeviceTagsFunc = nil
	m.GetDeviceFunc = nil
	m.CreateDeviceAccessKeyFunc = nil
	m.PingCalls = nil
	m.EnsureClusterDeviceCalls = nil
	m.EnsureNodeDeviceCalls = nil
	m.UpdateDeviceTagsCalls = nil
	m.GetDeviceCalls = nil
	m.CreateDeviceAccessKeyCalls = nil
}

// Ping implements LosantClient.
func (m *MockClient) Ping(ctx context.Context) error {
	m.mu.Lock()
	m.PingCalls = append(m.PingCalls, struct{}{})
	fn := m.PingFunc
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx)
	}
	return nil
}

// EnsureClusterDevice implements LosantClient.
func (m *MockClient) EnsureClusterDevice(ctx context.Context, spec losantv1alpha1.LosantSyncSpec) (string, error) {
	m.mu.Lock()
	m.EnsureClusterDeviceCalls = append(m.EnsureClusterDeviceCalls, EnsureClusterDeviceCall{Spec: spec})
	fn := m.EnsureClusterDeviceFunc
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, spec)
	}
	return "mock-cluster-device-id", nil
}

// EnsureNodeDevice implements LosantClient.
func (m *MockClient) EnsureNodeDevice(ctx context.Context, spec losantv1alpha1.LosantSyncSpec, nodeName, gatewayID string) (string, error) {
	m.mu.Lock()
	m.EnsureNodeDeviceCalls = append(m.EnsureNodeDeviceCalls, EnsureNodeDeviceCall{Spec: spec, NodeName: nodeName, GatewayID: gatewayID})
	fn := m.EnsureNodeDeviceFunc
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, spec, nodeName, gatewayID)
	}
	return "mock-node-" + nodeName, nil
}

// UpdateDeviceTags implements LosantClient.
func (m *MockClient) UpdateDeviceTags(ctx context.Context, applicationID, deviceID string, tags map[string]string) error {
	m.mu.Lock()
	m.UpdateDeviceTagsCalls = append(m.UpdateDeviceTagsCalls, UpdateDeviceTagsCall{
		ApplicationID: applicationID,
		DeviceID:      deviceID,
		Tags:          tags,
	})
	fn := m.UpdateDeviceTagsFunc
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, applicationID, deviceID, tags)
	}
	return nil
}

// GetDevice implements LosantClient.
func (m *MockClient) GetDevice(ctx context.Context, applicationID, deviceID string) (*Device, error) {
	m.mu.Lock()
	m.GetDeviceCalls = append(m.GetDeviceCalls, GetDeviceCall{
		ApplicationID: applicationID,
		DeviceID:      deviceID,
	})
	fn := m.GetDeviceFunc
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, applicationID, deviceID)
	}
	return &Device{DeviceID: deviceID}, nil
}

// CreateDeviceAccessKey implements LosantClient.
func (m *MockClient) CreateDeviceAccessKey(ctx context.Context, applicationID, deviceID, name string) (string, string, string, error) {
	m.mu.Lock()
	m.CreateDeviceAccessKeyCalls = append(m.CreateDeviceAccessKeyCalls, CreateDeviceAccessKeyCall{
		ApplicationID: applicationID,
		DeviceID:      deviceID,
		Name:          name,
	})
	fn := m.CreateDeviceAccessKeyFunc
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, applicationID, deviceID, name)
	}
	return "mock-key-id", "mock-access-key", "mock-access-secret", nil
}
