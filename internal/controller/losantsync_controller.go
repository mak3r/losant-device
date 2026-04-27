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

package controller

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	losantv1alpha1 "github.com/mak3r/losant-device/api/v1alpha1"
)

// LosantSyncReconciler reconciles a LosantSync object.
type LosantSyncReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=losant.io,resources=losantsyncs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=losant.io,resources=losantsyncs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=losant.io,resources=losantsyncs/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get
// +kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=events,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch

func (r *LosantSyncReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var ls losantv1alpha1.LosantSync
	if err := r.Get(ctx, req.NamespacedName, &ls); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Suspend: halt all reconciliation.
	if ls.Spec.Suspend {
		logger.Info("reconciliation suspended", "name", ls.Name)
		if ls.Status.Phase != losantv1alpha1.PhaseSuspended {
			ls.Status.Phase = losantv1alpha1.PhaseSuspended
			if err := r.Status().Update(ctx, &ls); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// First reconcile: set phase to Provisioning.
	if ls.Status.Phase == "" {
		logger.Info("first reconcile, setting phase to Provisioning", "name", ls.Name)
		ls.Status.Phase = losantv1alpha1.PhaseProvisioning
		ls.Status.NextScheduledTime = &metav1.Time{Time: time.Now()}
		if err := r.Status().Update(ctx, &ls); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	// Schedule check: if next sync is still in the future, wait.
	if ls.Status.NextScheduledTime != nil && time.Now().Before(ls.Status.NextScheduledTime.Time) {
		wait := time.Until(ls.Status.NextScheduledTime.Time)
		logger.V(1).Info("next sync not yet due", "name", ls.Name, "in", wait.Round(time.Second))
		return ctrl.Result{RequeueAfter: wait}, nil
	}

	// TODO(developer): provisioning and metric reporting will be added in subsequent tasks.
	logger.Info("sync due — provisioning and reporting not yet implemented", "name", ls.Name)

	return ctrl.Result{}, nil
}

// SetupWithManager registers the controller with the manager.
func (r *LosantSyncReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&losantv1alpha1.LosantSync{}).
		Complete(r)
}
