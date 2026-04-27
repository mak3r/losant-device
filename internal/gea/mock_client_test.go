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
	"errors"
	"sync"
	"testing"
)

var ctx = context.Background()

func TestMockClient_DefaultAcceptsAll(t *testing.T) {
	m := NewMockClient()
	err := m.ReportState(ctx, StatePayload{
		DeviceID:   "dev-1",
		Attributes: map[string]interface{}{"health_score": 95},
	})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if m.CallCount() != 1 {
		t.Errorf("CallCount: got %d, want 1", m.CallCount())
	}
}

func TestMockClient_SetError(t *testing.T) {
	m := NewMockClient()
	want := errors.New("gea unavailable")
	m.SetError(want)

	err := m.ReportState(ctx, StatePayload{DeviceID: "dev-1"})
	if !errors.Is(err, want) {
		t.Errorf("got %v, want %v", err, want)
	}
	// Calls are still recorded even when returning an error.
	if m.CallCount() != 1 {
		t.Errorf("CallCount after error: got %d, want 1", m.CallCount())
	}
}

func TestMockClient_SetErrorNilRestoresDefault(t *testing.T) {
	m := NewMockClient()
	m.SetError(errors.New("down"))
	m.SetError(nil)

	if err := m.ReportState(ctx, StatePayload{DeviceID: "d"}); err != nil {
		t.Errorf("after SetError(nil): got error %v", err)
	}
}

func TestMockClient_AssertPayloadContains(t *testing.T) {
	m := NewMockClient()
	m.ReportState(ctx, StatePayload{
		DeviceID: "dev-cluster",
		Attributes: map[string]interface{}{
			"health_score": 87,
			"ready_nodes":  3,
		},
	})
	m.ReportState(ctx, StatePayload{
		DeviceID: "dev-node-1",
		Attributes: map[string]interface{}{
			"health_score": 100,
		},
	})

	// Use a recorder to capture any test failures from the helper itself.
	sub := &testing.T{}
	m.AssertPayloadContains(sub, "dev-cluster", "health_score", 87)
	if sub.Failed() {
		t.Error("AssertPayloadContains: expected pass for matching attribute")
	}

	sub2 := &testing.T{}
	m.AssertPayloadContains(sub2, "dev-cluster", "health_score", 50)
	if !sub2.Failed() {
		t.Error("AssertPayloadContains: expected failure for wrong value")
	}

	sub3 := &testing.T{}
	m.AssertPayloadContains(sub3, "dev-missing", "health_score", 87)
	if !sub3.Failed() {
		t.Error("AssertPayloadContains: expected failure for unknown deviceID")
	}
}

func TestMockClient_AssertCalled(t *testing.T) {
	m := NewMockClient()

	sub := &testing.T{}
	m.AssertCalled(sub)
	if !sub.Failed() {
		t.Error("AssertCalled: expected failure when no calls recorded")
	}

	m.ReportState(ctx, StatePayload{DeviceID: "d"})
	sub2 := &testing.T{}
	m.AssertCalled(sub2)
	if sub2.Failed() {
		t.Error("AssertCalled: expected pass after one call")
	}
}

func TestMockClient_AssertNotCalled(t *testing.T) {
	m := NewMockClient()

	sub := &testing.T{}
	m.AssertNotCalled(sub)
	if sub.Failed() {
		t.Error("AssertNotCalled: expected pass when no calls recorded")
	}

	m.ReportState(ctx, StatePayload{DeviceID: "d"})
	sub2 := &testing.T{}
	m.AssertNotCalled(sub2)
	if !sub2.Failed() {
		t.Error("AssertNotCalled: expected failure after one call")
	}
}

func TestMockClient_Reset(t *testing.T) {
	m := NewMockClient()
	m.SetError(errors.New("err"))
	m.ReportState(ctx, StatePayload{DeviceID: "d"})

	m.Reset()
	if m.CallCount() != 0 {
		t.Errorf("after Reset: CallCount = %d, want 0", m.CallCount())
	}
	if err := m.ReportState(ctx, StatePayload{DeviceID: "d"}); err != nil {
		t.Errorf("after Reset: unexpected error %v", err)
	}
}

func TestMockClient_ConcurrentAccess(t *testing.T) {
	m := NewMockClient()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.ReportState(ctx, StatePayload{DeviceID: "d", Attributes: map[string]interface{}{"k": 1}})
		}()
	}
	wg.Wait()
	if m.CallCount() != 20 {
		t.Errorf("concurrent calls: got %d, want 20", m.CallCount())
	}
}

func TestMockClient_CustomHandlerFunc(t *testing.T) {
	m := NewMockClient()
	var seen []string
	m.ReportStateFunc = func(_ context.Context, p StatePayload) error {
		seen = append(seen, p.DeviceID)
		return nil
	}

	m.ReportState(ctx, StatePayload{DeviceID: "dev-a"})
	m.ReportState(ctx, StatePayload{DeviceID: "dev-b"})

	if len(seen) != 2 || seen[0] != "dev-a" || seen[1] != "dev-b" {
		t.Errorf("custom handler: got %v, want [dev-a dev-b]", seen)
	}
}
