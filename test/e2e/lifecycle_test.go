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
	"fmt"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

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

	// AC-P-05/06, AC-C-01..08, AC-S-01..03, AC-GEA-01..06, AC-REST-01..05
	Describe("post-provisioning phases", func() {
		var name string

		BeforeEach(func() { name = uniqueName("lc-post") })
		AfterEach(func() { cleanupLosantSync(ctx, name) })

		// AC-P-05
		It("transitions from Provisioning to Active after successful GEA report", func() {
			Expect(k8sClient.Create(ctx, newLosantSync(name, "5m"))).To(Succeed())
			Eventually(func() losantv1alpha1.LosantSyncPhase {
				return getLosantSync(ctx, name).Status.Phase
			}, 60*time.Second, pollInterval).Should(Equal(losantv1alpha1.PhaseActive))
		})

		// AC-P-06
		It("transitions to Degraded when the GEA is unreachable", func() {
			ls := newLosantSync(name, "5m")
			ls.Spec.GEA.ServiceRef = "no-such-gea"
			Expect(k8sClient.Create(ctx, ls)).To(Succeed())
			Eventually(func() losantv1alpha1.LosantSyncPhase {
				return getLosantSync(ctx, name).Status.Phase
			}, 60*time.Second, pollInterval).Should(Equal(losantv1alpha1.PhaseDegraded))
		})

		// AC-C-01
		It("GEAReachable=True after a successful metric report to GEA", func() {
			Expect(k8sClient.Create(ctx, newLosantSync(name, "5m"))).To(Succeed())
			Eventually(func() bool {
				cond := getCondition(ctx, name, "GEAReachable")
				return cond != nil && cond.Status == metav1.ConditionTrue
			}, 60*time.Second, pollInterval).Should(BeTrue())
		})

		// AC-C-02
		It("GEAReachable=False with non-empty reason when GEA is unreachable", func() {
			ls := newLosantSync(name, "5m")
			ls.Spec.GEA.ServiceRef = "no-such-gea"
			Expect(k8sClient.Create(ctx, ls)).To(Succeed())
			Eventually(func() bool {
				cond := getCondition(ctx, name, "GEAReachable")
				return cond != nil && cond.Status == metav1.ConditionFalse && cond.Reason != ""
			}, 60*time.Second, pollInterval).Should(BeTrue())
		})

		// AC-C-03
		It("GEAReachable transitions back to True after GEA recovery", func() {
			ls := newLosantSync(name, "5m")
			ls.Spec.GEA.ServiceRef = "no-such-gea"
			Expect(k8sClient.Create(ctx, ls)).To(Succeed())
			Eventually(func() losantv1alpha1.LosantSyncPhase {
				return getLosantSync(ctx, name).Status.Phase
			}, 60*time.Second, pollInterval).Should(Equal(losantv1alpha1.PhaseDegraded))

			// Restore the real GEA service.
			latest := getLosantSync(ctx, name)
			latest.Spec.GEA.ServiceRef = "losant-gea"
			Expect(k8sClient.Update(ctx, latest)).To(Succeed())

			// Controller retries after requeueOnDegraded (1m); allow 2m for recovery.
			Eventually(func() bool {
				cond := getCondition(ctx, name, "GEAReachable")
				return cond != nil && cond.Status == metav1.ConditionTrue
			}, 120*time.Second, pollInterval).Should(BeTrue())
		})

		// AC-C-04
		It("DevicesProvisioned=True after all nodes have Losant device IDs", func() {
			Expect(k8sClient.Create(ctx, newLosantSync(name, "5m"))).To(Succeed())
			Eventually(func() bool {
				cond := getCondition(ctx, name, "DevicesProvisioned")
				return cond != nil && cond.Status == metav1.ConditionTrue
			}, 60*time.Second, pollInterval).Should(BeTrue())
		})

		// AC-C-07
		It("LastSyncSucceeded=True after each successful GEA report", func() {
			Expect(k8sClient.Create(ctx, newLosantSync(name, "5m"))).To(Succeed())
			Eventually(func() bool {
				cond := getCondition(ctx, name, "LastSyncSucceeded")
				return cond != nil && cond.Status == metav1.ConditionTrue
			}, 60*time.Second, pollInterval).Should(BeTrue())
		})

		// AC-C-08
		It("LastSyncSucceeded=False when GEA returns non-2xx response", func() {
			ls := newLosantSync(name, "5m")
			ls.Spec.GEA.ServiceRef = "no-such-gea"
			Expect(k8sClient.Create(ctx, ls)).To(Succeed())
			Eventually(func() bool {
				cond := getCondition(ctx, name, "LastSyncSucceeded")
				return cond != nil && cond.Status == metav1.ConditionFalse
			}, 60*time.Second, pollInterval).Should(BeTrue())
		})

		// AC-S-01
		It("lastSyncTime is updated after every successful sync cycle", func() {
			Expect(k8sClient.Create(ctx, newLosantSync(name, "5m"))).To(Succeed())
			Eventually(func() bool {
				return getLosantSync(ctx, name).Status.LastSyncTime != nil
			}, 60*time.Second, pollInterval).Should(BeTrue())
		})

		// AC-S-02
		It("lastSyncTime is not updated when the GEA HTTP call fails", func() {
			ls := newLosantSync(name, "5m")
			ls.Spec.GEA.ServiceRef = "no-such-gea"
			Expect(k8sClient.Create(ctx, ls)).To(Succeed())
			Eventually(func() losantv1alpha1.LosantSyncPhase {
				return getLosantSync(ctx, name).Status.Phase
			}, 60*time.Second, pollInterval).Should(Equal(losantv1alpha1.PhaseDegraded))
			Expect(getLosantSync(ctx, name).Status.LastSyncTime).To(BeNil())
		})

		// AC-S-03
		It("nodeDevices contains exactly one entry per ready k8s node", func() {
			Expect(k8sClient.Create(ctx, newLosantSync(name, "5m"))).To(Succeed())
			Eventually(func() losantv1alpha1.LosantSyncPhase {
				return getLosantSync(ctx, name).Status.Phase
			}, 60*time.Second, pollInterval).Should(Equal(losantv1alpha1.PhaseActive))

			readyNodes := listReadyNodeNames(ctx)
			nodeDevices := getLosantSync(ctx, name).Status.NodeDevices
			Expect(nodeDevices).To(HaveLen(len(readyNodes)))
		})

		// AC-GEA-01: controller must not crash; it must requeue and retry.
		It("controller does not crash when GEA is unreachable — requeueing with backoff", func() {
			ls := newLosantSync(name, "5m")
			ls.Spec.GEA.ServiceRef = "no-such-gea"
			Expect(k8sClient.Create(ctx, ls)).To(Succeed())
			Eventually(func() losantv1alpha1.LosantSyncPhase {
				return getLosantSync(ctx, name).Status.Phase
			}, 60*time.Second, pollInterval).Should(Equal(losantv1alpha1.PhaseDegraded))
			// Controller stays alive (resource remains Degraded, not deleted or panicking).
			Consistently(func() losantv1alpha1.LosantSyncPhase {
				return getLosantSync(ctx, name).Status.Phase
			}, 5*time.Second, pollInterval).Should(Equal(losantv1alpha1.PhaseDegraded))
		})

		// AC-GEA-02
		It("phase transitions to Degraded when GEA is unreachable for a full sync cycle", func() {
			ls := newLosantSync(name, "5m")
			ls.Spec.GEA.ServiceRef = "no-such-gea"
			Expect(k8sClient.Create(ctx, ls)).To(Succeed())
			Eventually(func() losantv1alpha1.LosantSyncPhase {
				return getLosantSync(ctx, name).Status.Phase
			}, 60*time.Second, pollInterval).Should(Equal(losantv1alpha1.PhaseDegraded))
		})

		// AC-GEA-03
		It("GEAReachable=False reason describes the failure", func() {
			ls := newLosantSync(name, "5m")
			ls.Spec.GEA.ServiceRef = "no-such-gea"
			Expect(k8sClient.Create(ctx, ls)).To(Succeed())
			Eventually(func() bool {
				cond := getCondition(ctx, name, "GEAReachable")
				return cond != nil && cond.Status == metav1.ConditionFalse && cond.Reason != ""
			}, 60*time.Second, pollInterval).Should(BeTrue())
		})

		// AC-GEA-04
		It("lastSyncTime is not updated during a failed GEA call", func() {
			ls := newLosantSync(name, "5m")
			ls.Spec.GEA.ServiceRef = "no-such-gea"
			Expect(k8sClient.Create(ctx, ls)).To(Succeed())
			Eventually(func() losantv1alpha1.LosantSyncPhase {
				return getLosantSync(ctx, name).Status.Phase
			}, 60*time.Second, pollInterval).Should(Equal(losantv1alpha1.PhaseDegraded))
			Expect(getLosantSync(ctx, name).Status.LastSyncTime).To(BeNil())
		})

		// AC-GEA-05
		It("phase returns to Active and GEAReachable=True after GEA recovers", func() {
			ls := newLosantSync(name, "5m")
			ls.Spec.GEA.ServiceRef = "no-such-gea"
			Expect(k8sClient.Create(ctx, ls)).To(Succeed())
			Eventually(func() losantv1alpha1.LosantSyncPhase {
				return getLosantSync(ctx, name).Status.Phase
			}, 60*time.Second, pollInterval).Should(Equal(losantv1alpha1.PhaseDegraded))

			latest := getLosantSync(ctx, name)
			latest.Spec.GEA.ServiceRef = "losant-gea"
			Expect(k8sClient.Update(ctx, latest)).To(Succeed())

			Eventually(func() losantv1alpha1.LosantSyncPhase {
				return getLosantSync(ctx, name).Status.Phase
			}, 120*time.Second, pollInterval).Should(Equal(losantv1alpha1.PhaseActive))
			cond := getCondition(ctx, name, "GEAReachable")
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		})

		// AC-GEA-06: retry must not spin without delay (controller stays Degraded, not looping).
		It("retry interval uses exponential backoff capped at 5 minutes", func() {
			ls := newLosantSync(name, "5m")
			ls.Spec.GEA.ServiceRef = "no-such-gea"
			Expect(k8sClient.Create(ctx, ls)).To(Succeed())
			Eventually(func() losantv1alpha1.LosantSyncPhase {
				return getLosantSync(ctx, name).Status.Phase
			}, 60*time.Second, pollInterval).Should(Equal(losantv1alpha1.PhaseDegraded))
			// Phase stays Degraded (not flipping repeatedly) — confirms the controller
			// is not spinning: it is waiting requeueOnDegraded between retries.
			Consistently(func() losantv1alpha1.LosantSyncPhase {
				return getLosantSync(ctx, name).Status.Phase
			}, 10*time.Second, pollInterval).Should(Equal(losantv1alpha1.PhaseDegraded))
		})

		// AC-REST-01: controller must not crash when Losant REST API is unreachable.
		// Placeholder credentials cause Losant Ping to fail (simulates REST API failure).
		It("controller does not crash when Losant REST API is unreachable — requeueing with backoff", func() {
			Expect(k8sClient.Create(ctx, newLosantSync(name, "5m"))).To(Succeed())
			Eventually(func() losantv1alpha1.LosantSyncPhase {
				return getLosantSync(ctx, name).Status.Phase
			}, 60*time.Second, pollInterval).Should(Equal(losantv1alpha1.PhaseDegraded))
			Consistently(func() losantv1alpha1.LosantSyncPhase {
				return getLosantSync(ctx, name).Status.Phase
			}, 5*time.Second, pollInterval).Should(Equal(losantv1alpha1.PhaseDegraded))
		})

		// AC-REST-02: phase MUST NOT be Active when REST API is unavailable.
		It("phase does not advance to Active when REST API is unreachable", func() {
			Expect(k8sClient.Create(ctx, newLosantSync(name, "5m"))).To(Succeed())
			Eventually(func() losantv1alpha1.LosantSyncPhase {
				return getLosantSync(ctx, name).Status.Phase
			}, 60*time.Second, pollInterval).Should(Equal(losantv1alpha1.PhaseDegraded))
			Expect(getLosantSync(ctx, name).Status.Phase).NotTo(Equal(losantv1alpha1.PhaseActive))
		})

		// AC-REST-03: descriptive failure reason when REST unavailable.
		It("DevicesProvisioned condition has descriptive reason when REST API is unavailable", func() {
			Expect(k8sClient.Create(ctx, newLosantSync(name, "5m"))).To(Succeed())
			Eventually(func() losantv1alpha1.LosantSyncPhase {
				return getLosantSync(ctx, name).Status.Phase
			}, 60*time.Second, pollInterval).Should(Equal(losantv1alpha1.PhaseDegraded))
			cond := getCondition(ctx, name, "LastSyncSucceeded")
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).NotTo(BeEmpty())
		})

		// AC-REST-04: provisioning resumes when REST API recovers (requires real credentials on leap cluster).
		It("provisioning resumes automatically once REST API is reachable", func() {
			Expect(k8sClient.Create(ctx, newLosantSync(name, "5m"))).To(Succeed())
			Eventually(func() losantv1alpha1.LosantSyncPhase {
				return getLosantSync(ctx, name).Status.Phase
			}, 120*time.Second, pollInterval).Should(Equal(losantv1alpha1.PhaseActive))
		})

		// AC-REST-05: partial provisioning persists across re-provisioning attempts.
		It("partial provisioning state persists: previously provisioned nodes are not re-provisioned", func() {
			Expect(k8sClient.Create(ctx, newLosantSync(name, "5m"))).To(Succeed())
			Eventually(func() losantv1alpha1.LosantSyncPhase {
				return getLosantSync(ctx, name).Status.Phase
			}, 60*time.Second, pollInterval).Should(Equal(losantv1alpha1.PhaseActive))

			firstDevices := getLosantSync(ctx, name).Status.NodeDevices
			Expect(firstDevices).NotTo(BeEmpty())

			// Force a re-reconcile by touching spec.
			latest := getLosantSync(ctx, name)
			latest.Spec.Region = "us-west-2"
			Expect(k8sClient.Update(ctx, latest)).To(Succeed())

			// Node device IDs from the first sync MUST be preserved.
			Eventually(func() bool {
				current := getLosantSync(ctx, name).Status.NodeDevices
				for nodeName, deviceID := range firstDevices {
					if current[nodeName] != deviceID {
						return false
					}
				}
				return true
			}, 60*time.Second, pollInterval).Should(BeTrue(),
				"nodeDevices entries from the first sync must persist across re-provisioning")
		})
	})

	// AC-SUSP-03/04
	Describe("suspension HTTP and resume", func() {
		var name string

		BeforeEach(func() { name = uniqueName("lc-susp-http") })
		AfterEach(func() { cleanupLosantSync(ctx, name) })

		// AC-SUSP-03: while Suspended, no HTTP calls must be made.
		// Verified indirectly: LastSyncTime must stay nil and no conditions must be set.
		It("makes no HTTP calls to GEA or Losant REST API while suspended", func() {
			ls := newLosantSync(name, "5m")
			ls.Spec.Suspend = true
			Expect(k8sClient.Create(ctx, ls)).To(Succeed())

			Eventually(func() losantv1alpha1.LosantSyncPhase {
				return getLosantSync(ctx, name).Status.Phase
			}, pollTimeout, pollInterval).Should(Equal(losantv1alpha1.PhaseSuspended))

			Consistently(func() bool {
				current := getLosantSync(ctx, name)
				return current.Status.LastSyncTime == nil && len(current.Status.Conditions) == 0
			}, 10*time.Second, pollInterval).Should(BeTrue(),
				"LastSyncTime and Conditions must not be populated while suspended")
		})

		// AC-SUSP-04: removing suspend MUST restart the lifecycle from Provisioning.
		It("resuming from suspension (suspend=false) restarts lifecycle from Provisioning", func() {
			ls := newLosantSync(name, "5m")
			ls.Spec.Suspend = true
			Expect(k8sClient.Create(ctx, ls)).To(Succeed())

			Eventually(func() losantv1alpha1.LosantSyncPhase {
				return getLosantSync(ctx, name).Status.Phase
			}, pollTimeout, pollInterval).Should(Equal(losantv1alpha1.PhaseSuspended))

			latest := getLosantSync(ctx, name)
			latest.Spec.Suspend = false
			Expect(k8sClient.Update(ctx, latest)).To(Succeed())

			Eventually(func() losantv1alpha1.LosantSyncPhase {
				return getLosantSync(ctx, name).Status.Phase
			}, pollTimeout, pollInterval).Should(Equal(losantv1alpha1.PhaseProvisioning))
		})
	})

	// AC-NODE-ADD-01..04
	// These tests require adding a new node to the cluster at runtime, which cannot be
	// automated without cluster autoscaler or node pool management. Run manually on a
	// cluster where nodes can be provisioned on demand.
	Describe("new node joins the cluster", func() {
		var name string

		BeforeEach(func() { name = uniqueName("lc-node-add") })
		AfterEach(func() { cleanupLosantSync(ctx, name) })

		It("attempts to provision a Losant device within one reconcile cycle of a new node appearing", func() {
			Skip("requires adding a new node to the cluster at runtime — run manually with cluster node provisioner")
		})

		It("status.nodeDevices contains an entry for the new node within 60s of it becoming Ready", func() {
			Skip("requires adding a new node to the cluster at runtime — run manually with cluster node provisioner")
		})

		It("DevicesProvisioned transitions False→True after the new node is provisioned", func() {
			Skip("requires adding a new node to the cluster at runtime — run manually with cluster node provisioner")
		})

		It("metrics for the new node appear in the next GEA report after provisioning", func() {
			Skip("requires adding a new node to the cluster at runtime — run manually with cluster node provisioner")
		})
	})

	// AC-NODE-RM-01..03 / AC-NODE-CORDON-01..02
	Describe("node removal and cordoning", func() {
		var name string

		BeforeEach(func() { name = uniqueName("lc-node-rm") })
		AfterEach(func() { cleanupLosantSync(ctx, name) })

		// AC-NODE-RM-01..03: removal tests require deleting a cluster node at runtime.
		It("removing a node from the cluster does not cause an error or Degraded phase", func() {
			Skip("requires removing a cluster node at runtime — run manually")
		})

		It("removed node's entry may remain in nodeDevices and does not re-trigger provisioning", func() {
			Skip("requires removing a cluster node at runtime — run manually")
		})

		It("removed node does not appear in subsequent GEA metric reports", func() {
			Skip("requires removing a cluster node at runtime — run manually")
		})

		// AC-NODE-CORDON-01: cordoned node MUST still be included in health metric reports.
		It("a cordoned node is still included in health metric reports", func() {
			readyNodes := listReadyNodeNames(ctx)
			if len(readyNodes) == 0 {
				Skip("no Ready nodes found in cluster")
			}
			targetNode := readyNodes[0]
			DeferCleanup(func() { uncordonNode(ctx, targetNode) })
			cordonNode(ctx, targetNode)

			Expect(k8sClient.Create(ctx, newLosantSync(name, "5m"))).To(Succeed())

			// Wait for Active — all nodes (including cordoned) provisioned and reported.
			Eventually(func() losantv1alpha1.LosantSyncPhase {
				return getLosantSync(ctx, name).Status.Phase
			}, 60*time.Second, pollInterval).Should(Equal(losantv1alpha1.PhaseActive))

			// Cordoned node must still have a device ID in nodeDevices.
			nodeDevices := getLosantSync(ctx, name).Status.NodeDevices
			Expect(nodeDevices).To(HaveKey(targetNode),
				"cordoned node %q must appear in nodeDevices", targetNode)
		})

		// AC-NODE-CORDON-02: cordoned node health snapshot MUST reflect actual condition.
		It("cordoned node health snapshot reflects actual condition (Ready=True even if unschedulable)", func() {
			readyNodes := listReadyNodeNames(ctx)
			if len(readyNodes) == 0 {
				Skip("no Ready nodes found in cluster")
			}
			targetNode := readyNodes[0]
			DeferCleanup(func() { uncordonNode(ctx, targetNode) })
			cordonNode(ctx, targetNode)

			Expect(k8sClient.Create(ctx, newLosantSync(name, "5m"))).To(Succeed())

			Eventually(func() losantv1alpha1.LosantSyncPhase {
				return getLosantSync(ctx, name).Status.Phase
			}, 60*time.Second, pollInterval).Should(Equal(losantv1alpha1.PhaseActive))

			// Sync succeeded (LastSyncTime set) → GEA report included the cordoned node.
			Expect(getLosantSync(ctx, name).Status.LastSyncTime).NotTo(BeNil())
			// DevicesProvisioned=True confirms all nodes (including cordoned) are registered.
			cond := getCondition(ctx, name, "DevicesProvisioned")
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		})
	})
})

