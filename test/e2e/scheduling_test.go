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

var _ = Describe("LosantSync scheduling", func() {

	BeforeEach(func() {
		ensureTestNamespaceAndSecret(ctx)
	})

	Describe("NextScheduledTime initial value", func() {
		var name string

		BeforeEach(func() { name = uniqueName("sched-nst") })
		AfterEach(func() { cleanupLosantSync(ctx, name) })

		It("is set close to the time of creation", func() {
			before := time.Now()
			Expect(k8sClient.Create(ctx, newLosantSync(name, "5m"))).To(Succeed())

			Eventually(func() bool {
				return getLosantSync(ctx, name).Status.NextScheduledTime != nil
			}, pollTimeout, pollInterval).Should(BeTrue())

			nst := getLosantSync(ctx, name).Status.NextScheduledTime.Time
			// The controller sets NextScheduledTime = time.Now() on first reconcile,
			// so it should fall within the window [before-5s, before+pollTimeout].
			Expect(nst).To(BeTemporally(">=", before.Add(-5*time.Second)),
				"NextScheduledTime should not be earlier than resource creation")
			Expect(nst).To(BeTemporally("<=", before.Add(pollTimeout)),
				"NextScheduledTime should be set promptly after creation")
		})
	})

	Describe("schedule hold-off", func() {
		var name string

		BeforeEach(func() { name = uniqueName("sched-hold") })
		AfterEach(func() { cleanupLosantSync(ctx, name) })

		It("does not advance NextScheduledTime before the interval elapses", func() {
			// Use a long interval so the window never expires during this test.
			Expect(k8sClient.Create(ctx, newLosantSync(name, "10m"))).To(Succeed())

			Eventually(func() bool {
				return getLosantSync(ctx, name).Status.NextScheduledTime != nil
			}, pollTimeout, pollInterval).Should(BeTrue())

			first := getLosantSync(ctx, name).Status.NextScheduledTime.DeepCopy()

			// Over 10 seconds the 10m interval won't tick, so NextScheduledTime must stay fixed.
			Consistently(func() bool {
				nst := getLosantSync(ctx, name).Status.NextScheduledTime
				return nst != nil && nst.Equal(first)
			}, 10*time.Second, pollInterval).Should(BeTrue(),
				"NextScheduledTime advanced before the interval elapsed")
		})
	})

	Describe("suspension freezes scheduling", func() {
		var name string

		BeforeEach(func() { name = uniqueName("sched-freeze") })
		AfterEach(func() { cleanupLosantSync(ctx, name) })

		It("stops advancing NextScheduledTime after Suspend is set", func() {
			Expect(k8sClient.Create(ctx, newLosantSync(name, "5m"))).To(Succeed())

			// Wait for the schedule to arm.
			Eventually(func() losantv1alpha1.LosantSyncPhase {
				return getLosantSync(ctx, name).Status.Phase
			}, pollTimeout, pollInterval).Should(Equal(losantv1alpha1.PhaseProvisioning))

			// Suspend.
			latest := getLosantSync(ctx, name)
			latest.Spec.Suspend = true
			Expect(k8sClient.Update(ctx, latest)).To(Succeed())

			Eventually(func() losantv1alpha1.LosantSyncPhase {
				return getLosantSync(ctx, name).Status.Phase
			}, pollTimeout, pollInterval).Should(Equal(losantv1alpha1.PhaseSuspended))

			// Snapshot NextScheduledTime post-suspension.
			nstAtSuspend := getLosantSync(ctx, name).Status.NextScheduledTime

			// It must not change while suspended.
			Consistently(func() bool {
				current := getLosantSync(ctx, name).Status.NextScheduledTime
				if nstAtSuspend == nil && current == nil {
					return true
				}
				if nstAtSuspend == nil || current == nil {
					return false
				}
				return current.Equal(nstAtSuspend)
			}, 8*time.Second, pollInterval).Should(BeTrue(),
				"NextScheduledTime changed while resource was suspended")
		})
	})

	// These scheduling tests require the developer to implement the NextScheduledTime
	// advance logic after each successful sync cycle. Blocked on: #63.
	PDescribe("post-sync schedule advance [pending: #63]", func() {
		It("advances NextScheduledTime by the configured interval after each sync")
		It("advances NextScheduledTime to the next cron tick after each sync")
		It("persists NextScheduledTime across controller restarts so no cycle is missed")
		It("does not double-fire when the controller restarts mid-interval")
	})
})
