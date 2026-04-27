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

	appsv1 "k8s.io/api/apps/v1"
)

func TestIsCoreDNSHealthy_Nil(t *testing.T) {
	if IsCoreDNSHealthy(nil) {
		t.Error("nil deployment: expected false")
	}
}

func TestIsCoreDNSHealthy_ZeroReplicas(t *testing.T) {
	d := &appsv1.Deployment{}
	d.Status.AvailableReplicas = 0
	if IsCoreDNSHealthy(d) {
		t.Error("zero replicas: expected false")
	}
}

func TestIsCoreDNSHealthy_OneReplica(t *testing.T) {
	d := &appsv1.Deployment{}
	d.Status.AvailableReplicas = 1
	if !IsCoreDNSHealthy(d) {
		t.Error("one replica: expected true")
	}
}

func TestIsCoreDNSHealthy_MultipleReplicas(t *testing.T) {
	d := &appsv1.Deployment{}
	d.Status.AvailableReplicas = 3
	if !IsCoreDNSHealthy(d) {
		t.Error("three replicas: expected true")
	}
}
