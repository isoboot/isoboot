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
	"os"
	"path/filepath"
	"strings"
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

// bootConfigGetCounter counts the reconciler's reads of one BootConfig. Each
// reconcile starts with exactly one, so it counts reconciles.
type bootConfigGetCounter struct {
	client.Client
	name  string
	count atomic.Int32
}

func (c *bootConfigGetCounter) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*isobootgithubiov1alpha1.BootConfig); ok && key.Name == c.name {
		c.count.Add(1)
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

var _ = Describe("BootConfig controller in a manager", func() {
	// startManager runs reconciler in a manager whose cache holds only
	// namespace ns, until the spec ends. wrapClient, when not nil, wraps the
	// manager's client before the reconciler gets it.
	startManager := func(ns string, reconciler *BootConfigReconciler, wrapClient func(client.Client) client.Client) {
		err := k8sClient.Create(ctx, &corev1.Namespace{Name: ns})
		ExpectWithOffset(1, client.IgnoreAlreadyExists(err)).To(Succeed())
		mgr, err := ctrl.NewManager(cfg, ctrl.Options{
			Scheme:                 k8sClient.Scheme(),
			Metrics:                metricsserver.Options{BindAddress: "0"},
			HealthProbeBindAddress: "0",
			Cache:                  cache.Options{DefaultNamespaces: map[string]cache.Config{ns: {}}},
			Controller:             config.Controller{SkipNameValidation: new(true)},
		})
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
		reconciler.Client = mgr.GetClient()
		if wrapClient != nil {
			reconciler.Client = wrapClient(reconciler.Client)
		}
		reconciler.Scheme = mgr.GetScheme()
		reconciler.Namespace = ns
		ExpectWithOffset(1, reconciler.SetupWithManager(mgr)).To(Succeed())
		mgrCtx, stop := context.WithCancel(ctx)
		DeferCleanup(stop)
		go func() {
			defer GinkgoRecover()
			Expect(mgr.Start(mgrCtx)).To(Succeed())
		}()
	}

	It("is not reconciled again because of its own status update", func() {
		const ns = "bc-manager"
		counter := &bootConfigGetCounter{name: "bc-loop"}
		startManager(ns, &BootConfigReconciler{DataDir: GinkgoT().TempDir()}, func(c client.Client) client.Client {
			counter.Client = c
			return counter
		})

		// Missing artifacts: the first reconcile sets Error and asks to be
		// requeued in 10 seconds.
		bc := &isobootgithubiov1alpha1.BootConfig{
			Name: "bc-loop", Namespace: ns,
			Spec: isobootgithubiov1alpha1.BootConfigSpec{
				Netboot: &isobootgithubiov1alpha1.BootConfigNetbootSpec{KernelRef: "nope", InitrdRef: "nope"},
			},
		}
		Expect(k8sClient.Create(ctx, bc)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, bc) })

		// The manager can take a few seconds to start when the machine is
		// busy (make test runs packages in parallel).
		Eventually(func() isobootgithubiov1alpha1.BootConfigPhase {
			var got isobootgithubiov1alpha1.BootConfig
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: "bc-loop", Namespace: ns}, &got)
			return got.Status.Phase
		}).WithTimeout(30 * time.Second).Should(Equal(isobootgithubiov1alpha1.BootConfigPhaseError))
		Consistently(counter.count.Load, 2*time.Second, 100*time.Millisecond).Should(BeEquivalentTo(1))
	})

	It("removes the files of deleted BootConfigs when it starts", func() {
		dataDir := GinkgoT().TempDir()
		bootDir := filepath.Join(dataDir, "boot")
		Expect(os.MkdirAll(filepath.Join(bootDir, "gone"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(bootDir, "boot.ipxe"), []byte("#!ipxe"), 0o644)).To(Succeed())

		startManager("bc-manager-sweep", &BootConfigReconciler{DataDir: dataDir}, nil)

		Eventually(func() string {
			entries, _ := os.ReadDir(bootDir)
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				names = append(names, e.Name())
			}
			return strings.Join(names, " ")
		}).WithTimeout(30 * time.Second).Should(Equal("boot.ipxe"))
	})
})
