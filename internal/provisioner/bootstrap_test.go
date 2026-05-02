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

package provisioner_test

import (
	"context"
	"errors"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	losantv1alpha1 "github.com/mak3r/losant-device/api/v1alpha1"
	"github.com/mak3r/losant-device/internal/losant"
	"github.com/mak3r/losant-device/internal/provisioner"
)

const (
	testNS        = "default"
	geaSecretName = "losant-gea-credentials"
)

var bootstrapScheme = func() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = losantv1alpha1.AddToScheme(s)
	return s
}()

func baseLS() *losantv1alpha1.LosantSync {
	return &losantv1alpha1.LosantSync{
		ObjectMeta: metav1.ObjectMeta{Name: "test-ls", Namespace: testNS},
		Spec: losantv1alpha1.LosantSyncSpec{
			ApplicationID: "app-123",
			ClusterName:   "test-cluster",
			ProvisioningSecretRef: losantv1alpha1.SecretRef{
				Name:      "losant-creds",
				Namespace: testNS,
			},
		},
	}
}

func buildFakeClient(objs ...client.Object) client.Client {
	return fake.NewClientBuilder().
		WithScheme(bootstrapScheme).
		WithObjects(objs...).
		Build()
}

func buildFakeClientWithInterceptor(funcs interceptor.Funcs, objs ...client.Object) client.Client {
	return fake.NewClientBuilder().
		WithScheme(bootstrapScheme).
		WithObjects(objs...).
		WithInterceptorFuncs(funcs).
		Build()
}

// TestBootstrap_SecretNotFound verifies that Bootstrap creates the secret when absent.
func TestBootstrap_SecretNotFound(t *testing.T) {
	mc := losant.NewMockClient()
	b := &provisioner.GEABootstrapper{
		Client:       buildFakeClient(),
		LosantClient: mc,
	}

	if err := b.Bootstrap(context.Background(), baseLS(), "device-123"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mc.CreateDeviceAccessKeyCalls) != 1 {
		t.Errorf("expected 1 CreateDeviceAccessKey call, got %d", len(mc.CreateDeviceAccessKeyCalls))
	}

	var got corev1.Secret
	if err := b.Client.Get(context.Background(), types.NamespacedName{Name: geaSecretName, Namespace: testNS}, &got); err != nil {
		t.Fatalf("secret not created: %v", err)
	}
	if string(got.Data["DEVICE_ID"]) != "device-123" {
		t.Errorf("DEVICE_ID: got %q, want %q", string(got.Data["DEVICE_ID"]), "device-123")
	}
	if string(got.Data["ACCESS_KEY"]) != "mock-access-key" {
		t.Errorf("ACCESS_KEY: got %q, want %q", string(got.Data["ACCESS_KEY"]), "mock-access-key")
	}
	if string(got.Data["ACCESS_SECRET"]) != "mock-access-secret" {
		t.Errorf("ACCESS_SECRET: got %q, want %q", string(got.Data["ACCESS_SECRET"]), "mock-access-secret")
	}
}

// TestBootstrap_SecretIncomplete verifies that Bootstrap patches an incomplete secret.
func TestBootstrap_SecretIncomplete(t *testing.T) {
	incomplete := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: geaSecretName, Namespace: testNS},
		Data:       map[string][]byte{"DEVICE_ID": []byte("old-device")},
	}

	mc := losant.NewMockClient()
	b := &provisioner.GEABootstrapper{
		Client:       buildFakeClient(incomplete),
		LosantClient: mc,
	}

	if err := b.Bootstrap(context.Background(), baseLS(), "device-456"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mc.CreateDeviceAccessKeyCalls) != 1 {
		t.Errorf("expected 1 CreateDeviceAccessKey call, got %d", len(mc.CreateDeviceAccessKeyCalls))
	}

	var got corev1.Secret
	if err := b.Client.Get(context.Background(), types.NamespacedName{Name: geaSecretName, Namespace: testNS}, &got); err != nil {
		t.Fatalf("secret not found after patch: %v", err)
	}
	if string(got.Data["DEVICE_ID"]) != "device-456" {
		t.Errorf("DEVICE_ID: got %q, want %q", string(got.Data["DEVICE_ID"]), "device-456")
	}
}

