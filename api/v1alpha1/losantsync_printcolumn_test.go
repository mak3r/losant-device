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

package v1alpha1_test

import (
	"encoding/json"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	losantv1alpha1 "github.com/mak3r/losant-device/api/v1alpha1"
)

// resolveStatusField marshals a LosantSync to JSON and returns the value at
// .status.<fieldName>, or ("", false) if the path does not exist.
func resolveStatusField(t *testing.T, ls losantv1alpha1.LosantSync, fieldName string) (interface{}, bool) {
	t.Helper()
	b, err := json.Marshal(ls)
	if err != nil {
		t.Fatalf("marshal LosantSync: %v", err)
	}
	var root map[string]interface{}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("unmarshal LosantSync JSON: %v", err)
	}
	status, ok := root["status"].(map[string]interface{})
	if !ok {
		return nil, false
	}
	val, ok := status[fieldName]
	return val, ok
}

// TestNextSyncPrintColumnJSONPath verifies that the NEXT SYNC printcolumn JSONPath
// (.status.nextScheduledTime) resolves to a non-empty value when NextScheduledTime is set.
func TestNextSyncPrintColumnJSONPath(t *testing.T) {
	ts := metav1.NewTime(time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC))
	ls := losantv1alpha1.LosantSync{
		Status: losantv1alpha1.LosantSyncStatus{
			NextScheduledTime: &ts,
		},
	}

	val, ok := resolveStatusField(t, ls, "nextScheduledTime")
	if !ok {
		t.Fatal("NEXT SYNC column: .status.nextScheduledTime not present in JSON — printcolumn JSONPath will not resolve")
	}
	if val == nil || val == "" {
		t.Errorf("NEXT SYNC column: .status.nextScheduledTime is empty; got %v", val)
	}
}

// TestLastSyncPrintColumnJSONPath verifies that the LAST SYNC printcolumn JSONPath
// (.status.lastSyncTime) resolves to a non-empty value when LastSyncTime is set.
func TestLastSyncPrintColumnJSONPath(t *testing.T) {
	ts := metav1.NewTime(time.Date(2026, 5, 4, 10, 30, 0, 0, time.UTC))
	ls := losantv1alpha1.LosantSync{
		Status: losantv1alpha1.LosantSyncStatus{
			LastSyncTime: &ts,
		},
	}

	val, ok := resolveStatusField(t, ls, "lastSyncTime")
	if !ok {
		t.Fatal("LAST SYNC column: .status.lastSyncTime not present in JSON — printcolumn JSONPath will not resolve")
	}
	if val == nil || val == "" {
		t.Errorf("LAST SYNC column: .status.lastSyncTime is empty; got %v", val)
	}
}

// TestNextSyncPrintColumnOmittedWhenNil verifies that .status.nextScheduledTime is
// absent from the JSON output when the field is nil, matching the omitempty tag.
func TestNextSyncPrintColumnOmittedWhenNil(t *testing.T) {
	ls := losantv1alpha1.LosantSync{
		Status: losantv1alpha1.LosantSyncStatus{
			NextScheduledTime: nil,
		},
	}
	if _, ok := resolveStatusField(t, ls, "nextScheduledTime"); ok {
		t.Error("NEXT SYNC column: .status.nextScheduledTime should be absent in JSON when nil (omitempty)")
	}
}

// TestLastSyncPrintColumnOmittedWhenNil verifies that .status.lastSyncTime is absent
// from the JSON output when the field is nil, matching the omitempty tag.
func TestLastSyncPrintColumnOmittedWhenNil(t *testing.T) {
	ls := losantv1alpha1.LosantSync{
		Status: losantv1alpha1.LosantSyncStatus{
			LastSyncTime: nil,
		},
	}
	if _, ok := resolveStatusField(t, ls, "lastSyncTime"); ok {
		t.Error("LAST SYNC column: .status.lastSyncTime should be absent in JSON when nil (omitempty)")
	}
}
