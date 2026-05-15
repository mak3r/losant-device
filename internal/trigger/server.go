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

// Package trigger implements the in-cluster HTTP server that translates GEA
// edge workflow calls into RancherSession CR create/delete operations.
//
// Security note: the server has no authentication in v1. It relies on the
// ClusterIP Service being the only network path to reach it. A future phase
// will add bearer token auth.
package trigger

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	losantv1alpha1 "github.com/mak3r/losant-device/api/v1alpha1"
)

// Server is a controller-runtime Runnable that listens for GEA trigger calls
// and translates them into RancherSession CR create/delete operations.
type Server struct {
	Client    client.Client
	Addr      string
	Namespace string
}

type rancherRequest struct {
	Action     string `json:"action"`
	TTLSeconds int64  `json:"ttlSeconds"`
}

// Start implements manager.Runnable. It starts the HTTP listener and blocks
// until ctx is cancelled.
func (s *Server) Start(ctx context.Context) error {
	logger := log.FromContext(ctx).WithName("trigger-server")

	mux := http.NewServeMux()
	mux.HandleFunc("/rancher", s.handleRancher)

	srv := &http.Server{
		Addr:    s.Addr,
		Handler: mux,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("trigger server listening", "addr", s.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		if err := srv.Shutdown(context.Background()); err != nil { //nolint:contextcheck
			logger.Error(err, "trigger server shutdown error")
		}
		return nil
	case err := <-errCh:
		return fmt.Errorf("trigger server: %w", err)
	}
}

// handleRancher processes POST /rancher requests from GEA edge workflows.
func (s *Server) handleRancher(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req rancherRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	logger := log.FromContext(ctx).WithName("trigger-server")

	lsName, err := s.losantSyncName(ctx)
	if err != nil {
		logger.Error(err, "failed to locate LosantSync CR")
		http.Error(w, "internal error: could not determine session name", http.StatusInternalServerError)
		return
	}

	switch req.Action {
	case "connect":
		s.handleConnect(w, r, ctx, lsName, req.TTLSeconds)
	case "disconnect":
		s.handleDisconnect(w, r, ctx, lsName)
	default:
		http.Error(w, fmt.Sprintf("unknown action %q; expected connect or disconnect", req.Action), http.StatusBadRequest)
	}

	logger.Info("processed trigger request", "action", req.Action, "session", lsName)
}

// handleConnect creates a RancherSession CR named after the LosantSync CR.
// Returns 202 on success, 409 if the CR already exists.
func (s *Server) handleConnect(w http.ResponseWriter, _ *http.Request, ctx context.Context, name string, ttlSeconds int64) {
	ttl := ttlSeconds
	if ttl <= 0 {
		ttl = 3600
	}

	rs := &losantv1alpha1.RancherSession{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: s.Namespace,
		},
		Spec: losantv1alpha1.RancherSessionSpec{
			CredentialsSecretRef: losantv1alpha1.SecretRef{
				Name:      "rancher-credentials",
				Namespace: s.Namespace,
			},
			TTLSeconds: ttl,
		},
	}

	if err := s.Client.Create(ctx, rs); err != nil {
		if k8serrors.IsAlreadyExists(err) {
			http.Error(w, "RancherSession already exists", http.StatusConflict)
			return
		}
		log.FromContext(ctx).Error(err, "failed to create RancherSession", "name", name)
		http.Error(w, "failed to create session", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// handleDisconnect deletes the RancherSession CR named after the LosantSync CR.
// Returns 202 on success, 404 if the CR does not exist.
//
// NOTE: the delete verb on ranchersessions is not yet security-approved (see
// docs/rancher-rbac.md on persona/security). This handler will receive a 403
// from the k8s API until a type/security issue is opened and approved.
func (s *Server) handleDisconnect(w http.ResponseWriter, _ *http.Request, ctx context.Context, name string) {
	rs := &losantv1alpha1.RancherSession{}
	if err := s.Client.Get(ctx, types.NamespacedName{Name: name, Namespace: s.Namespace}, rs); err != nil {
		if k8serrors.IsNotFound(err) {
			http.Error(w, "RancherSession not found", http.StatusNotFound)
			return
		}
		log.FromContext(ctx).Error(err, "failed to get RancherSession", "name", name)
		http.Error(w, "failed to look up session", http.StatusInternalServerError)
		return
	}

	if err := s.Client.Delete(ctx, rs); err != nil {
		if k8serrors.IsNotFound(err) {
			http.Error(w, "RancherSession not found", http.StatusNotFound)
			return
		}
		log.FromContext(ctx).Error(err, "failed to delete RancherSession", "name", name)
		http.Error(w, "failed to delete session", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// losantSyncName lists LosantSync CRs in the server's namespace and returns
// the name of the first one (the 1:1 mapping target for RancherSession).
func (s *Server) losantSyncName(ctx context.Context) (string, error) {
	var lsList losantv1alpha1.LosantSyncList
	if err := s.Client.List(ctx, &lsList, client.InNamespace(s.Namespace)); err != nil {
		return "", fmt.Errorf("list LosantSync in %s: %w", s.Namespace, err)
	}
	if len(lsList.Items) == 0 {
		return "", fmt.Errorf("no LosantSync CR found in namespace %s", s.Namespace)
	}
	return lsList.Items[0].Name, nil
}
