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
	// Blocked on: #63 (LosantSyncReconciler full reconcile logic).
	PDescribe("post-provisioning phases [pending: #63]", func() {
		It("transitions from Provisioning to Active after successful GEA report")
		It("transitions to Degraded when the GEA is unreachable")
		It("sets the GEAReachable condition to True when GEA responds")
		It("sets the DevicesProvisioned condition after all nodes are provisioned")
		It("populates NodeDevices map with a device ID per k8s node")
		It("updates LastSyncTime after each successful report")

		// AC-C-01..08 — condition assertions
		It("GEAReachable=True after a successful metric report to GEA")
		It("GEAReachable=False with non-empty reason when GEA is unreachable")
		It("GEAReachable transitions back to True after GEA recovery")
		It("DevicesProvisioned=True after all nodes have Losant device IDs")
		It("LastSyncSucceeded=True after each successful GEA report")
		It("LastSyncSucceeded=False when GEA returns non-2xx response")

		// AC-S-01..03 — sync verification
		It("lastSyncTime is updated after every successful sync cycle")
		It("lastSyncTime is not updated when the GEA HTTP call fails")
		It("nodeDevices contains exactly one entry per ready k8s node")

		// AC-GEA-01..06 — GEA unreachability
		It("controller does not crash when GEA is unreachable — requeueing with backoff")
		It("phase transitions to Degraded when GEA is unreachable for a full sync cycle")
		It("GEAReachable=False reason describes the failure")
		It("lastSyncTime is not updated during a failed GEA call")
		It("phase returns to Active and GEAReachable=True after GEA recovers")
		It("retry interval uses exponential backoff capped at 5 minutes")

		// AC-REST-01..05 — REST API unreachability
		It("controller does not crash when Losant REST API is unreachable — requeueing with backoff")
		It("phase remains Provisioning when REST API is unreachable")
		It("DevicesProvisioned=False with descriptive reason when REST API is unavailable")
		It("provisioning resumes automatically within one reconcile cycle after REST recovers")
		It("partial provisioning state persists: previously provisioned nodes are not re-provisioned")
	})

	// AC-SUSP-03/04 — suspension HTTP and resume behaviour
	// Blocked on: #63 (LosantSyncReconciler full reconcile logic).
	PDescribe("suspension HTTP and resume [pending: #63]", func() {
		It("makes no HTTP calls to GEA or Losant REST API while suspended")
		It("resuming from suspension (suspend=false) restarts lifecycle from Provisioning")
	})

	// AC-NODE-ADD-01..04 — new node joins cluster
	// Blocked on: #63 (LosantSyncReconciler full reconcile logic).
	PDescribe("new node joins the cluster [pending: #63]", func() {
		It("attempts to provision a Losant device within one reconcile cycle of a new node appearing")
		It("status.nodeDevices contains an entry for the new node within 60s of it becoming Ready")
		It("DevicesProvisioned transitions False→True after the new node is provisioned")
		It("metrics for the new node appear in the next GEA report after provisioning")
	})

	// AC-NODE-RM-01..03 / AC-NODE-CORDON-01..02 — node removal and cordoning
	// Blocked on: #63 (LosantSyncReconciler full reconcile logic).
	PDescribe("node removal and cordoning [pending: #63]", func() {
		It("removing a node from the cluster does not cause an error or Degraded phase")
		It("removed node's entry may remain in nodeDevices and does not re-trigger provisioning")
		It("removed node does not appear in subsequent GEA metric reports")
		It("a cordoned node is still included in health metric reports")
		It("cordoned node health snapshot reflects actual condition (Ready=True even if unschedulable)")
	})
})
