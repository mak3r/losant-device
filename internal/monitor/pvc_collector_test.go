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

package monitor

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func makePVC(phase corev1.PersistentVolumeClaimPhase) corev1.PersistentVolumeClaim {
	return corev1.PersistentVolumeClaim{
		Status: corev1.PersistentVolumeClaimStatus{Phase: phase},
	}
}

func TestCountDegradedPVCs(t *testing.T) {
	pvcs := []corev1.PersistentVolumeClaim{
		makePVC(corev1.ClaimBound),
		makePVC(corev1.ClaimPending),
		makePVC(corev1.ClaimLost),
		makePVC(corev1.ClaimBound),
	}
	got := CountDegradedPVCs(pvcs)
	if got != 2 {
		t.Errorf("got %d, want 2 (1 Pending + 1 Lost)", got)
	}
}

func TestCountDegradedPVCs_AllBound(t *testing.T) {
	pvcs := []corev1.PersistentVolumeClaim{
		makePVC(corev1.ClaimBound),
		makePVC(corev1.ClaimBound),
	}
	if got := CountDegradedPVCs(pvcs); got != 0 {
		t.Errorf("all bound: got %d, want 0", got)
	}
}

func TestCountDegradedPVCs_Empty(t *testing.T) {
	if got := CountDegradedPVCs(nil); got != 0 {
		t.Errorf("nil slice: got %d, want 0", got)
	}
}
