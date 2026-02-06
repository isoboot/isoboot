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
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	isobootv1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
)

// newFakeReconciler creates a reconciler with a fake client for unit testing
func newFakeReconciler(objs ...client.Object) *BootSourceReconciler {
	scheme := runtime.NewScheme()
	if err := isobootv1alpha1.AddToScheme(scheme); err != nil {
		panic(err)
	}
	if err := batchv1.AddToScheme(scheme); err != nil {
		panic(err)
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&isobootv1alpha1.BootSource{}).
		Build()

	return &BootSourceReconciler{
		Client:          fakeClient,
		Scheme:          scheme,
		HostPathBaseDir: "/var/lib/isoboot",
		DownloadImage:   testDownloadImage,
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

		It("should return an error when Get fails with a non-NotFound error", func() {
			ctx := context.Background()
			// Build a client with an empty scheme so Get returns a "no kind is registered" error
			emptyScheme := runtime.NewScheme()
			fakeClient := fake.NewClientBuilder().
				WithScheme(emptyScheme).
				Build()

			reconciler := &BootSourceReconciler{
				Client:          fakeClient,
				Scheme:          emptyScheme,
				HostPathBaseDir: "/var/lib/isoboot",
				DownloadImage:   testDownloadImage,
			}

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: testName, Namespace: testNamespace},
			})
			Expect(err).To(HaveOccurred())
		})
	})

	Context("Pending phase", func() {
		It("should create a download Job and transition to Downloading", func() {
			ctx := context.Background()
			pendingName := "pending-source"
			bootSource := newTestBootSource(pendingName, testNamespace)
			bootSource.Status.Phase = isobootv1alpha1.PhasePending
			reconciler := newFakeReconciler(bootSource)

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: pendingName, Namespace: testNamespace},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify phase transitioned to Downloading
			updated := &isobootv1alpha1.BootSource{}
			err = reconciler.Get(ctx, types.NamespacedName{Name: pendingName, Namespace: testNamespace}, updated)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Status.Phase).To(Equal(isobootv1alpha1.PhaseDownloading))
			Expect(updated.Status.DownloadJobName).To(Equal(downloadJobName(pendingName)))

			// Verify the Job was created
			job := &batchv1.Job{}
			err = reconciler.Get(ctx, types.NamespacedName{
				Name:      downloadJobName(pendingName),
				Namespace: testNamespace,
			}, job)
			Expect(err).NotTo(HaveOccurred())
			Expect(job.Spec.Template.Spec.Containers[0].Image).To(Equal(testDownloadImage))
		})
	})

	Context("Downloading phase", func() {
		It("should transition to Verifying when Job completes", func() {
			ctx := context.Background()
			bootSource := newTestBootSource(testName, testNamespace)
			bootSource.Status.Phase = isobootv1alpha1.PhaseDownloading
			bootSource.Status.DownloadJobName = downloadJobName(testName)

			// Create a completed Job
			completedJob := &batchv1.Job{}
			completedJob.Name = downloadJobName(testName)
			completedJob.Namespace = testNamespace
			completedJob.Status.Conditions = []batchv1.JobCondition{
				{Type: batchv1.JobComplete, Status: corev1.ConditionTrue},
			}

			reconciler := newFakeReconciler(bootSource, completedJob)

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: testName, Namespace: testNamespace},
			})
			Expect(err).NotTo(HaveOccurred())

			updated := &isobootv1alpha1.BootSource{}
			err = reconciler.Get(ctx, types.NamespacedName{Name: testName, Namespace: testNamespace}, updated)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Status.Phase).To(Equal(isobootv1alpha1.PhaseVerifying))
			Expect(updated.Status.ArtifactPaths).To(HaveKey("kernel"))
			Expect(updated.Status.ArtifactPaths).To(HaveKey("initrd"))
		})

		It("should transition to Failed when Job fails", func() {
			ctx := context.Background()
			ns := "custom-ns"
			bootSource := newTestBootSource(testName, ns)
			bootSource.Status.Phase = isobootv1alpha1.PhaseDownloading
			bootSource.Status.DownloadJobName = downloadJobName(testName)

			failedJob := &batchv1.Job{}
			failedJob.Name = downloadJobName(testName)
			failedJob.Namespace = ns
			failedJob.Status.Conditions = []batchv1.JobCondition{
				{Type: batchv1.JobFailed, Status: corev1.ConditionTrue},
			}

			reconciler := newFakeReconciler(bootSource, failedJob)

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: testName, Namespace: ns},
			})
			Expect(err).NotTo(HaveOccurred())

			updated := &isobootv1alpha1.BootSource{}
			err = reconciler.Get(ctx, types.NamespacedName{Name: testName, Namespace: ns}, updated)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Status.Phase).To(Equal(isobootv1alpha1.PhaseFailed))
		})

		It("should revert to Pending when Job is missing", func() {
			ctx := context.Background()
			bootSource := newTestBootSource(testName, testNamespace)
			bootSource.Status.Phase = isobootv1alpha1.PhaseDownloading
			bootSource.Status.DownloadJobName = downloadJobName(testName)

			// No Job object in the fake client
			reconciler := newFakeReconciler(bootSource)

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: testName, Namespace: testNamespace},
			})
			Expect(err).NotTo(HaveOccurred())

			updated := &isobootv1alpha1.BootSource{}
			err = reconciler.Get(ctx, types.NamespacedName{Name: testName, Namespace: testNamespace}, updated)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Status.Phase).To(Equal(isobootv1alpha1.PhasePending))
			Expect(updated.Status.DownloadJobName).To(BeEmpty())
		})

		It("should revert to Pending when DownloadJobName is empty", func() {
			ctx := context.Background()
			bootSource := newTestBootSource(testName, testNamespace)
			bootSource.Status.Phase = isobootv1alpha1.PhaseDownloading
			bootSource.Status.DownloadJobName = ""

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

		It("should stay in Downloading when Job is still running", func() {
			ctx := context.Background()
			bootSource := newTestBootSource(testName, testNamespace)
			bootSource.Status.Phase = isobootv1alpha1.PhaseDownloading
			bootSource.Status.DownloadJobName = downloadJobName(testName)

			// Job with no conditions (still running)
			runningJob := &batchv1.Job{}
			runningJob.Name = downloadJobName(testName)
			runningJob.Namespace = testNamespace

			reconciler := newFakeReconciler(bootSource, runningJob)

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: testName, Namespace: testNamespace},
			})
			Expect(err).NotTo(HaveOccurred())

			updated := &isobootv1alpha1.BootSource{}
			err = reconciler.Get(ctx, types.NamespacedName{Name: testName, Namespace: testNamespace}, updated)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Status.Phase).To(Equal(isobootv1alpha1.PhaseDownloading))
		})
	})

	Context("SetupWithManager", func() {
		It("should register the controller with the manager", func() {
			mgr, err := ctrl.NewManager(cfg, ctrl.Options{
				Scheme: k8sClient.Scheme(),
			})
			Expect(err).NotTo(HaveOccurred())

			reconciler := &BootSourceReconciler{
				Client:          mgr.GetClient(),
				Scheme:          mgr.GetScheme(),
				HostPathBaseDir: "/var/lib/isoboot",
				DownloadImage:   testDownloadImage,
			}
			Expect(reconciler.SetupWithManager(mgr)).To(Succeed())
		})
	})

	Context("Integration tests with envtest", func() {
		var (
			ctx                context.Context
			typeNamespacedName types.NamespacedName
		)

		BeforeEach(func() {
			ctx = context.Background()
			typeNamespacedName = types.NamespacedName{
				Name:      testName,
				Namespace: testNamespace,
			}

			err := k8sClient.Get(ctx, typeNamespacedName, &isobootv1alpha1.BootSource{})
			if err != nil {
				if errors.IsNotFound(err) {
					Expect(k8sClient.Create(ctx, newTestBootSource(testName, testNamespace))).To(Succeed())
				} else {
					Expect(err).NotTo(HaveOccurred())
				}
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
				Client:          k8sClient,
				Scheme:          k8sClient.Scheme(),
				HostPathBaseDir: "/var/lib/isoboot",
				DownloadImage:   testDownloadImage,
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
