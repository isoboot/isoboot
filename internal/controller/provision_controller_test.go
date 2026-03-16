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
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	isobootgithubiov1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
)

var _ = Describe("Provision Controller", func() {
	ctx := context.Background()

	It("sets phase to Pending on a new Provision", func() {
		prov := &isobootgithubiov1alpha1.Provision{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-default-phase",
				Namespace: "default",
			},
			Spec: isobootgithubiov1alpha1.ProvisionSpec{
				MachineRef:             "some-machine",
				BootConfigRef:          "some-bootconfig",
				ProvisionAutomationRef: "some-automation",
			},
		}
		Expect(k8sClient.Create(ctx, prov)).To(Succeed())
		defer func() {
			Expect(k8sClient.Delete(ctx, prov)).To(Succeed())
		}()

		reconciler := &ProvisionReconciler{
			Client: k8sClient,
			Scheme: scheme.Scheme,
		}

		_, err := reconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      "test-default-phase",
				Namespace: "default",
			},
		})
		Expect(err).NotTo(HaveOccurred())

		var fetched isobootgithubiov1alpha1.Provision
		Expect(k8sClient.Get(ctx, types.NamespacedName{
			Name:      "test-default-phase",
			Namespace: "default",
		}, &fetched)).To(Succeed())
		Expect(fetched.Status.Phase).To(Equal(
			isobootgithubiov1alpha1.ProvisionPhasePending))
	})

	It("does not overwrite an existing phase", func() {
		prov := &isobootgithubiov1alpha1.Provision{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-existing-phase",
				Namespace: "default",
			},
			Spec: isobootgithubiov1alpha1.ProvisionSpec{
				MachineRef:             "some-machine",
				BootConfigRef:          "some-bootconfig",
				ProvisionAutomationRef: "some-automation",
			},
		}
		Expect(k8sClient.Create(ctx, prov)).To(Succeed())
		defer func() {
			Expect(k8sClient.Delete(ctx, prov)).To(Succeed())
		}()

		// Set phase to Complete via status subresource.
		prov.Status.Phase = isobootgithubiov1alpha1.ProvisionPhaseComplete
		Expect(k8sClient.Status().Update(ctx, prov)).To(Succeed())

		reconciler := &ProvisionReconciler{
			Client: k8sClient,
			Scheme: scheme.Scheme,
		}

		_, err := reconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      "test-existing-phase",
				Namespace: "default",
			},
		})
		Expect(err).NotTo(HaveOccurred())

		var fetched isobootgithubiov1alpha1.Provision
		Expect(k8sClient.Get(ctx, types.NamespacedName{
			Name:      "test-existing-phase",
			Namespace: "default",
		}, &fetched)).To(Succeed())
		Expect(fetched.Status.Phase).To(Equal(
			isobootgithubiov1alpha1.ProvisionPhaseComplete))
	})
})
