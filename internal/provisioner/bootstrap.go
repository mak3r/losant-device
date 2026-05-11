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

// Package provisioner handles one-time GEA credential bootstrap.
package provisioner

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	losantv1alpha1 "github.com/mak3r/losant-device/api/v1alpha1"
	"github.com/mak3r/losant-device/internal/losant"
)

const geaCredentialsSecretName = "losant-gea-credentials"

// EnsureCredentialPlaceholder creates the losant-gea-credentials Secret with empty values
// if it does not already exist. This allows the GEA pod to start (avoiding
// CreateContainerConfigError) before real credentials are provisioned by Bootstrap.
// It is idempotent and makes no Losant API calls.
func EnsureCredentialPlaceholder(ctx context.Context, c client.Client, ns string) error {
	var existing corev1.Secret
	err := c.Get(ctx, types.NamespacedName{Name: geaCredentialsSecretName, Namespace: ns}, &existing)
	if err == nil {
		return nil
	}
	if !errors.IsNotFound(err) {
		return fmt.Errorf("read %s/%s: %w", ns, geaCredentialsSecretName, err)
	}
	placeholder := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      geaCredentialsSecretName,
			Namespace: ns,
		},
		Data: map[string][]byte{
			"DEVICE_ID":     []byte(""),
			"ACCESS_KEY":    []byte(""),
			"ACCESS_SECRET": []byte(""),
		},
	}
	if createErr := c.Create(ctx, placeholder); createErr != nil && !errors.IsAlreadyExists(createErr) {
		return fmt.Errorf("create placeholder %s/%s: %w", ns, geaCredentialsSecretName, createErr)
	}
	return nil
}

// GEABootstrapper provisions initial GEA MQTT credentials into a cluster Secret.
type GEABootstrapper struct {
	Client       client.Client
	LosantClient losant.LosantClient
}

// Bootstrap writes the losant-gea-credentials Secret if it is absent or incomplete.
// It is idempotent: a Secret that already contains DEVICE_ID, ACCESS_KEY, and
// ACCESS_SECRET causes an immediate return with no API calls.
func (b *GEABootstrapper) Bootstrap(ctx context.Context, ls *losantv1alpha1.LosantSync, clusterDeviceID string) error {
	ns := ls.Spec.ProvisioningSecretRef.Namespace

	var existing corev1.Secret
	getErr := b.Client.Get(ctx, types.NamespacedName{Name: geaCredentialsSecretName, Namespace: ns}, &existing)
	switch {
	case getErr == nil:
		d := existing.Data
		if len(d["DEVICE_ID"]) > 0 && len(d["ACCESS_KEY"]) > 0 && len(d["ACCESS_SECRET"]) > 0 &&
			string(d["DEVICE_ID"]) == clusterDeviceID {
			return nil
		}
	case errors.IsNotFound(getErr):
		// will create below
	default:
		return fmt.Errorf("read %s/%s: %w", ns, geaCredentialsSecretName, getErr)
	}

	keyName := fmt.Sprintf("losant-device-controller-%s-%d", ls.Spec.ClusterName, time.Now().Unix())
	_, key, secret, err := b.LosantClient.CreateDeviceAccessKey(ctx, ls.Spec.ApplicationID, clusterDeviceID, keyName)
	if err != nil {
		return fmt.Errorf("create device access key: %w", err)
	}

	data := map[string][]byte{
		"DEVICE_ID":     []byte(clusterDeviceID),
		"ACCESS_KEY":    []byte(key),
		"ACCESS_SECRET": []byte(secret),
	}

	if errors.IsNotFound(getErr) {
		cred := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      geaCredentialsSecretName,
				Namespace: ns,
			},
			Data: data,
		}
		if err := b.Client.Create(ctx, cred); err != nil {
			return fmt.Errorf("create %s/%s: %w", ns, geaCredentialsSecretName, err)
		}
	} else {
		patched := existing.DeepCopy()
		patched.Data = data
		if err := b.Client.Update(ctx, patched); err != nil {
			return fmt.Errorf("update %s/%s: %w", ns, geaCredentialsSecretName, err)
		}
	}

	if ls.Spec.GEA.DeploymentRef != "" {
		if err := b.restartDeployment(ctx, ns, ls.Spec.GEA.DeploymentRef); err != nil {
			return err
		}
	}

	return nil
}

func (b *GEABootstrapper) restartDeployment(ctx context.Context, ns, name string) error {
	var dep appsv1.Deployment
	if err := b.Client.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, &dep); err != nil {
		return fmt.Errorf("get GEA Deployment %s/%s: %w", ns, name, err)
	}
	patched := dep.DeepCopy()
	if patched.Spec.Template.Annotations == nil {
		patched.Spec.Template.Annotations = make(map[string]string)
	}
	patched.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().Format(time.RFC3339)
	if err := b.Client.Patch(ctx, patched, client.MergeFrom(&dep)); err != nil {
		return fmt.Errorf("patch GEA Deployment %s/%s: %w", ns, name, err)
	}
	return nil
}
