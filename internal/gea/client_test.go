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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	losantv1alpha1 "github.com/mak3r/losant-device/api/v1alpha1"
)

// newTestHTTPClient returns an HTTPClient pointed at the given httptest server.
func newTestHTTPClient(serverURL string) *HTTPClient {
	return &HTTPClient{
		endpoint:   serverURL + "/state",
		httpClient: &http.Client{},
	}
}

// --- NewHTTPClient ---

func TestNewHTTPClient_EndpointFormat(t *testing.T) {
	spec := losantv1alpha1.GEASpec{ServiceRef: "losant-gea", Port: 8080}
	c := NewHTTPClient(spec)
	want := "http://losant-gea:8080/state"
	if c.endpoint != want {
		t.Errorf("endpoint: got %q, want %q", c.endpoint, want)
	}
}

// --- ReportState ---

func TestReportState_PostsToState(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	payload := StatePayload{DeviceID: "dev-1", Attributes: map[string]interface{}{"cpu": 42}}
	if err := newTestHTTPClient(srv.URL).ReportState(context.Background(), payload); err != nil {
		t.Fatalf("ReportState: %v", err)
	}
	if gotPath != "/state" {
		t.Errorf("path: got %q, want %q", gotPath, "/state")
	}
}

func TestReportState_UsesPostMethod(t *testing.T) {
	var gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	payload := StatePayload{DeviceID: "dev-1", Attributes: map[string]interface{}{}}
	if err := newTestHTTPClient(srv.URL).ReportState(context.Background(), payload); err != nil {
		t.Fatalf("ReportState: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method: got %q, want %q", gotMethod, http.MethodPost)
	}
}

func TestReportState_JSONBody_DeviceIDAndData(t *testing.T) {
	var body map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	payload := StatePayload{
		DeviceID:   "dev-abc",
		Attributes: map[string]interface{}{"cpu": 55.5, "mem": 1024},
	}
	if err := newTestHTTPClient(srv.URL).ReportState(context.Background(), payload); err != nil {
		t.Fatalf("ReportState: %v", err)
	}

	if body["deviceId"] != "dev-abc" {
		t.Errorf("deviceId: got %v, want %q", body["deviceId"], "dev-abc")
	}
	data, ok := body["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data field missing or wrong type: %v", body["data"])
	}
	if data["cpu"] != 55.5 {
		t.Errorf("data.cpu: got %v, want 55.5", data["cpu"])
	}
	if data["mem"] != float64(1024) {
		t.Errorf("data.mem: got %v, want 1024", data["mem"])
	}
}

func TestReportState_Timestamp_Included(t *testing.T) {
	var body map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ts := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	payload := StatePayload{
		DeviceID:   "dev-ts",
		Attributes: map[string]interface{}{"x": 1},
		Timestamp:  &ts,
	}
	if err := newTestHTTPClient(srv.URL).ReportState(context.Background(), payload); err != nil {
		t.Fatalf("ReportState: %v", err)
	}
	if _, ok := body["time"]; !ok {
		t.Error("expected 'time' field in JSON body when Timestamp is set, got none")
	}
}

func TestReportState_Timestamp_Omitted(t *testing.T) {
	var body map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	payload := StatePayload{
		DeviceID:   "dev-no-ts",
		Attributes: map[string]interface{}{"x": 1},
		Timestamp:  nil,
	}
	if err := newTestHTTPClient(srv.URL).ReportState(context.Background(), payload); err != nil {
		t.Fatalf("ReportState: %v", err)
	}
	if _, ok := body["time"]; ok {
		t.Error("unexpected 'time' field in JSON body when Timestamp is nil")
	}
}

func TestReportState_ContentTypeHeader(t *testing.T) {
	var gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	payload := StatePayload{DeviceID: "dev-ct", Attributes: map[string]interface{}{}}
	if err := newTestHTTPClient(srv.URL).ReportState(context.Background(), payload); err != nil {
		t.Fatalf("ReportState: %v", err)
	}
	if !strings.HasPrefix(gotCT, "application/json") {
		t.Errorf("Content-Type: got %q, want application/json", gotCT)
	}
}

func TestReportState_4xx_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"bad request"}`, http.StatusBadRequest)
	}))
	defer srv.Close()

	payload := StatePayload{DeviceID: "dev-err", Attributes: map[string]interface{}{}}
	if err := newTestHTTPClient(srv.URL).ReportState(context.Background(), payload); err == nil {
		t.Fatal("expected error on 400 response, got nil")
	}
}

func TestReportState_5xx_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"internal error"}`, http.StatusInternalServerError)
	}))
	defer srv.Close()

	payload := StatePayload{DeviceID: "dev-5xx", Attributes: map[string]interface{}{}}
	if err := newTestHTTPClient(srv.URL).ReportState(context.Background(), payload); err == nil {
		t.Fatal("expected error on 500 response, got nil")
	}
}

func TestReportState_Success_ReturnsNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	payload := StatePayload{DeviceID: "dev-ok", Attributes: map[string]interface{}{"health": "good"}}
	if err := newTestHTTPClient(srv.URL).ReportState(context.Background(), payload); err != nil {
		t.Fatalf("ReportState: unexpected error: %v", err)
	}
}