// TestBootstrap_SecretComplete verifies that Bootstrap is idempotent when all credentials exist.
func TestBootstrap_SecretComplete(t *testing.T) {
	complete := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: geaSecretName, Namespace: testNS},
		Data: map[string][]byte{
			"DEVICE_ID":     []byte("d"),
			"ACCESS_KEY":    []byte("k"),
			"ACCESS_SECRET": []byte("s"),
		},
	}

	mc := losant.NewMockClient()
	b := &provisioner.GEABootstrapper{
		Client:       buildFakeClient(complete),
		LosantClient: mc,
	}

	if err := b.Bootstrap(context.Background(), baseLS(), "device-789"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mc.CreateDeviceAccessKeyCalls) != 0 {
		t.Errorf("expected 0 CreateDeviceAccessKey calls, got %d", len(mc.CreateDeviceAccessKeyCalls))
	}
}

// TestBootstrap_CreateAccessKeyError verifies that a Losant API error is propagated.
func TestBootstrap_CreateAccessKeyError(t *testing.T) {
	apiErr := errors.New("losant unavailable")
	mc := losant.NewMockClient()
	mc.CreateDeviceAccessKeyFunc = func(_ context.Context, _, _, _ string) (string, string, string, error) {
		return "", "", "", apiErr
	}

	b := &provisioner.GEABootstrapper{
		Client:       buildFakeClient(),
		LosantClient: mc,
	}

	err := b.Bootstrap(context.Background(), baseLS(), "device-x")
	if !errors.Is(err, apiErr) {
		t.Errorf("expected losant error, got: %v", err)
	}
}

// TestBootstrap_SecretCreateFails verifies that a k8s Create failure is propagated.
func TestBootstrap_SecretCreateFails(t *testing.T) {
	createErr := errors.New("etcd write failed")
	c := buildFakeClientWithInterceptor(interceptor.Funcs{
		Create: func(_ context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if _, ok := obj.(*corev1.Secret); ok {
				return createErr
			}
			return cl.Create(context.Background(), obj, opts...)
		},
	})

	b := &provisioner.GEABootstrapper{
		Client:       c,
		LosantClient: losant.NewMockClient(),
	}

	err := b.Bootstrap(context.Background(), baseLS(), "device-x")
	if !errors.Is(err, createErr) {
		t.Errorf("expected create error, got: %v", err)
	}
}

// TestBootstrap_SecretUpdateFails verifies that a k8s Update failure on an incomplete secret is propagated.
func TestBootstrap_SecretUpdateFails(t *testing.T) {
	updateErr := errors.New("update rejected")
	incomplete := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: geaSecretName, Namespace: testNS},
		Data:       map[string][]byte{"DEVICE_ID": []byte("old")},
	}

	c := buildFakeClientWithInterceptor(interceptor.Funcs{
		Update: func(_ context.Context, cl client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			if _, ok := obj.(*corev1.Secret); ok {
				return updateErr
			}
			return cl.Update(context.Background(), obj, opts...)
		},
	}, incomplete)

	b := &provisioner.GEABootstrapper{
		Client:       c,
		LosantClient: losant.NewMockClient(),
	}

	err := b.Bootstrap(context.Background(), baseLS(), "device-x")
	if !errors.Is(err, updateErr) {
		t.Errorf("expected update error, got: %v", err)
	}
}

// TestBootstrap_DeploymentRestart verifies the GEA Deployment is patched with the restartedAt annotation.
func TestBootstrap_DeploymentRestart(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "losant-gea", Namespace: testNS},
	}

	mc := losant.NewMockClient()
	b := &provisioner.GEABootstrapper{
		Client:       buildFakeClient(dep),
		LosantClient: mc,
	}

	ls := baseLS()
	ls.Spec.GEA.DeploymentRef = "losant-gea"

	if err := b.Bootstrap(context.Background(), ls, "device-rst"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var got appsv1.Deployment
	if err := b.Client.Get(context.Background(), types.NamespacedName{Name: "losant-gea", Namespace: testNS}, &got); err != nil {
		t.Fatalf("deployment not found: %v", err)
	}
	if got.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] == "" {
		t.Errorf("expected restartedAt annotation to be set on deployment")
	}
}

// TestBootstrap_DeploymentNotFound verifies that a missing GEA Deployment returns an error.
func TestBootstrap_DeploymentNotFound(t *testing.T) {
	mc := losant.NewMockClient()
	b := &provisioner.GEABootstrapper{
		Client:       buildFakeClient(),
		LosantClient: mc,
	}

	ls := baseLS()
	ls.Spec.GEA.DeploymentRef = "losant-gea"

	err := b.Bootstrap(context.Background(), ls, "device-x")
	if err == nil {
		t.Errorf("expected error for missing deployment, got nil")
	}
}
