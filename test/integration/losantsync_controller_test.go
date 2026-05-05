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

package integration_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	losantv1alpha1 "github.com/mak3r/losant-device/api/v1alpha1"
)

const (
	timeout  = 10 * time.Second
	interval = 250 * time.Millisecond
)

func baseLosantSync(name string) *losantv1alpha1.LosantSync {
	return &losantv1alpha1.LosantSync{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Finalizers: []string{"losant.io/device-cleanup"},
		},
		Spec: losantv1alpha1.LosantSyncSpec{
			ApplicationID: "app-123",
			ProvisioningSecretRef: losantv1alpha1.SecretRef{
				Name:      "losant-creds",
				Namespace: "default",
			},
			ClusterName: "test-cluster",
			Region:      "us-east",
			Interval:    "1m",
			GEA: losantv1alpha1.GEASpec{
				ServiceRef: "losant-gea",
				Port:       8080,
			},
		},
	}
}

var _ = Describe("LosantSyncReconciler", func() {
	Context("first reconcile", func() {
		It("reaches Phase=Active and sets NextScheduledTime", func() {
			ls := baseLosantSync("test-first-reconcile")
			Expect(k8sClient.Create(ctx, ls)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, ls) })

			Eventually(func(g Gomega) {
				var got losantv1alpha1.LosantSync
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: ls.Name}, &got)).To(Succeed())
				g.Expect(got.Status.Phase).To(Equal(losantv1alpha1.PhaseActive))
				g.Expect(got.Status.NextScheduledTime).NotTo(BeNil())
			}, timeout, interval).Should(Succeed())
		})
	})

	Context("suspend", func() {
		It("sets Phase=Suspended when Spec.Suspend=true", func() {
			ls := baseLosantSync("test-suspended")
			ls.Spec.Suspend = true
			Expect(k8sClient.Create(ctx, ls)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, ls) })

			Eventually(func(g Gomega) {
				var got losantv1alpha1.LosantSync
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: ls.Name}, &got)).To(Succeed())
				g.Expect(got.Status.Phase).To(Equal(losantv1alpha1.PhaseSuspended))
			}, timeout, interval).Should(Succeed())
		})
	})

	Context("schedule check", func() {
		It("does not re-enter Provisioning when NextScheduledTime is in the future", func() {
			ls := baseLosantSync("test-schedule-future")
			Expect(k8sClient.Create(ctx, ls)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, ls) })

			// Wait for first reconcile to complete (reaches Active with mock clients), then override status.
			Eventually(func(g Gomega) {
				var got losantv1alpha1.LosantSync
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: ls.Name}, &got)).To(Succeed())
				g.Expect(got.Status.Phase).To(Equal(losantv1alpha1.PhaseActive))
			}, timeout, interval).Should(Succeed())

			// Override status to Active with a future NextScheduledTime.
			var got losantv1alpha1.LosantSync
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: ls.Name}, &got)).To(Succeed())
			future := metav1.NewTime(time.Now().Add(10 * time.Minute))
			got.Status.Phase = losantv1alpha1.PhaseActive
			got.Status.NextScheduledTime = &future
			Expect(k8sClient.Status().Update(ctx, &got)).To(Succeed())

			// Phase should remain Active for the observation window.
			Consistently(func(g Gomega) {
				var current losantv1alpha1.LosantSync
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: ls.Name}, &current)).To(Succeed())
				g.Expect(current.Status.Phase).To(Equal(losantv1alpha1.PhaseActive))
			}, 2*time.Second, interval).Should(Succeed())
		})
	})

	Context("not-found", func() {
		It("does not error when reconciling a deleted resource", func() {
			ls := baseLosantSync("test-not-found")
			Expect(k8sClient.Create(ctx, ls)).To(Succeed())
			Expect(k8sClient.Delete(ctx, ls)).To(Succeed())
			// Give the reconciler time to process the deletion; no panic/error expected.
			time.Sleep(500 * time.Millisecond)
		})
	})

	Context("cluster tags", func() {
		It("forwards ClusterTags spec fields to EnsureClusterDevice on reconcile", func() {
			ls := baseLosantSync("test-cluster-tags-fwd")
			ls.Spec.Tags = losantv1alpha1.ClusterTags{
				Manager: "https://mgmt.example.com",
				UID:     "uid-test-123",
				GPS:     "37.7749,-122.4194",
			}
			Expect(k8sClient.Create(ctx, ls)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, ls) })

			Eventually(func(g Gomega) {
				var got losantv1alpha1.LosantSync
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: ls.Name}, &got)).To(Succeed())
				g.Expect(got.Status.Phase).To(Equal(losantv1alpha1.PhaseActive))
			}, timeout, interval).Should(Succeed())

			calls := mockLosantClient.GetEnsureClusterDeviceCalls()
			found := false
			for _, c := range calls {
				if c.Spec.Tags.Manager == "https://mgmt.example.com" &&
					c.Spec.Tags.UID == "uid-test-123" &&
					c.Spec.Tags.GPS == "37.7749,-122.4194" {
					found = true
					break
				}
			}
			Expect(found).To(BeTrue(), "EnsureClusterDevice must have been called with all three ClusterTag fields set")
		})
	})
})
