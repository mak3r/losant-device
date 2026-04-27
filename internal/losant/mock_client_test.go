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
	"errors"
	"sync"
	"testing"

	losantv1alpha1 "github.com/mak3r/losant-device/api/v1alpha1"
)

var ctx = context.Background()

var baseSpec = losantv1alpha1.LosantSyncSpec{
	ApplicationID: "app-123",
	ClusterName:   "test-cluster",
	Region:        "us-east",
}

func TestMockClient_DefaultResponses(t *testing.T) {
	m := NewMockClient()

	id, err := m.EnsureClusterDevice(ctx, baseSpec)
	if err != nil || id == "" {
		t.Errorf("EnsureClusterDevice: got (%q, %v), want non-empty id and nil err", id, err)
	}

	nodeID, err := m.EnsureNodeDevice(ctx, baseSpec, "node-1")
	if err != nil || nodeID == "" {
		t.Errorf("EnsureNodeDevice: got (%q, %v), want non-empty id and nil err", nodeID, err)
	}
	if nodeID != "mock-node-node-1" {
		t.Errorf("EnsureNodeDevice default id: got %q, want %q", nodeID, "mock-node-node-1")
	}

	if err := m.UpdateDeviceTags(ctx, "app-123", "dev-1", map[string]string{"k": "v"}); err != nil {
		t.Errorf("UpdateDeviceTags: unexpected error: %v", err)
	}

	dev, err := m.GetDevice(ctx, "app-123", "dev-1")
	if err != nil || dev == nil {
		t.Errorf("GetDevice: got (%v, %v), want non-nil device and nil err", dev, err)
	}
}

func TestMockClient_CallsRecorded(t *testing.T) {
	m := NewMockClient()

	m.EnsureClusterDevice(ctx, baseSpec)
	m.EnsureNodeDevice(ctx, baseSpec, "node-a")
	m.EnsureNodeDevice(ctx, baseSpec, "node-b")
	m.UpdateDeviceTags(ctx, "app-123", "dev-1", nil)
	m.GetDevice(ctx, "app-123", "dev-1")

	if len(m.EnsureClusterDeviceCalls) != 1 {
		t.Errorf("EnsureClusterDeviceCalls: got %d, want 1", len(m.EnsureClusterDeviceCalls))
	}
	if len(m.EnsureNodeDeviceCalls) != 2 {
		t.Errorf("EnsureNodeDeviceCalls: got %d, want 2", len(m.EnsureNodeDeviceCalls))
	}
	if m.EnsureNodeDeviceCalls[0].NodeName != "node-a" {
		t.Errorf("first node call: got %q, want %q", m.EnsureNodeDeviceCalls[0].NodeName, "node-a")
	}
	if m.CallCount() != 5 {
		t.Errorf("CallCount: got %d, want 5", m.CallCount())
	}
}

func TestMockClient_SetError(t *testing.T) {
	m := NewMockClient()
	want := errors.New("api down")
	m.SetError(want)

	if _, err := m.EnsureClusterDevice(ctx, baseSpec); !errors.Is(err, want) {
		t.Errorf("EnsureClusterDevice: got %v, want %v", err, want)
	}
	if _, err := m.EnsureNodeDevice(ctx, baseSpec, "n"); !errors.Is(err, want) {
		t.Errorf("EnsureNodeDevice: got %v, want %v", err, want)
	}
	if err := m.UpdateDeviceTags(ctx, "", "", nil); !errors.Is(err, want) {
		t.Errorf("UpdateDeviceTags: got %v, want %v", err, want)
	}
	if _, err := m.GetDevice(ctx, "", ""); !errors.Is(err, want) {
		t.Errorf("GetDevice: got %v, want %v", err, want)
	}
}

func TestMockClient_CustomHandlerFunc(t *testing.T) {
	m := NewMockClient()
	m.EnsureClusterDeviceFunc = func(_ context.Context, spec losantv1alpha1.LosantSyncSpec) (string, error) {
		return "custom-" + spec.ClusterName, nil
	}

	id, err := m.EnsureClusterDevice(ctx, baseSpec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "custom-test-cluster" {
		t.Errorf("got %q, want %q", id, "custom-test-cluster")
	}
}

func TestMockClient_Reset(t *testing.T) {
	m := NewMockClient()
	m.EnsureClusterDevice(ctx, baseSpec)
	m.SetError(errors.New("err"))
	m.Reset()

	if m.CallCount() != 0 {
		t.Errorf("after Reset: CallCount = %d, want 0", m.CallCount())
	}
	// Default handler restored — should not error.
	if _, err := m.EnsureClusterDevice(ctx, baseSpec); err != nil {
		t.Errorf("after Reset: EnsureClusterDevice returned error: %v", err)
	}
}

func TestMockClient_ConcurrentAccess(t *testing.T) {
	m := NewMockClient()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			m.EnsureNodeDevice(ctx, baseSpec, "node")
		}(i)
	}
	wg.Wait()
	if len(m.EnsureNodeDeviceCalls) != 20 {
		t.Errorf("concurrent calls: got %d, want 20", len(m.EnsureNodeDeviceCalls))
	}
}
