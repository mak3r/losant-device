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

package testenv

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"
)

const losantAPI = "https://api.losant.com"

// httpClient is shared across subtests; 10 s timeout is generous for CI.
var httpClient = &http.Client{Timeout: 10 * time.Second}

// TestLosantAPIConnectivity verifies that the Losant REST API is reachable and
// that the configured credentials are accepted. Skips when HasAPICredentials
// returns false (missing or placeholder values in env.test.local).
//
// This test is intentionally diagnostic: it tries both known Losant auth
// approaches and records which one succeeds so the developer implementing
// internal/losant/client.go knows the exact mechanism to use.
func TestLosantAPIConnectivity(t *testing.T) {
	creds, _ := LoadCredentials()
	if !creds.HasAPICredentials() {
		t.Skip("Losant API credentials not configured — set APPLICATION_ID, ACCESS_KEY, ACCESS_SECRET in test/env.test.local")
	}

	t.Run("api_reachable", func(t *testing.T) {
		resp, err := httpClient.Get(losantAPI + "/applications/" + creds.ApplicationID)
		if err != nil {
			t.Fatalf("could not reach api.losant.com: %v", err)
		}
		defer resp.Body.Close()
		// 401 is expected (no auth yet) — anything but a network error confirms reachability.
		t.Logf("GET /applications/%s → %d", creds.ApplicationID, resp.StatusCode)
		if resp.StatusCode == 0 {
			t.Fatal("empty status code — network error")
		}
	})

	t.Run("auth_with_access_key_as_bearer", func(t *testing.T) {
		// Attempt 1: use ACCESS_KEY directly as a Bearer token.
		// Some Losant API token types (Application API Tokens) work this way.
		token, status := bearerGet(t,
			losantAPI+"/applications/"+creds.ApplicationID,
			creds.AccessKey,
		)
		t.Logf("Bearer=ACCESS_KEY → %d", status)
		if status == http.StatusOK {
			t.Log("✓ ACCESS_KEY works directly as Bearer token")
			verifyApplicationResponse(t, token, creds.ApplicationID)
			return
		}
		t.Logf("ACCESS_KEY-as-Bearer failed (%d) — this is expected for key+secret pairs", status)
	})

	t.Run("auth_device_exchange", func(t *testing.T) {
		// Attempt 2: POST /auth/device with key + secret + clusterDeviceID.
		// This is the standard Losant device credential flow. Requires a real
		// ClusterDeviceID; skips if it is still a placeholder.
		if !isReal(creds.ClusterDeviceID) {
			t.Skip("LOSANT_CLUSTER_DEVICE_ID not set — skipping device auth exchange")
		}

		body, _ := json.Marshal(map[string]string{
			"deviceId": creds.ClusterDeviceID,
			"key":      creds.AccessKey,
			"secret":   creds.AccessSecret,
		})
		resp, err := httpClient.Post(
			losantAPI+"/auth/device",
			"application/json",
			bytes.NewReader(body),
		)
		if err != nil {
			t.Fatalf("POST /auth/device: %v", err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		t.Logf("POST /auth/device → %d: %s", resp.StatusCode, truncate(raw, 200))

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("device auth failed (%d): %s", resp.StatusCode, raw)
		}

		var result map[string]interface{}
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatalf("parse auth response: %v", err)
		}
		tokenVal, ok := result["token"].(string)
		if !ok || tokenVal == "" {
			t.Fatalf("auth response missing token field: %s", raw)
		}
		t.Log("✓ device auth exchange succeeded — Bearer token obtained")

		// Verify the token actually works for a read call.
		_, status := bearerGet(t,
			losantAPI+"/applications/"+creds.ApplicationID,
			tokenVal,
		)
		t.Logf("GET /applications with device token → %d", status)
		if status != http.StatusOK {
			t.Logf("device token cannot read application (%d) — limited scope as expected", status)
		} else {
			t.Log("✓ device token has application read scope")
		}
	})
}

// bearerGet issues GET url with Authorization: Bearer token and returns
// (body, statusCode). Non-fatal on HTTP-level errors so callers can log them.
func bearerGet(t *testing.T, url, token string) ([]byte, int) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Logf("GET %s: %v", url, err)
		return nil, 0
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return body, resp.StatusCode
}

// verifyApplicationResponse checks that the response body contains the expected applicationId.
func verifyApplicationResponse(t *testing.T, body []byte, wantID string) {
	t.Helper()
	if len(body) == 0 {
		return
	}
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return
	}
	gotID, _ := result["applicationId"].(string)
	if gotID != wantID {
		t.Errorf("application response: applicationId=%q, want %q", gotID, wantID)
	} else {
		t.Logf("✓ application verified: name=%v", result["name"])
	}
}

func truncate(b []byte, n int) string {
	s := string(b)
	if len(s) > n {
		return fmt.Sprintf("%s…(%d chars total)", s[:n], len(s))
	}
	return s
}
