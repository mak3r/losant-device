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
	"time"

	corev1 "k8s.io/api/core/v1"
)

const warningWindow = time.Hour

// CountRecentWarnings counts Warning-type events that occurred within the last hour.
func CountRecentWarnings(events []corev1.Event) int {
	cutoff := time.Now().Add(-warningWindow)
	count := 0
	for _, ev := range events {
		if ev.Type != corev1.EventTypeWarning {
			continue
		}
		// Use LastTimestamp if set, otherwise EventTime.
		ts := ev.LastTimestamp.Time
		if ts.IsZero() {
			ts = ev.EventTime.Time
		}
		if ts.After(cutoff) {
			count++
		}
	}
	return count
}
