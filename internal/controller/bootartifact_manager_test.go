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
	"net/http"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	isobootgithubiov1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
)

var _ = Describe("BootArtifact controller in a manager", func() {
	It("waits for the backoff after a failed download and tries a corrected spec at once", func() {
		const ns = "ba-manager"
		Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, &corev1.Namespace{Name: ns}))).To(Succeed())

		content := []byte("kernel image")
		var missingRequests atomic.Int32
		var fileFirstRequested atomic.Int64 // UnixNano of the first request for /file
		serverURL, httpClient, closeServer := withTestServer(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/file" {
				fileFirstRequested.CompareAndSwap(0, time.Now().UnixNano())
				_, _ = w.Write(content)
				return
			}
			missingRequests.Add(1)
			http.NotFound(w, r)
		})
		DeferCleanup(closeServer)

		mgr, err := ctrl.NewManager(cfg, ctrl.Options{
			Scheme:                 k8sClient.Scheme(),
			Metrics:                metricsserver.Options{BindAddress: "0"},
			HealthProbeBindAddress: "0",
			Cache:                  cache.Options{DefaultNamespaces: map[string]cache.Config{ns: {}}},
			Controller:             config.Controller{SkipNameValidation: new(true)},
		})
		Expect(err).NotTo(HaveOccurred())
		reconciler := &BootArtifactReconciler{
			Client:     mgr.GetClient(),
			Scheme:     mgr.GetScheme(),
			DataDir:    GinkgoT().TempDir(),
			HTTPClient: httpClient,
		}
		Expect(reconciler.SetupWithManager(mgr)).To(Succeed())
		mgrCtx, stop := context.WithCancel(ctx)
		DeferCleanup(stop)
		go func() {
			defer GinkgoRecover()
			Expect(mgr.Start(mgrCtx)).To(Succeed())
		}()

		artifact := &isobootgithubiov1alpha1.BootArtifact{
			Name: "ba-backoff", Namespace: ns,
			Spec: isobootgithubiov1alpha1.BootArtifactSpec{URL: serverURL + "/missing", SHA256: new(sha256Hex(content))},
		}
		Expect(k8sClient.Create(ctx, artifact)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, artifact) })

		key := types.NamespacedName{Name: "ba-backoff", Namespace: ns}
		phase := func() isobootgithubiov1alpha1.BootArtifactPhase {
			var got isobootgithubiov1alpha1.BootArtifact
			_ = k8sClient.Get(ctx, key, &got)
			return got.Status.Phase
		}

		// The 404 sets Error and asks for the next attempt in 10 seconds. The
		// controller's own status writes (Downloading, Error) must not start
		// it sooner. The manager can take a few seconds to start when the
		// machine is busy (make test runs packages in parallel).
		Eventually(phase).WithTimeout(30 * time.Second).Should(Equal(isobootgithubiov1alpha1.BootArtifactPhaseError))
		Consistently(missingRequests.Load, 2*time.Second, 100*time.Millisecond).Should(BeEquivalentTo(1))

		// A corrected URL is a spec change: it is tried at once, not when
		// the 10-second backoff ends.
		Eventually(func() error {
			var got isobootgithubiov1alpha1.BootArtifact
			if err := k8sClient.Get(ctx, key, &got); err != nil {
				return err
			}
			got.Spec.URL = serverURL + "/file"
			return k8sClient.Update(ctx, &got)
		}).Should(Succeed())
		updated := time.Now()
		Eventually(phase).WithTimeout(30 * time.Second).Should(Equal(isobootgithubiov1alpha1.BootArtifactPhaseReady))
		Expect(time.Unix(0, fileFirstRequested.Load()).Sub(updated)).To(BeNumerically("<", 5*time.Second))
	})
})
