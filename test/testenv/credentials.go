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

// Complete returns true when all four credential fields are non-empty.
func (c Credentials) Complete() bool {
	return c.ApplicationID != "" &&
		c.AccessKey != "" &&
		c.AccessSecret != "" &&
		c.ClusterDeviceID != ""
}

// envFile is the path to the gitignored local credentials file, relative to
// this source file so it works regardless of where `go test` is invoked from.
func envFilePath() string {
	_, thisFile, _, _ := runtime.Caller(0)
	// thisFile is .../test/testenv/credentials.go; go up two dirs to reach the
	// repo root, then back down to test/.env.test.local.
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	return filepath.Join(repoRoot, "test", ".env.test.local")
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
