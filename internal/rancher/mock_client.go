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

package rancher

import (
	"context"
	"sync"
)

// MockClient implements RancherClient for use in unit tests.
// All methods are configurable via Func fields; defaults return success with
// deterministic values. Call counts and arguments are recorded.
//
// MockClient is safe for concurrent use.
type MockClient struct {
	mu sync.Mutex

	FindClusterFunc          func(ctx context.Context, name string) (string, bool, error)
	CreateClusterFunc        func(ctx context.Context, name string) (string, error)
	GetRegistrationTokenFunc func(ctx context.Context, clusterID string) (string, error)
	GetClusterFunc           func(ctx context.Context, clusterID string) error
	DeleteClusterFunc        func(ctx context.Context, clusterID string) error

	FindClusterCalls          []FindClusterCall
	CreateClusterCalls        []CreateClusterCall
	GetRegistrationTokenCalls []GetRegistrationTokenCall
	GetClusterCalls           []GetClusterCall
	DeleteClusterCalls        []DeleteClusterCall
}

// FindClusterCall records one invocation of FindCluster.
type FindClusterCall struct{ Name string }

// CreateClusterCall records one invocation of CreateCluster.
type CreateClusterCall struct{ Name string }

// GetRegistrationTokenCall records one invocation of GetRegistrationToken.
type GetRegistrationTokenCall struct{ ClusterID string }

// GetClusterCall records one invocation of GetCluster.
type GetClusterCall struct{ ClusterID string }

// DeleteClusterCall records one invocation of DeleteCluster.
type DeleteClusterCall struct{ ClusterID string }

// NewMockRancherClient returns a MockClient with defaults:
// FindCluster → ("", false, nil), CreateCluster → ("mock-cluster-id", nil),
// GetRegistrationToken → ("https://rancher.example.com/manifest.yaml", nil),
// GetCluster → nil, DeleteCluster → nil.
func NewMockRancherClient() *MockClient {
	return &MockClient{}
}

// Reset clears all recorded calls and Func fields.
func (m *MockClient) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.FindClusterFunc = nil
	m.CreateClusterFunc = nil
	m.GetRegistrationTokenFunc = nil
	m.GetClusterFunc = nil
	m.DeleteClusterFunc = nil
	m.FindClusterCalls = nil
	m.CreateClusterCalls = nil
	m.GetRegistrationTokenCalls = nil
	m.GetClusterCalls = nil
	m.DeleteClusterCalls = nil
}

// FindCluster implements RancherClient.
func (m *MockClient) FindCluster(ctx context.Context, name string) (string, bool, error) {
	m.mu.Lock()
	m.FindClusterCalls = append(m.FindClusterCalls, FindClusterCall{Name: name})
	fn := m.FindClusterFunc
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, name)
	}
	return "", false, nil
}

// CreateCluster implements RancherClient.
func (m *MockClient) CreateCluster(ctx context.Context, name string) (string, error) {
	m.mu.Lock()
	m.CreateClusterCalls = append(m.CreateClusterCalls, CreateClusterCall{Name: name})
	fn := m.CreateClusterFunc
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, name)
	}
	return "mock-cluster-id", nil
}

// GetRegistrationToken implements RancherClient.
func (m *MockClient) GetRegistrationToken(ctx context.Context, clusterID string) (string, error) {
	m.mu.Lock()
	m.GetRegistrationTokenCalls = append(m.GetRegistrationTokenCalls, GetRegistrationTokenCall{ClusterID: clusterID})
	fn := m.GetRegistrationTokenFunc
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, clusterID)
	}
	return "https://rancher.example.com/manifest.yaml", nil
}

// GetCluster implements RancherClient.
func (m *MockClient) GetCluster(ctx context.Context, clusterID string) error {
	m.mu.Lock()
	m.GetClusterCalls = append(m.GetClusterCalls, GetClusterCall{ClusterID: clusterID})
	fn := m.GetClusterFunc
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, clusterID)
	}
	return nil
}

// DeleteCluster implements RancherClient.
func (m *MockClient) DeleteCluster(ctx context.Context, clusterID string) error {
	m.mu.Lock()
	m.DeleteClusterCalls = append(m.DeleteClusterCalls, DeleteClusterCall{ClusterID: clusterID})
	fn := m.DeleteClusterFunc
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, clusterID)
	}
	return nil
}
