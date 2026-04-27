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

// Package integration holds envtest-based integration tests for the losant-device
// operator. Tests run against either:
//   - an envtest-spawned API server (CI default, requires KUBEBUILDER_ASSETS)
//   - an existing cluster (set USE_EXISTING_CLUSTER=true, uses ~/.kube/config)
package integration_test

import (
	"context"
	"os"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	losantv1alpha1 "github.com/mak3r/losant-device/api/v1alpha1"
	"github.com/mak3r/losant-device/internal/controller"
)

var (
	cfg       *rest.Config
	k8sClient client.Client
	testEnv   *envtest.Environment
	scheme    = runtime.NewScheme()
	ctx       context.Context
	cancel    context.CancelFunc
)

func TestIntegration(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Integration Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.Background())

	By("bootstrapping test environment")
	useExisting := os.Getenv("USE_EXISTING_CLUSTER") == "true"
	testEnv = &envtest.Environment{
		UseExistingCluster: &useExisting,
	}
	if !useExisting {
		testEnv.CRDDirectoryPaths = []string{"../../config/crd/bases"}
		testEnv.ErrorIfCRDPathMissing = false
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	By("registering schemes")
	Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
	Expect(losantv1alpha1.AddToScheme(scheme)).To(Succeed())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	By("starting controller manager")
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme,
		// Disable metrics server in tests to avoid port conflicts.
		Metrics: metricsserver.Options{BindAddress: "0"},
	})
	Expect(err).NotTo(HaveOccurred())

	err = (&controller.LosantSyncReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr)
	Expect(err).NotTo(HaveOccurred())

	go func() {
		defer GinkgoRecover()
		Expect(mgr.Start(ctx)).To(Succeed())
	}()
})

var _ = AfterSuite(func() {
	cancel()
	By("tearing down the test environment")
	Expect(testEnv.Stop()).To(Succeed())
})

// Smoke test: confirm the test suite itself can talk to the API server.
var _ = Describe("Suite smoke test", func() {
	It("creates and deletes a namespace without error", func() {
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "losant-test-",
			},
		}
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())
		Expect(k8sClient.Delete(ctx, ns)).To(Succeed())
	})
})
