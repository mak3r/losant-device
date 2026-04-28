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
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func makeEvent(evType string, lastTimestamp time.Time) corev1.Event {
	return corev1.Event{
		Type:          evType,
		LastTimestamp: metav1.Time{Time: lastTimestamp},
	}
}

func makeEventWithEventTime(evType string, eventTime time.Time) corev1.Event {
	return corev1.Event{
		Type:      evType,
		EventTime: metav1.MicroTime{Time: eventTime},
	}
}

func TestCountRecentWarnings_Basic(t *testing.T) {
	now := time.Now()
	events := []corev1.Event{
		makeEvent(corev1.EventTypeWarning, now.Add(-30*time.Minute)), // recent warning
		makeEvent(corev1.EventTypeWarning, now.Add(-2*time.Hour)),    // old warning
		makeEvent(corev1.EventTypeNormal, now.Add(-5*time.Minute)),   // normal (ignored)
		makeEvent(corev1.EventTypeWarning, now.Add(-59*time.Minute)), // just inside window
	}
	got := CountRecentWarnings(events)
	if got != 2 {
		t.Errorf("got %d, want 2", got)
	}
}

func TestCountRecentWarnings_EventTimeFallback(t *testing.T) {
	now := time.Now()
	// LastTimestamp is zero — should fall back to EventTime
	ev := makeEventWithEventTime(corev1.EventTypeWarning, now.Add(-10*time.Minute))
	got := CountRecentWarnings([]corev1.Event{ev})
	if got != 1 {
		t.Errorf("EventTime fallback: got %d, want 1", got)
	}
}

func TestCountRecentWarnings_Empty(t *testing.T) {
	if got := CountRecentWarnings(nil); got != 0 {
		t.Errorf("nil slice: got %d, want 0", got)
	}
}

func TestCountRecentWarnings_AllOld(t *testing.T) {
	now := time.Now()
	events := []corev1.Event{
		makeEvent(corev1.EventTypeWarning, now.Add(-2*time.Hour)),
		makeEvent(corev1.EventTypeWarning, now.Add(-25*time.Hour)),
	}
	if got := CountRecentWarnings(events); got != 0 {
		t.Errorf("all old: got %d, want 0", got)
	}
}
