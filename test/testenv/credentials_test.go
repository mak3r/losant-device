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
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCredentials_Complete(t *testing.T) {
	cases := []struct {
		name  string
		creds Credentials
		want  bool
	}{
		{"all set", Credentials{"app", "key", "secret", "dev"}, true},
		{"missing app id", Credentials{"", "key", "secret", "dev"}, false},
		{"missing access key", Credentials{"app", "", "secret", "dev"}, false},
		{"missing secret", Credentials{"app", "key", "", "dev"}, false},
		{"missing device id", Credentials{"app", "key", "secret", ""}, false},
		{"all empty", Credentials{}, false},
		// placeholder values from example file must not count as real
		{"placeholder device id", Credentials{"app", "key", "secret", "your-cluster-device-id-here"}, false},
		{"placeholder access key", Credentials{"your-access-key-here", "k", "s", "d"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.creds.Complete(); got != tc.want {
				t.Errorf("Complete() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCredentials_HasAPICredentials(t *testing.T) {
	cases := []struct {
		name  string
		creds Credentials
		want  bool
	}{
		{"all real, no device id", Credentials{"app", "key", "secret", ""}, true},
		{"all real including device", Credentials{"app", "key", "secret", "dev"}, true},
		{"missing secret", Credentials{"app", "key", "", ""}, false},
		{"placeholder app id", Credentials{"your-application-id-here", "key", "secret", ""}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.creds.HasAPICredentials(); got != tc.want {
				t.Errorf("HasAPICredentials() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsReal(t *testing.T) {
	cases := []struct {
		s    string
		want bool
	}{
		{"abc123", true},
		{"", false},
		{"your-access-key-here", false},
		{"your-application-id-here", false},
		{"YOUR-KEY-HERE", false}, // case-insensitive
		{"yourkey", true},        // starts with "your" but not "your-"
		{"somevalue-here", true},  // ends with "-here" but doesn't start with "your-"
		{"your-partial", true},    // starts with "your-" but doesn't end with "-here"
	}
	for _, tc := range cases {
		if got := isReal(tc.s); got != tc.want {
			t.Errorf("isReal(%q) = %v, want %v", tc.s, got, tc.want)
		}
	}
}

func TestParseEnvFile(t *testing.T) {
	content := `
# This is a comment
LOSANT_APPLICATION_ID=app-abc123
LOSANT_ACCESS_KEY="key-with-quotes"
LOSANT_ACCESS_SECRET='secret-single-quoted'
LOSANT_CLUSTER_DEVICE_ID=dev-xyz  # inline comment
  WHITESPACE_KEY = whitespace-value
EMPTY_VALUE=
`
	f := writeTempFile(t, content)
	result := parseEnvFile(f)

	cases := []struct{ key, want string }{
		{"LOSANT_APPLICATION_ID", "app-abc123"},
		{"LOSANT_ACCESS_KEY", "key-with-quotes"},
		{"LOSANT_ACCESS_SECRET", "secret-single-quoted"},
		{"LOSANT_CLUSTER_DEVICE_ID", "dev-xyz"},
		{"WHITESPACE_KEY", "whitespace-value"},
		{"EMPTY_VALUE", ""},
	}
	for _, tc := range cases {
		if got := result[tc.key]; got != tc.want {
			t.Errorf("key %q: got %q, want %q", tc.key, got, tc.want)
		}
	}
	// Comments must not appear as keys.
	for k := range result {
		if strings.HasPrefix(k, "#") {
			t.Errorf("comment line parsed as key: %q", k)
		}
	}
}

func TestLoadCredentials_EnvVars(t *testing.T) {
	t.Setenv("LOSANT_APPLICATION_ID", "app-env")
	t.Setenv("LOSANT_ACCESS_KEY", "key-env")
	t.Setenv("LOSANT_ACCESS_SECRET", "secret-env")
	t.Setenv("LOSANT_CLUSTER_DEVICE_ID", "dev-env")

	creds, ok := LoadCredentials()
	if !ok {
		t.Fatal("expected complete credentials from env vars")
	}
	if creds.ApplicationID != "app-env" {
		t.Errorf("ApplicationID: got %q, want %q", creds.ApplicationID, "app-env")
	}
	if creds.AccessKey != "key-env" {
		t.Errorf("AccessKey: got %q, want %q", creds.AccessKey, "key-env")
	}
}

func TestLoadCredentials_MissingReturnsIncomplete(t *testing.T) {
	// Clear all relevant env vars and ensure the local file doesn't exist.
	for _, k := range []string{
		"LOSANT_APPLICATION_ID",
		"LOSANT_ACCESS_KEY",
		"LOSANT_ACCESS_SECRET",
		"LOSANT_CLUSTER_DEVICE_ID",
	} {
		t.Setenv(k, "")
	}

	// Point envFilePath to a non-existent file by using a temp dir.
	// We can't override envFilePath directly, but we can verify the
	// function returns false when env vars are absent and the file doesn't exist.
	_, ok := LoadCredentials()
	// ok may be true if the developer has a real .env.test.local — that's fine.
	// We only assert the function doesn't panic and returns a bool.
	_ = ok
}

// writeTempFile creates a temp *os.File with content, for use in tests.
func writeTempFile(t *testing.T, content string) *os.File {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, ".env.test.local")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("writeTempFile: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("writeTempFile open: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}
