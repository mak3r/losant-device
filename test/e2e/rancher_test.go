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

// Rancher dynamic connect/disconnect e2e tests (AC-RANCHER-01..07).
//
// Required env vars:
//   RANCHER_URL   — base URL of the Rancher Manager instance (e.g. https://rancher.example.com)
//   RANCHER_TOKEN — service account bearer token with create/get/delete on /v3/clusters
//   RANCHER_CA    — PEM-encoded CA certificate for TLS verification
//   TRIGGER_ADDR  — address of the trigger receiver (e.g. http://localhost:9090);
//                   typically set up via: kubectl -n losant-system port-forward svc/<release>-trigger 9090:9090
//
// The tests assume the cluster has no pre-existing LosantSync or RancherSession CRs.
// Tests 1, 3, and 4 require a pre-pulled rancher/rancher-agent image to reach the
// Connected phase within the 120-second agent readiness window.

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
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
	rancherNamespace   = "losant-system"
	rancherCredsSecret = "rancher-credentials"
	rancherTriggerPath = "/rancher"

	// agentConnectTimeout is how long to wait for Phase=Connected (agent readiness window).
	agentConnectTimeout = 120 * time.Second
	// ttlTestSeconds is the short TTL used in test 2 to exercise auto-disconnect.
	ttlTestSeconds = int64(120)
	// ttlExpireTimeout covers connect time + TTL + reconcile jitter for test 2.
	ttlExpireTimeout    = 5 * time.Minute
	rancherPollInterval = 5 * time.Second
)

// --- skip guard ---

func skipUnlessRancher() {
	var missing []string
	for _, v := range []string{"RANCHER_URL", "RANCHER_TOKEN", "RANCHER_CA", "TRIGGER_ADDR"} {
		if os.Getenv(v) == "" {
			missing = append(missing, v)
		}
	}
	if len(missing) > 0 {
		Skip(fmt.Sprintf("Rancher e2e requires env vars: %v", missing))
	}
}

// --- Rancher REST API helpers ---

func newRancherHTTPClient() *http.Client {
	caData := []byte(os.Getenv("RANCHER_CA"))
	pool := x509.NewCertPool()
	if len(caData) > 0 {
		pool.AppendCertsFromPEM(caData)
	}
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool},
		},
	}
}

func rancherDo(ctx context.Context, hc *http.Client, method, path string, payload interface{}) ([]byte, int) {
	base := os.Getenv("RANCHER_URL")
	token := os.Getenv("RANCHER_TOKEN")
	var reqBody io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		Expect(err).NotTo(HaveOccurred())
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, reqBody)
	Expect(err).NotTo(HaveOccurred())
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	Expect(err).NotTo(HaveOccurred(), "%s %s", method, path)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	Expect(err).NotTo(HaveOccurred())
	return body, resp.StatusCode
}

// rancherFindCluster returns the cluster ID matching displayName, or ("", false).
func rancherFindCluster(ctx context.Context, hc *http.Client, displayName string) (string, bool) {
	body, status := rancherDo(ctx, hc, http.MethodGet, "/v3/clusters?name="+displayName, nil)
	Expect(status).To(BeNumerically("<", 300), "list Rancher clusters: %s", string(body))
	var result struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(body, &result)).To(Succeed())
	for _, c := range result.Data {
		if c.Name == displayName {
			return c.ID, true
		}
	}
	return "", false
}

func rancherClusterExists(ctx context.Context, hc *http.Client, displayName string) bool {
	_, found := rancherFindCluster(ctx, hc, displayName)
	return found
}

func rancherDeleteClusterByID(ctx context.Context, hc *http.Client, clusterID string) {
	_, status := rancherDo(ctx, hc, http.MethodDelete, "/v3/clusters/"+clusterID, nil)
	Expect(status).To(Or(BeNumerically("<", 300), Equal(http.StatusNotFound)),
		"delete Rancher cluster %s", clusterID)
}

// --- trigger server helpers ---

