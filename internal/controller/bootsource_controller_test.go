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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	isobootv1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
	"github.com/isoboot/isoboot/internal/downloader"
)

var testNN = types.NamespacedName{Name: testName, Namespace: testNamespace}

// fakeJobBuilder implements JobBuilder for unit tests by returning a minimal Job.
type fakeJobBuilder struct{}

func (f *fakeJobBuilder) Build(bootSource *isobootv1alpha1.BootSource) (*batchv1.Job, error) {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      bootSource.Name + downloader.JobNameSuffix,
			Namespace: bootSource.Namespace,
			Labels: map[string]string{
				"isoboot.github.io/bootsource": bootSource.Name,
				"app.kubernetes.io/component":  "downloader",
				"app.kubernetes.io/managed-by": "isoboot",
			},
		},
	}, nil
}

func newTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	Expect(isobootv1alpha1.AddToScheme(s)).To(Succeed())
	Expect(batchv1.AddToScheme(s)).To(Succeed())
	return s
}

// newFakeReconciler creates a reconciler with a fake client and fakeJobBuilder.
func newFakeReconciler(objs ...client.Object) *BootSourceReconciler {
	s := newTestScheme()
	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(objs...).
		WithStatusSubresource(&isobootv1alpha1.BootSource{}).
		Build()

	return &BootSourceReconciler{
		Client:     fakeClient,
		Scheme:     s,
		JobBuilder: &fakeJobBuilder{},
	}
}

// reconcileAndGetPhase runs one reconcile cycle and returns the resulting phase.
func reconcileAndGetPhase(reconciler *BootSourceReconciler) isobootv1alpha1.BootSourcePhase {
	_, err := reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: testNN})
	Expect(err).NotTo(HaveOccurred())

	updated := &isobootv1alpha1.BootSource{}
	Expect(reconciler.Get(context.Background(), testNN, updated)).To(Succeed())
	return updated.Status.Phase
}

var _ = Describe("BootSource Controller", func() {
	Context("Unit tests with fake client", func() {
		It("should set phase to Pending for new resources", func() {
			reconciler := newFakeReconciler(newTestBootSource())
			Expect(reconcileAndGetPhase(reconciler)).To(Equal(isobootv1alpha1.PhasePending))
		})

		It("should not change phase if already set", func() {
			bootSource := newTestBootSource()
			bootSource.Status.Phase = isobootv1alpha1.PhaseReady
			reconciler := newFakeReconciler(bootSource)

			Expect(reconcileAndGetPhase(reconciler)).To(Equal(isobootv1alpha1.PhaseReady))
		})

		It("should handle not found resources gracefully", func() {
			reconciler := newFakeReconciler()
			_, err := reconciler.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "nonexistent", Namespace: testNamespace},
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should work with ISO-based BootSource", func() {
			reconciler := newFakeReconciler(newTestBootSourceISO())
			Expect(reconcileAndGetPhase(reconciler)).To(Equal(isobootv1alpha1.PhasePending))
		})

		It("should create download job and transition to Downloading from Pending", func() {
			bootSource := newTestBootSource()
			bootSource.Status.Phase = isobootv1alpha1.PhasePending
			reconciler := newFakeReconciler(bootSource)

			Expect(reconcileAndGetPhase(reconciler)).To(Equal(isobootv1alpha1.PhaseDownloading))

			// Verify job was created
			job := &batchv1.Job{}
			Expect(reconciler.Get(context.Background(), types.NamespacedName{Name: testName + "-download", Namespace: testNamespace}, job)).To(Succeed())
			Expect(job.Labels["isoboot.github.io/bootsource"]).To(Equal(testName))
			Expect(job.Labels["app.kubernetes.io/component"]).To(Equal("downloader"))
		})

		It("should transition to Ready when download job completes", func() {
			bootSource := newTestBootSource()
			bootSource.Status.Phase = isobootv1alpha1.PhaseDownloading
			job := newTestDownloadJob(newJobCondition(batchv1.JobComplete, corev1.ConditionTrue))
			reconciler := newFakeReconciler(bootSource, job)

			Expect(reconcileAndGetPhase(reconciler)).To(Equal(isobootv1alpha1.PhaseReady))
		})

		It("should transition to Failed when download job fails", func() {
			bootSource := newTestBootSource()
			bootSource.Status.Phase = isobootv1alpha1.PhaseDownloading
			job := newTestDownloadJob(newJobCondition(batchv1.JobFailed, corev1.ConditionTrue))
			reconciler := newFakeReconciler(bootSource, job)

			Expect(reconcileAndGetPhase(reconciler)).To(Equal(isobootv1alpha1.PhaseFailed))
		})

		It("should stay in Downloading when job is still running", func() {
			bootSource := newTestBootSource()
			bootSource.Status.Phase = isobootv1alpha1.PhaseDownloading
			job := newTestDownloadJob() // no conditions = still running
			reconciler := newFakeReconciler(bootSource, job)

			Expect(reconcileAndGetPhase(reconciler)).To(Equal(isobootv1alpha1.PhaseDownloading))
		})

		It("should transition to Downloading when job already exists", func() {
			bootSource := newTestBootSource()
			bootSource.Status.Phase = isobootv1alpha1.PhasePending
			job := newTestDownloadJob() // job already exists
			reconciler := newFakeReconciler(bootSource, job)

			Expect(reconcileAndGetPhase(reconciler)).To(Equal(isobootv1alpha1.PhaseDownloading))
		})

		It("should transition back to Pending when job is deleted externally during Downloading", func() {
			bootSource := newTestBootSource()
			bootSource.Status.Phase = isobootv1alpha1.PhaseDownloading
			// No job object = simulates external deletion
			reconciler := newFakeReconciler(bootSource)

			Expect(reconcileAndGetPhase(reconciler)).To(Equal(isobootv1alpha1.PhasePending))
		})
	})

	Context("Integration tests with envtest", func() {
		var ctx context.Context

		BeforeEach(func() {
			ctx = context.Background()

			err := k8sClient.Get(ctx, testNN, &isobootv1alpha1.BootSource{})
			if err != nil {
				if errors.IsNotFound(err) {
					Expect(k8sClient.Create(ctx, newTestBootSource())).To(Succeed())
				} else {
					Expect(err).NotTo(HaveOccurred())
				}
			}
		})

		AfterEach(func() {
			resource := &isobootv1alpha1.BootSource{}
			err := k8sClient.Get(ctx, testNN, resource)
			if err == nil {
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
			}
		})

		It("should reconcile successfully with real API server", func() {
			reconciler := &BootSourceReconciler{
				Client:     k8sClient,
				Scheme:     k8sClient.Scheme(),
				JobBuilder: downloader.NewJobBuilder("/var/lib/isoboot", "alpine:3.23"),
			}

			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: testNN})
			Expect(err).NotTo(HaveOccurred())

			updated := &isobootv1alpha1.BootSource{}
			Expect(k8sClient.Get(ctx, testNN, updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(isobootv1alpha1.PhasePending))
		})
	})
})
