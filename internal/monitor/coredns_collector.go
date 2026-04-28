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
	appsv1 "k8s.io/api/apps/v1"
)

// IsCoreDNSHealthy returns true if the CoreDNS deployment has at least one
// available replica. Returns false if the deployment pointer is nil.
func IsCoreDNSHealthy(deploy *appsv1.Deployment) bool {
	if deploy == nil {
		return false
	}
	return deploy.Status.AvailableReplicas > 0
}