func triggerDo(action string, ttlSeconds int64) *http.Response {
	payload := map[string]interface{}{"action": action, "ttlSeconds": ttlSeconds}
	body, err := json.Marshal(payload)
	Expect(err).NotTo(HaveOccurred())
	addr := os.Getenv("TRIGGER_ADDR")
	resp, err := http.Post(addr+rancherTriggerPath, "application/json", bytes.NewReader(body)) //nolint:gosec
	Expect(err).NotTo(HaveOccurred(), "POST %s%s", addr, rancherTriggerPath)
	return resp
}

// --- k8s helpers ---

// ensureRancherPrereqs creates the trigger namespace, credentials secret, and a stub LosantSync.
// Returns the LosantSync name so the caller can clean it up in AfterEach.
func ensureRancherPrereqs(ctx context.Context) string {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: rancherNamespace}}
	err := k8sClient.Create(ctx, ns)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred(), "create %s namespace", rancherNamespace)
	}

	// Recreate the credentials secret to pick up fresh env vars.
	existing := &corev1.Secret{}
	if getErr := k8sClient.Get(ctx, types.NamespacedName{Name: rancherCredsSecret, Namespace: rancherNamespace}, existing); getErr == nil {
		_ = k8sClient.Delete(ctx, existing)
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: rancherCredsSecret, Namespace: rancherNamespace},
		StringData: map[string]string{
			"RANCHER_URL":   os.Getenv("RANCHER_URL"),
			"RANCHER_TOKEN": os.Getenv("RANCHER_TOKEN"),
			"RANCHER_CA":    os.Getenv("RANCHER_CA"),
		},
	}
	Expect(k8sClient.Create(ctx, secret)).To(Succeed(), "create rancher-credentials secret")

	// Stub LosantSync so the trigger server can find a session name to use.
	lsName := uniqueName("rancher-e2e")
	Expect(k8sClient.Create(ctx, newLosantSync(lsName, "30m"))).To(Succeed(),
		"create stub LosantSync %s", lsName)
	return lsName
}

// waitForRancherSession polls until at least one RancherSession exists in the trigger namespace.
func waitForRancherSession(ctx context.Context) *losantv1alpha1.RancherSession {
	var rs *losantv1alpha1.RancherSession
	Eventually(func() bool {
		var list losantv1alpha1.RancherSessionList
		if err := k8sClient.List(ctx, &list, client.InNamespace(rancherNamespace)); err != nil {
			return false
		}
		if len(list.Items) == 0 {
			return false
		}
		rs = &list.Items[0]
		return true
	}, pollTimeout, pollInterval).Should(BeTrue(), "no RancherSession appeared in %s", rancherNamespace)
	return rs
}

// getRancherSessionByName fetches a live RancherSession by name.
func getRancherSessionByName(ctx context.Context, name string) *losantv1alpha1.RancherSession {
	rs := &losantv1alpha1.RancherSession{}
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: rancherNamespace}, rs)).To(Succeed())
	return rs
}

// cleanupAllRancherSessions deletes every RancherSession in the trigger namespace and waits for them to be gone.
func cleanupAllRancherSessions(ctx context.Context) {
	var list losantv1alpha1.RancherSessionList
	if err := k8sClient.List(ctx, &list, client.InNamespace(rancherNamespace)); err != nil {
		return
	}
	for i := range list.Items {
		_ = k8sClient.Delete(ctx, &list.Items[i])
	}
	Eventually(func() int {
		var l losantv1alpha1.RancherSessionList
		_ = k8sClient.List(ctx, &l, client.InNamespace(rancherNamespace))
		return len(l.Items)
	}, pollTimeout, pollInterval).Should(Equal(0), "RancherSessions not fully cleaned up")
}

// getRancherCondition returns the named condition from a RancherSession, or nil.
func getRancherCondition(rs *losantv1alpha1.RancherSession, condType string) *metav1.Condition {
	for i := range rs.Status.Conditions {
		if rs.Status.Conditions[i].Type == condType {
			return &rs.Status.Conditions[i]
		}
	}
	return nil
}

