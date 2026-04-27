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
	"context"
	"fmt"
	"sync/atomic"
	"time"

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
			"accessKey":    "e2e-placeholder-key",
			"accessSecret": "e2e-placeholder-secret",
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
