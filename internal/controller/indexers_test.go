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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	isobootgithubiov1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
)

var _ = Describe("Provision status.phase indexer", func() {
	var (
		indexedClient client.Client
		mgrCancel     context.CancelFunc
	)

	BeforeEach(func() {
		mgr, err := ctrl.NewManager(cfg, ctrl.Options{
			Scheme: scheme.Scheme,
		})
		Expect(err).NotTo(HaveOccurred())

		Expect(SetupIndexers(ctx, mgr)).To(Succeed())

		indexedClient = mgr.GetClient()

		var mgrCtx context.Context
		mgrCtx, mgrCancel = context.WithCancel(ctx)
		go func() {
			defer GinkgoRecover()
			Expect(mgr.Start(mgrCtx)).To(Succeed())
		}()
	})

	AfterEach(func() {
		mgrCancel()
	})

	provision := func(
		name string, phase isobootgithubiov1alpha1.ProvisionPhase,
	) *isobootgithubiov1alpha1.Provision {
		p := &isobootgithubiov1alpha1.Provision{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: "default",
			},
			Spec: isobootgithubiov1alpha1.ProvisionSpec{
				MachineRef:         "machine-1",
				BootConfigRef:      "bootconfig-1",
				ProvisionAnswerRef: "answer-1",
			},
		}
		Expect(k8sClient.Create(ctx, p)).To(Succeed())
		if phase != "" {
			p.Status.Phase = phase
			Expect(k8sClient.Status().Update(ctx, p)).
				To(Succeed())
		}
		return p
	}

	It("returns only Pending provisions", func() {
		p1 := provision("idx-pending", isobootgithubiov1alpha1.ProvisionPhasePending)
		p2 := provision("idx-complete", isobootgithubiov1alpha1.ProvisionPhaseComplete)
		p3 := provision("idx-pending2", isobootgithubiov1alpha1.ProvisionPhasePending)

		defer func() {
			Expect(k8sClient.Delete(ctx, p1)).To(Succeed())
			Expect(k8sClient.Delete(ctx, p2)).To(Succeed())
			Expect(k8sClient.Delete(ctx, p3)).To(Succeed())
		}()

		var list isobootgithubiov1alpha1.ProvisionList
		Eventually(func() int {
			list = isobootgithubiov1alpha1.ProvisionList{}
			err := indexedClient.List(ctx, &list,
				client.MatchingFields{ProvisionPhaseField: "Pending"})
			if err != nil {
				return -1
			}
			return len(list.Items)
		}).Should(Equal(2))

		names := []string{list.Items[0].Name, list.Items[1].Name}
		Expect(names).To(ContainElements("idx-pending", "idx-pending2"))
	})

	It("returns empty list when no provisions match", func() {
		p := provision("idx-inprogress",
			isobootgithubiov1alpha1.ProvisionPhaseInProgress)
		defer func() {
			Expect(k8sClient.Delete(ctx, p)).To(Succeed())
		}()

		// Wait for the InProgress provision to appear in the cache
		// before asserting that Pending returns 0.
		Eventually(func() int {
			var all isobootgithubiov1alpha1.ProvisionList
			err := indexedClient.List(ctx, &all,
				client.MatchingFields{
					ProvisionPhaseField: "InProgress",
				})
			if err != nil {
				return -1
			}
			return len(all.Items)
		}).Should(Equal(1))

		var list isobootgithubiov1alpha1.ProvisionList
		Expect(indexedClient.List(ctx, &list,
			client.MatchingFields{ProvisionPhaseField: "Pending"})).
			To(Succeed())
		Expect(list.Items).To(BeEmpty())
	})
})
