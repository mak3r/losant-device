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

// Package testenv provides helpers for loading test credentials without
// committing secrets to the repository.
//
// Credentials are resolved in this order:
//  1. Environment variables (LOSANT_APPLICATION_ID, LOSANT_ACCESS_KEY, etc.)
//  2. test/.env.test.local — a gitignored file for local development
//
// Tests that require real credentials should call LoadCredentials and call
// t.Skip / ginkgo.Skip when the second return value is false, so the suite
// remains green in CI environments without real keys.
package testenv

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Credentials holds the Losant API credentials needed for integration tests
// that call the real Losant REST API or verify GEA payload delivery.
type Credentials struct {
	// ApplicationID is the Losant Application ID that owns all cluster devices.
	ApplicationID string
	// AccessKey is the Losant API access key used for device provisioning.
	AccessKey string
	// AccessSecret is the Losant API access secret paired with AccessKey.
	AccessSecret string
	// ClusterDeviceID is the Losant device ID of the Edge Compute (GEA) device.
	ClusterDeviceID string
}

// Complete returns true when all four credential fields are populated with
// real (non-placeholder) values.
func (c Credentials) Complete() bool {
	return isReal(c.ApplicationID) &&
		isReal(c.AccessKey) &&
		isReal(c.AccessSecret) &&
		isReal(c.ClusterDeviceID)
}

// HasAPICredentials returns true when the three fields needed for Losant REST
// API calls are populated. ClusterDeviceID is not required here because it may
// not be provisioned until the operator first runs.
func (c Credentials) HasAPICredentials() bool {
	return isReal(c.ApplicationID) &&
		isReal(c.AccessKey) &&
		isReal(c.AccessSecret)
}

// isReal returns false for empty strings and for obvious placeholder values
// left over from the example file (e.g. "your-access-key-here").
// A value is a placeholder only when it BOTH starts with "your-" AND ends
// with "-here", matching the exact pattern used in env.test.local.example.
func isReal(s string) bool {
	if s == "" {
		return false
	}
	lower := strings.ToLower(s)
	return !(strings.HasPrefix(lower, "your-") && strings.HasSuffix(lower, "-here"))
}

// envFilePath returns the path to the gitignored local credentials file.
// Checks test/env.test.local first (no dot prefix), then .env.test.local,
// so either naming convention works.
func envFilePath() string {
	_, thisFile, _, _ := runtime.Caller(0)
	// thisFile is .../test/testenv/credentials.go; go up two dirs to reach the
	// repo root, then back into the test/ directory.
	testDir := filepath.Join(filepath.Dir(thisFile), "..")
	for _, name := range []string{"env.test.local", ".env.test.local"} {
		p := filepath.Join(testDir, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return filepath.Join(testDir, "env.test.local")
}

// LoadCredentials reads credentials from environment variables, falling back
// to test/.env.test.local when env vars are absent.
//
// Returns (creds, true) when all four fields are populated, or (zero, false)
// when credentials are incomplete so callers can skip rather than fail.
func LoadCredentials() (Credentials, bool) {
	// Seed from env vars first.
	creds := Credentials{
		ApplicationID:   os.Getenv("LOSANT_APPLICATION_ID"),
		AccessKey:       os.Getenv("LOSANT_ACCESS_KEY"),
		AccessSecret:    os.Getenv("LOSANT_ACCESS_SECRET"),
		ClusterDeviceID: os.Getenv("LOSANT_CLUSTER_DEVICE_ID"),
	}

	// If any field is missing, try the local file.
	if !creds.Complete() {
		if file, err := os.Open(envFilePath()); err == nil {
			defer file.Close()
			overrides := parseEnvFile(file)
			if creds.ApplicationID == "" {
				creds.ApplicationID = overrides["LOSANT_APPLICATION_ID"]
			}
			if creds.AccessKey == "" {
				creds.AccessKey = overrides["LOSANT_ACCESS_KEY"]
			}
			if creds.AccessSecret == "" {
				creds.AccessSecret = overrides["LOSANT_ACCESS_SECRET"]
			}
			if creds.ClusterDeviceID == "" {
				creds.ClusterDeviceID = overrides["LOSANT_CLUSTER_DEVICE_ID"]
			}
		}
	}

	return creds, creds.Complete()
}

// parseEnvFile reads KEY=VALUE pairs from r, ignoring blank lines and # comments.
// Inline comments and surrounding whitespace are stripped. Quoted values have
// their outer quotes removed.
func parseEnvFile(r *os.File) map[string]string {
	result := make(map[string]string)
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Strip inline comment.
		if idx := strings.Index(line, " #"); idx != -1 {
			line = strings.TrimSpace(line[:idx])
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		// Strip matching outer quotes.
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		if key != "" {
			result[key] = value
		}
	}
	return result
}