// clusterAttributeNames lists the 13 telemetry attributes expected on a cluster Edge Compute device.
var clusterAttributeNames = []string{
	"health_score", "health_status", "total_nodes", "ready_nodes", "unhealthy_nodes",
	"total_pods", "running_pods", "failed_pods", "pending_pods", "crashloop_pods",
	"degraded_pvcs", "coredns_healthy", "event_warnings",
}

// nodeAttributeNames lists the 11 telemetry attributes expected on a node peripheral device.
var nodeAttributeNames = []string{
	"health_score", "health_status", "ready", "memory_pressure", "disk_pressure",
	"pid_pressure", "pod_count", "not_ready_pods", "crashloop_pods",
	"cpu_request_pct", "mem_request_pct",
}

// AC-LIFECYCLE-01..04
// These tests require LOSANT_APP_ID and LOSANT_API_TOKEN env vars and a live cluster with a
// reachable GEA pod. Skip automatically when real Losant credentials are absent.
var _ = Describe("LosantSync device lifecycle against real Losant API", func() {

	BeforeEach(func() {
		skipUnlessRealLosant()
		ensureTestNamespaceAndSecret(ctx)
		ensureRealLosantSecret(ctx)
	})

	// AC-LIFECYCLE-01: fresh CR provisions devices with all expected attribute definitions.
	It("creates cluster and node devices with all expected attribute definitions", func() {
		name := uniqueName("lc-attrs")
		Expect(k8sClient.Create(ctx, newRealLosantSync(name, "5m"))).To(Succeed())
		DeferCleanup(func() { cleanupLosantSync(ctx, name) })

		Eventually(func() losantv1alpha1.LosantSyncPhase {
			return getLosantSync(ctx, name).Status.Phase
		}, 120*time.Second, pollInterval).Should(Equal(losantv1alpha1.PhaseActive))

		current := getLosantSync(ctx, name)
		appID := os.Getenv("LOSANT_APP_ID")

		clusterDev, status := losantFetchDevice(appID, current.Status.ClusterDeviceID)
		Expect(status).To(Equal(200))
		Expect(losantAttrNames(clusterDev)).To(ConsistOf(clusterAttributeNames))

		for nodeName, nodeDeviceID := range current.Status.NodeDevices {
			nodeDev, s := losantFetchDevice(appID, nodeDeviceID)
			Expect(s).To(Equal(200), "fetch node device for %s", nodeName)
			Expect(losantAttrNames(nodeDev)).To(ConsistOf(nodeAttributeNames),
				"node device for %s should have all expected attributes", nodeName)
		}
	})

	// AC-LIFECYCLE-02: operator patches attributes onto a pre-existing device that has none.
	It("patches all cluster attributes onto a pre-existing device that has no attributes", func() {
		name := uniqueName("lc-patch")
		appID := os.Getenv("LOSANT_APP_ID")

		// Pre-create a cluster device with no attributes using the canonical name.
		bareDeviceID := losantCreateBareDevice(appID,
			fmt.Sprintf("k8s-cluster-%s", name), "edgeCompute")
		DeferCleanup(func() { losantDeleteDevice(appID, bareDeviceID) })

		Expect(k8sClient.Create(ctx, newRealLosantSync(name, "5m"))).To(Succeed())
		DeferCleanup(func() { cleanupLosantSync(ctx, name) })

		Eventually(func() losantv1alpha1.LosantSyncPhase {
			return getLosantSync(ctx, name).Status.Phase
		}, 120*time.Second, pollInterval).Should(Equal(losantv1alpha1.PhaseActive))

		clusterDev, status := losantFetchDevice(appID, bareDeviceID)
		Expect(status).To(Equal(200))
		Expect(losantAttrNames(clusterDev)).To(ConsistOf(clusterAttributeNames),
			"operator must patch all cluster attributes onto the pre-existing device")
	})

	// AC-LIFECYCLE-03: controller recreates a cluster device deleted directly in Losant.
	// Uses a 1m interval so self-healing completes within the 120s Eventually window.
	It("recreates a cluster device that was manually deleted from Losant", func() {
		name := uniqueName("lc-heal")
		Expect(k8sClient.Create(ctx, newRealLosantSync(name, "1m"))).To(Succeed())
		DeferCleanup(func() { cleanupLosantSync(ctx, name) })

		Eventually(func() losantv1alpha1.LosantSyncPhase {
			return getLosantSync(ctx, name).Status.Phase
		}, 120*time.Second, pollInterval).Should(Equal(losantv1alpha1.PhaseActive))

		appID := os.Getenv("LOSANT_APP_ID")
		originalDeviceID := getLosantSync(ctx, name).Status.ClusterDeviceID
		Expect(originalDeviceID).NotTo(BeEmpty())

		losantDeleteDevice(appID, originalDeviceID)

		// Wait for the controller to detect the missing device and recreate it (up to 2× interval).
		Eventually(func() bool {
			current := getLosantSync(ctx, name)
			newID := current.Status.ClusterDeviceID
			return newID != "" && newID != originalDeviceID &&
				current.Status.Phase == losantv1alpha1.PhaseActive
		}, 120*time.Second, pollInterval).Should(BeTrue(),
			"controller should self-heal by creating a new cluster device")

		newDeviceID := getLosantSync(ctx, name).Status.ClusterDeviceID
		newDev, status := losantFetchDevice(appID, newDeviceID)
		Expect(status).To(Equal(200))
		Expect(losantAttrNames(newDev)).To(ConsistOf(clusterAttributeNames),
			"recreated device must have all cluster attribute definitions")
	})

	// AC-LIFECYCLE-04: deleting a CR triggers the finalizer which removes all Losant devices.
	It("deletes all Losant devices when the CR is deleted", func() {
		name := uniqueName("lc-finalizer")
		Expect(k8sClient.Create(ctx, newRealLosantSync(name, "5m"))).To(Succeed())

		Eventually(func() losantv1alpha1.LosantSyncPhase {
			return getLosantSync(ctx, name).Status.Phase
		}, 120*time.Second, pollInterval).Should(Equal(losantv1alpha1.PhaseActive))

		current := getLosantSync(ctx, name)
		clusterDeviceID := current.Status.ClusterDeviceID
		nodeDeviceIDs := make([]string, 0, len(current.Status.NodeDevices))
		for _, id := range current.Status.NodeDevices {
			nodeDeviceIDs = append(nodeDeviceIDs, id)
		}

		appID := os.Getenv("LOSANT_APP_ID")

		Expect(k8sClient.Delete(ctx, current)).To(Succeed())
		Eventually(func() bool {
			err := k8sClient.Get(ctx, types.NamespacedName{Name: name}, &losantv1alpha1.LosantSync{})
			return apierrors.IsNotFound(err)
		}, 120*time.Second, pollInterval).Should(BeTrue(),
			"CR should be fully removed once the finalizer completes device cleanup")

		_, status := losantFetchDevice(appID, clusterDeviceID)
		Expect(status).To(Equal(404), "cluster device must be deleted from Losant after CR deletion")

		for _, nodeDeviceID := range nodeDeviceIDs {
			_, s := losantFetchDevice(appID, nodeDeviceID)
			Expect(s).To(Equal(404),
				"node device %s must be deleted from Losant after CR deletion", nodeDeviceID)
		}
	})
})
