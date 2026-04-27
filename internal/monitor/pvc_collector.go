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
	corev1 "k8s.io/api/core/v1"
)

// CountDegradedPVCs returns the number of PVCs that are not in the Bound phase.
func CountDegradedPVCs(pvcs []corev1.PersistentVolumeClaim) int {
	count := 0
	for _, pvc := range pvcs {
		if pvc.Status.Phase != corev1.ClaimBound {
			count++
		}
	}
	return count
}
