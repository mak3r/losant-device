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
	"context"
	"fmt"
	"sync"
	"testing"
)

// MockClient implements GEAClient for use in unit tests.
// It captures every ReportState call for later assertion and can be
// configured to return errors to simulate an unreachable GEA.
//
// MockClient is safe for concurrent use.
type MockClient struct {
	mu sync.Mutex

	ReportStateFunc func(ctx context.Context, payload StatePayload) error

	// Calls records every ReportState invocation in order.
	Calls []StatePayload
}

// NewMockClient returns a MockClient that accepts all payloads without error.
func NewMockClient() *MockClient {
	return &MockClient{}
}

// SetError configures ReportState to always return err.
// Pass nil to revert to the default (accept all).
func (m *MockClient) SetError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err == nil {
		m.ReportStateFunc = nil
		return
	}
	m.ReportStateFunc = func(_ context.Context, _ StatePayload) error {
		return err
	}
}

// Reset clears all recorded calls and resets ReportStateFunc to the default.
func (m *MockClient) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ReportStateFunc = nil
	m.Calls = nil
}

// CallCount returns the number of ReportState calls recorded.
func (m *MockClient) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.Calls)
}

// ReportState implements GEAClient.
func (m *MockClient) ReportState(ctx context.Context, payload StatePayload) error {
	m.mu.Lock()
	m.Calls = append(m.Calls, payload)
	fn := m.ReportStateFunc
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, payload)
	}
	return nil
}

// AssertPayloadContains asserts that at least one recorded call targeted deviceID
// and contained an attribute key with the given value.
// Calls t.Errorf (non-fatal) on failure so the test can continue.
func (m *MockClient) AssertPayloadContains(t *testing.T, deviceID, attributeKey string, want interface{}) {
	t.Helper()
	m.mu.Lock()
	calls := make([]StatePayload, len(m.Calls))
	copy(calls, m.Calls)
	m.mu.Unlock()

	for _, c := range calls {
		if c.DeviceID != deviceID {
			continue
		}
		got, ok := c.Attributes[attributeKey]
		if !ok {
			continue
		}
		if fmt.Sprintf("%v", got) == fmt.Sprintf("%v", want) {
			return
		}
	}
	t.Errorf("no ReportState call found for deviceID=%q with attribute %q=%v (recorded %d calls)",
		deviceID, attributeKey, want, len(calls))
}

// AssertCalled asserts that ReportState was called at least once.
func (m *MockClient) AssertCalled(t *testing.T) {
	t.Helper()
	if m.CallCount() == 0 {
		t.Error("expected at least one ReportState call, got none")
	}
}

// AssertNotCalled asserts that ReportState was never called.
func (m *MockClient) AssertNotCalled(t *testing.T) {
	t.Helper()
	if n := m.CallCount(); n > 0 {
		t.Errorf("expected no ReportState calls, got %d", n)
	}
}