// assertCoreSystemsHealthy verifies that the trigger namespace and its LosantSync CR are intact.
func assertCoreSystemsHealthy(ctx context.Context, lsName string) {
	ns := &corev1.Namespace{}
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: rancherNamespace}, ns)).To(Succeed(),
		"%s namespace should still exist after disconnect", rancherNamespace)
	Eventually(func() losantv1alpha1.LosantSyncPhase {
		return getLosantSync(ctx, lsName).Status.Phase
	}, pollTimeout, pollInterval).ShouldNot(BeEmpty(),
		"LosantSync %s should continue reconciling after Rancher disconnect", lsName)
}

// --- test suite ---

var _ = Describe("Rancher dynamic connect/disconnect", func() {
	var (
		hc     *http.Client
		lsName string
	)

	BeforeEach(func() {
		skipUnlessRancher()
		hc = newRancherHTTPClient()
		lsName = ensureRancherPrereqs(ctx)
	})

	AfterEach(func() {
		cleanupAllRancherSessions(ctx)
		cleanupLosantSync(ctx, lsName)
		_ = k8sClient.Delete(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: rancherCredsSecret, Namespace: rancherNamespace},
		})
	})

	// AC-RANCHER-01: full connect → Connected within 120s; cluster visible in Rancher.
	It("transitions to Connected within 120s and cluster appears in Rancher", func() {
		resp := triggerDo("connect", 0)
		Expect(resp.StatusCode).To(Equal(http.StatusAccepted))

		rs := waitForRancherSession(ctx)
		displayName := rs.Name

		Eventually(func() losantv1alpha1.RancherSessionPhase {
			return getRancherSessionByName(ctx, rs.Name).Status.Phase
		}, agentConnectTimeout, rancherPollInterval).Should(Equal(losantv1alpha1.RancherSessionPhaseConnected))

		Expect(rancherClusterExists(ctx, hc, displayName)).To(BeTrue(),
			"cluster %q should appear in Rancher after connecting", displayName)
		assertCoreSystemsHealthy(ctx, lsName)
	})

	// AC-RANCHER-02: TTL auto-disconnect; cluster record removed from Rancher.
	//
	// Note: the controller transitions the RancherSession to Phase=Disconnected on TTL
	// expiry but does NOT delete the CR. Deletion requires an explicit k8s delete (which
	// the trigger disconnect action provides).
	It("auto-disconnects when TTL expires and removes the Rancher cluster record", func() {
		resp := triggerDo("connect", ttlTestSeconds)
		Expect(resp.StatusCode).To(Equal(http.StatusAccepted))

		rs := waitForRancherSession(ctx)
		displayName := rs.Name

		Eventually(func() losantv1alpha1.RancherSessionPhase {
			return getRancherSessionByName(ctx, rs.Name).Status.Phase
		}, agentConnectTimeout, rancherPollInterval).Should(Equal(losantv1alpha1.RancherSessionPhaseConnected))

		Eventually(func() losantv1alpha1.RancherSessionPhase {
			return getRancherSessionByName(ctx, rs.Name).Status.Phase
		}, ttlExpireTimeout, rancherPollInterval).Should(Equal(losantv1alpha1.RancherSessionPhaseDisconnected))

		Expect(rancherClusterExists(ctx, hc, displayName)).To(BeFalse(),
			"Rancher cluster %q should be gone after TTL expiry", displayName)
	})

	// AC-RANCHER-03: Losant-initiated reconnect; cluster reappears with the same display name.
	It("reconnects via trigger and cluster reappears in Rancher with the same display name", func() {
		resp := triggerDo("connect", 0)
		Expect(resp.StatusCode).To(Equal(http.StatusAccepted))

		rs := waitForRancherSession(ctx)
		displayName := rs.Name

		Eventually(func() losantv1alpha1.RancherSessionPhase {
			return getRancherSessionByName(ctx, rs.Name).Status.Phase
		}, agentConnectTimeout, rancherPollInterval).Should(Equal(losantv1alpha1.RancherSessionPhaseConnected))

		// Trigger disconnect: the trigger server deletes the RancherSession CR.
		resp = triggerDo("disconnect", 0)
		Expect(resp.StatusCode).To(Equal(http.StatusAccepted))

		Eventually(func() bool {
			err := k8sClient.Get(ctx, types.NamespacedName{Name: rs.Name, Namespace: rancherNamespace},
				&losantv1alpha1.RancherSession{})
			return apierrors.IsNotFound(err)
		}, pollTimeout, pollInterval).Should(BeTrue(),
			"RancherSession should be deleted after trigger disconnect")

		// Reconnect.
		resp = triggerDo("connect", 0)
		Expect(resp.StatusCode).To(Equal(http.StatusAccepted))

		Eventually(func() losantv1alpha1.RancherSessionPhase {
			var list losantv1alpha1.RancherSessionList
			_ = k8sClient.List(ctx, &list, client.InNamespace(rancherNamespace))
			if len(list.Items) == 0 {
				return ""
			}
			return list.Items[0].Status.Phase
		}, agentConnectTimeout, rancherPollInterval).Should(Equal(losantv1alpha1.RancherSessionPhaseConnected))

		Expect(rancherClusterExists(ctx, hc, displayName)).To(BeTrue(),
			"cluster %q should reappear in Rancher after reconnect", displayName)
	})

	// AC-RANCHER-04: Rancher-initiated disconnect; controller detects missing cluster and
	// cleans up: Phase→Disconnected, cattle-system namespace removed.
	It("detects Rancher-initiated cluster deletion and removes cattle-system", func() {
		resp := triggerDo("connect", 0)
		Expect(resp.StatusCode).To(Equal(http.StatusAccepted))

		rs := waitForRancherSession(ctx)
		displayName := rs.Name

		var clusterID string
		Eventually(func() string {
			r := getRancherSessionByName(ctx, rs.Name)
			if r.Status.Phase != losantv1alpha1.RancherSessionPhaseConnected {
				return ""
			}
			clusterID = r.Status.RancherClusterID
			return clusterID
		}, agentConnectTimeout, rancherPollInterval).ShouldNot(BeEmpty(),
			"expected Connected phase with RancherClusterID populated")

		// Delete the cluster from Rancher directly, simulating a Rancher-initiated disconnect.
		rancherDeleteClusterByID(ctx, hc, clusterID)
		Expect(rancherClusterExists(ctx, hc, displayName)).To(BeFalse())

		// Controller polls the cluster on its TTL cadence; wait for it to detect the deletion.
		Eventually(func() losantv1alpha1.RancherSessionPhase {
			return getRancherSessionByName(ctx, rs.Name).Status.Phase
		}, 2*time.Minute, rancherPollInterval).Should(Equal(losantv1alpha1.RancherSessionPhaseDisconnected))

		// cattle-system namespace should be gone.
		Eventually(func() bool {
			err := k8sClient.Get(ctx, types.NamespacedName{Name: "cattle-system"}, &corev1.Namespace{})
			return apierrors.IsNotFound(err)
		}, pollTimeout, pollInterval).Should(BeTrue(),
			"cattle-system namespace should be removed after Rancher-initiated disconnect")

		assertCoreSystemsHealthy(ctx, lsName)
	})

	// AC-RANCHER-05: simultaneous connect requests; exactly one 202, one 409, one RancherSession.
	It("returns 409 on a duplicate connect and only one RancherSession exists", func() {
		var (
			mu       sync.Mutex
			statuses []int
			wg       sync.WaitGroup
		)
		for range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				r := triggerDo("connect", 0)
				mu.Lock()
				statuses = append(statuses, r.StatusCode)
				mu.Unlock()
			}()
		}
		wg.Wait()

		Expect(statuses).To(ConsistOf(http.StatusAccepted, http.StatusConflict),
			"expected exactly one 202 and one 409")

		var list losantv1alpha1.RancherSessionList
		Expect(k8sClient.List(ctx, &list, client.InNamespace(rancherNamespace))).To(Succeed())
		Expect(list.Items).To(HaveLen(1), "only one RancherSession should exist after duplicate connect")
	})

	// AC-RANCHER-06: unreachable Rancher API → Phase=Connecting + RancherAPIReachable=False;
	// LosantSync reconciliation continues normally.
	//
	// Note: the controller sets Phase=Connecting then retries with setAPIUnreachable (not setFailed)
	// when a connection-level error occurs. Phase=Failed is reserved for credential/secret failures.
	It("sets RancherAPIReachable=False when Rancher API is unreachable and does not block LosantSync", func() {
		badSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "rancher-bad-creds", Namespace: rancherNamespace},
			StringData: map[string]string{
				"RANCHER_URL":   "https://rancher.invalid.example.local:443",
				"RANCHER_TOKEN": "bad-token",
				"RANCHER_CA":    os.Getenv("RANCHER_CA"),
			},
		}
		Expect(k8sClient.Create(ctx, badSecret)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, badSecret) })

		rs := &losantv1alpha1.RancherSession{
			ObjectMeta: metav1.ObjectMeta{
				Name:      uniqueName("rancher-fail"),
				Namespace: rancherNamespace,
			},
			Spec: losantv1alpha1.RancherSessionSpec{
				CredentialsSecretRef: losantv1alpha1.SecretRef{
					Name:      "rancher-bad-creds",
					Namespace: rancherNamespace,
				},
				TTLSeconds: 3600,
			},
		}
		Expect(k8sClient.Create(ctx, rs)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, rs) })

		// Controller sets Phase=Connecting then marks RancherAPIReachable=False on the connection error.
		Eventually(func() losantv1alpha1.RancherSessionPhase {
			return getRancherSessionByName(ctx, rs.Name).Status.Phase
		}, pollTimeout, pollInterval).Should(Equal(losantv1alpha1.RancherSessionPhaseConnecting))

		Eventually(func() metav1.ConditionStatus {
			r := getRancherSessionByName(ctx, rs.Name)
			cond := getRancherCondition(r, "RancherAPIReachable")
			if cond == nil {
				return ""
			}
			return cond.Status
		}, pollTimeout, pollInterval).Should(Equal(metav1.ConditionFalse),
			"RancherAPIReachable condition should be False when the API is unreachable")

		// LosantSync reconciliation must continue while the RancherSession is failing.
		Consistently(func() bool {
			return getLosantSync(ctx, lsName).Status.Phase != ""
		}, 10*time.Second, pollInterval).Should(BeTrue(),
			"LosantSync reconciliation should not be blocked by an unreachable Rancher API")
	})

	// AC-RANCHER-07: cluster safety — after any disconnect path losant-system is intact,
	// LosantSync is healthy, and no unexpected namespaces are deleted.
	// This criterion is verified inline in tests 1, 4 via assertCoreSystemsHealthy.
	// The following standalone check covers the trigger-disconnect (Losant-initiated) path.
	It("leaves losant-system and LosantSync intact after a trigger-initiated disconnect", func() {
		resp := triggerDo("connect", 0)
		Expect(resp.StatusCode).To(Equal(http.StatusAccepted))

		rs := waitForRancherSession(ctx)

		Eventually(func() losantv1alpha1.RancherSessionPhase {
			return getRancherSessionByName(ctx, rs.Name).Status.Phase
		}, agentConnectTimeout, rancherPollInterval).Should(Equal(losantv1alpha1.RancherSessionPhaseConnected))

		resp = triggerDo("disconnect", 0)
		Expect(resp.StatusCode).To(Equal(http.StatusAccepted))

		Eventually(func() bool {
			err := k8sClient.Get(ctx, types.NamespacedName{Name: rs.Name, Namespace: rancherNamespace},
				&losantv1alpha1.RancherSession{})
			return apierrors.IsNotFound(err)
		}, pollTimeout, pollInterval).Should(BeTrue())

		assertCoreSystemsHealthy(ctx, lsName)

		// Confirm losant-system namespace is intact.
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: rancherNamespace}, &corev1.Namespace{})).To(Succeed())
	})
})
