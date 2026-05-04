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

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	losantv1alpha1 "github.com/mak3r/losant-device/api/v1alpha1"
)

const (
	// e2eNamespace holds Secrets referenced by test LosantSync resources.
	// LosantSync itself is cluster-scoped, but Secrets are namespaced.
	e2eNamespace = "losant-e2e"

	pollInterval = 500 * time.Millisecond
	pollTimeout  = 30 * time.Second
)

var (
	// runID makes names unique across concurrent test runs against the same cluster.
	runID   = time.Now().UnixMilli() % 10_000
	nameSeq int64
)

// uniqueName returns a name that is unique within a test run and across runs.
func uniqueName(base string) string {
	seq := atomic.AddInt64(&nameSeq, 1)
	return fmt.Sprintf("%s-%04d-%d", base, seq, runID)
}

// newLosantSync returns a minimal valid LosantSync using an interval schedule.
// Pass interval="" to omit the interval field (required when setting CronSchedule instead).
func newLosantSync(name, interval string) *losantv1alpha1.LosantSync {
	ls := &losantv1alpha1.LosantSync{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: losantv1alpha1.LosantSyncSpec{
			ApplicationID: "e2e-test-app-id",
			ProvisioningSecretRef: losantv1alpha1.SecretRef{
				Name:      "losant-credentials",
				Namespace: e2eNamespace,
			},
			ClusterName: "e2e-test-cluster",
			Region:      "us-east-1",
			GEA: losantv1alpha1.GEASpec{
				ServiceRef: "losant-gea",
				Port:       8080,
			},
		},
	}
	if interval != "" {
		ls.Spec.Interval = interval
	}
	return ls
}

// ensureTestNamespaceAndSecret creates the e2eNamespace and a placeholder
// provisioning secret if they do not already exist.
func ensureTestNamespaceAndSecret(ctx context.Context) {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: e2eNamespace}}
	err := k8sClient.Create(ctx, ns)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred(), "creating test namespace")
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "losant-credentials",
			Namespace: e2eNamespace,
		},
		StringData: map[string]string{
			"api-token": "e2e-placeholder-token",
		},
	}
	err = k8sClient.Create(ctx, secret)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred(), "creating test secret")
	}
}

// getLosantSync fetches the current live state of a LosantSync by name.
func getLosantSync(ctx context.Context, name string) *losantv1alpha1.LosantSync {
	ls := &losantv1alpha1.LosantSync{}
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, ls)).To(Succeed())
	return ls
}

// cleanupLosantSync deletes the named LosantSync and waits for it to be gone.
func cleanupLosantSync(ctx context.Context, name string) {
	ls := &losantv1alpha1.LosantSync{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: name}, ls); apierrors.IsNotFound(err) {
		return
	}
	_ = k8sClient.Delete(ctx, ls)
	Eventually(func() bool {
		err := k8sClient.Get(ctx, types.NamespacedName{Name: name}, &losantv1alpha1.LosantSync{})
		return client.IgnoreNotFound(err) == nil && err != nil
	}, pollTimeout, pollInterval).Should(BeTrue(), "LosantSync %q was not deleted within timeout", name)
}

// getCondition returns the named condition from LosantSync status, or nil if absent.
func getCondition(ctx context.Context, name, condType string) *metav1.Condition {
	ls := getLosantSync(ctx, name)
	for i := range ls.Status.Conditions {
		if ls.Status.Conditions[i].Type == condType {
			return &ls.Status.Conditions[i]
		}
	}
	return nil
}

// listReadyNodeNames returns the names of nodes currently reporting Ready=True.
func listReadyNodeNames(ctx context.Context) []string {
	nodes := &corev1.NodeList{}
	Expect(k8sClient.List(ctx, nodes)).To(Succeed())
	var names []string
	for _, n := range nodes.Items {
		for _, c := range n.Status.Conditions {
			if c.Type == corev1.NodeReady && c.Status == corev1.ConditionTrue {
				names = append(names, n.Name)
			}
		}
	}
	return names
}

// cordonNode marks the named node as unschedulable.
func cordonNode(ctx context.Context, nodeName string) {
	node := &corev1.Node{}
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: nodeName}, node)).To(Succeed())
	node.Spec.Unschedulable = true
	Expect(k8sClient.Update(ctx, node)).To(Succeed())
}

// uncordonNode removes the unschedulable mark from the named node (best-effort, for cleanup).
func uncordonNode(ctx context.Context, nodeName string) {
	node := &corev1.Node{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: nodeName}, node); err != nil {
		return
	}
	node.Spec.Unschedulable = false
	_ = k8sClient.Update(ctx, node)
}

// --- Real Losant REST API helpers (require LOSANT_APP_ID and LOSANT_API_TOKEN env vars) ---

const (
	losantAPIBase     = "https://api.losant.com"
	e2eRealSecretName = "losant-real-credentials"
)

