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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	losantv1alpha1 "github.com/mak3r/losant-device/api/v1alpha1"
)

var _ = Describe("LosantSync lifecycle", func() {

	BeforeEach(func() {
		ensureTestNamespaceAndSecret(ctx)
	})

	Describe("initial phase transition", func() {
		var name string

		BeforeEach(func() { name = uniqueName("lc-init") })
		AfterEach(func() { cleanupLosantSync(ctx, name) })

		It("moves to Provisioning on the first reconcile", func() {
			Expect(k8sClient.Create(ctx, newLosantSync(name, "5m"))).To(Succeed())

			Eventually(func() losantv1alpha1.LosantSyncPhase {
				return getLosantSync(ctx, name).Status.Phase
			}, pollTimeout, pollInterval).Should(Equal(losantv1alpha1.PhaseProvisioning))
		})

		It("sets NextScheduledTime on the first reconcile", func() {
			Expect(k8sClient.Create(ctx, newLosantSync(name, "5m"))).To(Succeed())

			Eventually(func() bool {
				return getLosantSync(ctx, name).Status.NextScheduledTime != nil
			}, pollTimeout, pollInterval).Should(BeTrue())
		})

		It("does not remain in the empty phase indefinitely", func() {
			Expect(k8sClient.Create(ctx, newLosantSync(name, "5m"))).To(Succeed())

			Consistently(func() losantv1alpha1.LosantSyncPhase {
				return getLosantSync(ctx, name).Status.Phase
			}, 3*time.Second, pollInterval).ShouldNot(BeEmpty(),
				"Phase should not stay blank — controller may not be running")
		})

		It("works identically with a cronSchedule instead of an interval", func() {
			ls := newLosantSync(name, "")
			ls.Spec.CronSchedule = "*/5 * * * *"
			Expect(k8sClient.Create(ctx, ls)).To(Succeed())

			Eventually(func() losantv1alpha1.LosantSyncPhase {
				return getLosantSync(ctx, name).Status.Phase
			}, pollTimeout, pollInterval).Should(Equal(losantv1alpha1.PhaseProvisioning))
		})
	})

	Describe("suspend behaviour", func() {
		var name string

		BeforeEach(func() { name = uniqueName("lc-suspend") })
		AfterEach(func() { cleanupLosantSync(ctx, name) })

		It("enters Suspended immediately when Suspend=true on creation", func() {
			ls := newLosantSync(name, "5m")
			ls.Spec.Suspend = true
			Expect(k8sClient.Create(ctx, ls)).To(Succeed())

			Eventually(func() losantv1alpha1.LosantSyncPhase {
				return getLosantSync(ctx, name).Status.Phase
			}, pollTimeout, pollInterval).Should(Equal(losantv1alpha1.PhaseSuspended))
		})

		It("transitions from Provisioning to Suspended when Suspend is enabled", func() {
			Expect(k8sClient.Create(ctx, newLosantSync(name, "5m"))).To(Succeed())

			Eventually(func() losantv1alpha1.LosantSyncPhase {
				return getLosantSync(ctx, name).Status.Phase
			}, pollTimeout, pollInterval).Should(Equal(losantv1alpha1.PhaseProvisioning))

			latest := getLosantSync(ctx, name)
			latest.Spec.Suspend = true
			Expect(k8sClient.Update(ctx, latest)).To(Succeed())

			Eventually(func() losantv1alpha1.LosantSyncPhase {
				return getLosantSync(ctx, name).Status.Phase
			}, pollTimeout, pollInterval).Should(Equal(losantv1alpha1.PhaseSuspended))
		})

		It("does not set NextScheduledTime when suspended from the start", func() {
			ls := newLosantSync(name, "5m")
			ls.Spec.Suspend = true
			Expect(k8sClient.Create(ctx, ls)).To(Succeed())

			Eventually(func() losantv1alpha1.LosantSyncPhase {
				return getLosantSync(ctx, name).Status.Phase
			}, pollTimeout, pollInterval).Should(Equal(losantv1alpha1.PhaseSuspended))

			// NextScheduledTime must never be written for a suspended resource.
			Consistently(func() bool {
				return getLosantSync(ctx, name).Status.NextScheduledTime == nil
			}, 5*time.Second, pollInterval).Should(BeTrue(),
				"NextScheduledTime should not be set while suspended")
		})

		It("phase remains Suspended when reconciled repeatedly", func() {
			ls := newLosantSync(name, "5m")
			ls.Spec.Suspend = true
			Expect(k8sClient.Create(ctx, ls)).To(Succeed())

			Eventually(func() losantv1alpha1.LosantSyncPhase {
				return getLosantSync(ctx, name).Status.Phase
			}, pollTimeout, pollInterval).Should(Equal(losantv1alpha1.PhaseSuspended))

			// Touch spec to force a re-reconcile, verify phase holds.
			latest := getLosantSync(ctx, name)
			latest.Spec.Region = "eu-west-1"
			Expect(k8sClient.Update(ctx, latest)).To(Succeed())

			Consistently(func() losantv1alpha1.LosantSyncPhase {
				return getLosantSync(ctx, name).Status.Phase
			}, 5*time.Second, pollInterval).Should(Equal(losantv1alpha1.PhaseSuspended))
		})
	})

	Describe("deletion", func() {
		It("can be deleted while in Provisioning phase", func() {
			name := uniqueName("lc-delete")
			Expect(k8sClient.Create(ctx, newLosantSync(name, "5m"))).To(Succeed())

			Eventually(func() losantv1alpha1.LosantSyncPhase {
				return getLosantSync(ctx, name).Status.Phase
			}, pollTimeout, pollInterval).Should(Equal(losantv1alpha1.PhaseProvisioning))

			cleanupLosantSync(ctx, name)
		})

		It("can be deleted while suspended", func() {
			name := uniqueName("lc-delete-suspended")
			ls := newLosantSync(name, "5m")
			ls.Spec.Suspend = true
			Expect(k8sClient.Create(ctx, ls)).To(Succeed())

			Eventually(func() losantv1alpha1.LosantSyncPhase {
				return getLosantSync(ctx, name).Status.Phase
			}, pollTimeout, pollInterval).Should(Equal(losantv1alpha1.PhaseSuspended))

			cleanupLosantSync(ctx, name)
		})
	})

	// These tests will be enabled once the developer implements provisioning
	// and GEA reporting (see TODO in internal/controller/losantsync_controller.go).
	PDescribe("post-provisioning phases [pending: developer TODO]", func() {
		It("transitions from Provisioning to Active after successful GEA report")
		It("transitions to Degraded when the GEA is unreachable")
		It("sets the GEAReachable condition to True when GEA responds")
		It("sets the DevicesProvisioned condition after all nodes are provisioned")
		It("populates NodeDevices map with a device ID per k8s node")
		It("updates LastSyncTime after each successful report")
	})
})
