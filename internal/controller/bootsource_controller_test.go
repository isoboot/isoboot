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
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	isobootv1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
)

// newFakeReconciler creates a reconciler with a fake client for unit testing
func newFakeReconciler(objs ...client.Object) *BootSourceReconciler {
	scheme := runtime.NewScheme()
	_ = isobootv1alpha1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&isobootv1alpha1.BootSource{}).
		Build()

	return &BootSourceReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}
}

var _ = Describe("BootSource Controller", func() {
	const (
		testName      = "test-bootsource"
		testNamespace = "default"
	)

	Context("Unit tests with fake client", func() {
		It("should set phase to Pending for new resources", func() {
			ctx := context.Background()
			bootSource := newTestBootSource(testName, testNamespace)
			reconciler := newFakeReconciler(bootSource)

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: testName, Namespace: testNamespace},
			})
			Expect(err).NotTo(HaveOccurred())

			updated := &isobootv1alpha1.BootSource{}
			err = reconciler.Get(ctx, types.NamespacedName{Name: testName, Namespace: testNamespace}, updated)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Status.Phase).To(Equal(isobootv1alpha1.PhasePending))
		})

		It("should not change phase if already set", func() {
			ctx := context.Background()
			bootSource := newTestBootSource(testName, testNamespace)
			bootSource.Status.Phase = isobootv1alpha1.PhaseReady
			reconciler := newFakeReconciler(bootSource)

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: testName, Namespace: testNamespace},
			})
			Expect(err).NotTo(HaveOccurred())

			updated := &isobootv1alpha1.BootSource{}
			err = reconciler.Get(ctx, types.NamespacedName{Name: testName, Namespace: testNamespace}, updated)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Status.Phase).To(Equal(isobootv1alpha1.PhaseReady))
		})

		It("should handle not found resources gracefully", func() {
			ctx := context.Background()
			reconciler := newFakeReconciler() // no objects

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "nonexistent", Namespace: testNamespace},
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should work with ISO-based BootSource", func() {
			ctx := context.Background()
			bootSource := newTestBootSourceISO(testName, testNamespace)
			reconciler := newFakeReconciler(bootSource)

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: testName, Namespace: testNamespace},
			})
			Expect(err).NotTo(HaveOccurred())

			updated := &isobootv1alpha1.BootSource{}
			err = reconciler.Get(ctx, types.NamespacedName{Name: testName, Namespace: testNamespace}, updated)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Status.Phase).To(Equal(isobootv1alpha1.PhasePending))
		})
	})

	Context("Integration tests with envtest", func() {
		var (
			ctx                context.Context
			typeNamespacedName types.NamespacedName
			bootSource         *isobootv1alpha1.BootSource
		)

		BeforeEach(func() {
			ctx = context.Background()
			typeNamespacedName = types.NamespacedName{
				Name:      testName,
				Namespace: testNamespace,
			}

			bootSource = newTestBootSource(testName, testNamespace)
			err := k8sClient.Get(ctx, typeNamespacedName, &isobootv1alpha1.BootSource{})
			if errors.IsNotFound(err) {
				Expect(k8sClient.Create(ctx, bootSource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &isobootv1alpha1.BootSource{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			if err == nil {
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
			}
		})

		It("should reconcile successfully with real API server", func() {
			reconciler := &BootSourceReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			updated := &isobootv1alpha1.BootSource{}
			err = k8sClient.Get(ctx, typeNamespacedName, updated)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Status.Phase).To(Equal(isobootv1alpha1.PhasePending))
		})
	})
})