var losantTestHTTPClient = &http.Client{Timeout: 30 * time.Second}

// losantDeviceResp is a minimal Losant REST device representation for E2E test assertions.
type losantDeviceResp struct {
	DeviceID   string           `json:"deviceId"`
	Name       string           `json:"name"`
	Attributes []losantAttrResp `json:"attributes"`
}

// losantAttrResp holds one telemetry attribute returned by the Losant REST API.
type losantAttrResp struct {
	Name     string `json:"name"`
	DataType string `json:"dataType"`
}

// skipUnlessRealLosant skips the current spec when real Losant credentials are absent.
func skipUnlessRealLosant() {
	if os.Getenv("LOSANT_APP_ID") == "" || os.Getenv("LOSANT_API_TOKEN") == "" {
		Skip("LOSANT_APP_ID or LOSANT_API_TOKEN not set — requires real Losant credentials")
	}
}

// losantDo executes an authenticated request against the Losant REST API and returns the
// response body and HTTP status code. Transport-level failures are fatal to the test.
func losantDo(method, path string, payload interface{}) ([]byte, int) {
	token := os.Getenv("LOSANT_API_TOKEN")
	var reqBody io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		Expect(err).NotTo(HaveOccurred(), "marshal losant request payload")
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, losantAPIBase+path, reqBody)
	Expect(err).NotTo(HaveOccurred(), "build losant request")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := losantTestHTTPClient.Do(req)
	Expect(err).NotTo(HaveOccurred(), "losant %s %s", method, path)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	Expect(err).NotTo(HaveOccurred(), "read losant response body")
	return body, resp.StatusCode
}

// losantFetchDevice calls GET /applications/{appID}/devices/{deviceID}.
// Returns (device, statusCode); device is nil when status >= 400.
func losantFetchDevice(appID, deviceID string) (*losantDeviceResp, int) {
	body, status := losantDo(http.MethodGet,
		fmt.Sprintf("/applications/%s/devices/%s", appID, deviceID), nil)
	if status >= 400 {
		return nil, status
	}
	var d losantDeviceResp
	Expect(json.Unmarshal(body, &d)).To(Succeed(), "decode losant device response")
	return &d, status
}

// losantAttrNames extracts attribute names from a device response for use with Gomega matchers.
func losantAttrNames(d *losantDeviceResp) []string {
	names := make([]string, len(d.Attributes))
	for i, a := range d.Attributes {
		names[i] = a.Name
	}
	return names
}

// losantCreateBareDevice creates a Losant device with no attributes and returns its device ID.
func losantCreateBareDevice(appID, name, class string) string {
	payload := map[string]interface{}{
		"name":        name,
		"deviceClass": class,
	}
	body, status := losantDo(http.MethodPost,
		fmt.Sprintf("/applications/%s/devices", appID), payload)
	Expect(status).To(BeNumerically("<", 300),
		"create bare losant device %q: %s", name, string(body))
	var d losantDeviceResp
	Expect(json.Unmarshal(body, &d)).To(Succeed())
	Expect(d.DeviceID).NotTo(BeEmpty())
	return d.DeviceID
}

// losantDeleteDevice deletes a Losant device; ignores 404 (already gone).
func losantDeleteDevice(appID, deviceID string) {
	_, status := losantDo(http.MethodDelete,
		fmt.Sprintf("/applications/%s/devices/%s", appID, deviceID), nil)
	Expect(status).To(Or(BeNumerically("<", 300), Equal(404)),
		"delete losant device %s", deviceID)
}

// ensureRealLosantSecret creates a k8s Secret holding the real Losant API token.
func ensureRealLosantSecret(ctx context.Context) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      e2eRealSecretName,
			Namespace: e2eNamespace,
		},
		StringData: map[string]string{
			"api-token": os.Getenv("LOSANT_API_TOKEN"),
		},
	}
	err := k8sClient.Create(ctx, secret)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred(), "creating real losant credentials secret")
	}
}

// newRealLosantSync returns a LosantSync pointing at the real Losant application and credentials.
// ClusterName is set to name so the canonical device name (k8s-cluster-<name>) is unique per test.
func newRealLosantSync(name, interval string) *losantv1alpha1.LosantSync {
	ls := &losantv1alpha1.LosantSync{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: losantv1alpha1.LosantSyncSpec{
			ApplicationID: os.Getenv("LOSANT_APP_ID"),
			ProvisioningSecretRef: losantv1alpha1.SecretRef{
				Name:      e2eRealSecretName,
				Namespace: e2eNamespace,
			},
			ClusterName: name,
			Region:      "us-east-1",
			GEA: losantv1alpha1.GEASpec{
				ServiceRef: "losant-gea",
				Port:       8080,
			},
		},
	}
	if interval != "" {
		ls.Spec.Interval = interval
	}
	return ls
}
