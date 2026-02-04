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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	isobootv1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
)

// errorClient wraps a client and can inject errors for specific operations
type errorClient struct {
	client.Client
	getErr          error
	createErr       error
	statusUpdateErr error
}

func (e *errorClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if e.getErr != nil {
		return e.getErr
	}
	return e.Client.Get(ctx, key, obj, opts...)
}

func (e *errorClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if e.createErr != nil {
		return e.createErr
	}
	return e.Client.Create(ctx, obj, opts...)
}

func (e *errorClient) Status() client.StatusWriter {
	return &errorStatusWriter{StatusWriter: e.Client.Status(), err: e.statusUpdateErr}
}

type errorStatusWriter struct {
	client.StatusWriter
	err error
}

func (e *errorStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	if e.err != nil {
		return e.err
	}
	return e.StatusWriter.Update(ctx, obj, opts...)
}

// testScheme returns a scheme with all required types registered
func testScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = isobootv1alpha1.AddToScheme(scheme)
	_ = batchv1.AddToScheme(scheme)
	return scheme
}

// newFakeReconciler creates a reconciler with a fake client for unit testing
func newFakeReconciler(objs ...client.Object) *BootSourceReconciler {
	scheme := testScheme()
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&isobootv1alpha1.BootSource{}, &batchv1.Job{}).
		Build()

	return &BootSourceReconciler{
		Client:     fakeClient,
		Scheme:     scheme,
		JobBuilder: &DefaultJobBuilder{},
	}
}

// newErrorReconciler creates a reconciler with an error-injecting client
func newErrorReconciler(errCfg errorClient, objs ...client.Object) *BootSourceReconciler {
	scheme := testScheme()
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&isobootv1alpha1.BootSource{}, &batchv1.Job{}).
		Build()

	errCfg.Client = fakeClient
	return &BootSourceReconciler{
		Client:     &errCfg,
		Scheme:     scheme,
		JobBuilder: &DefaultJobBuilder{},
	}
}

// reconcileRequest creates a reconcile request for the test resource
func reconcileRequest(name, namespace string) reconcile.Request {
	return reconcile.Request{
		NamespacedName: types.NamespacedName{Name: name, Namespace: namespace},
	}
}

// getBootSource fetches a BootSource from the reconciler's client
func getBootSource(ctx context.Context, r *BootSourceReconciler, name, namespace string) (*isobootv1alpha1.BootSource, error) {
	bootSource := &isobootv1alpha1.BootSource{}
	err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, bootSource)
	return bootSource, err
}

var _ = Describe("BootSource Controller", func() {
	const (
		testName      = "test-bootsource"
		testNamespace = "default"
	)

	var (
		ctx context.Context
		req reconcile.Request
	)

	BeforeEach(func() {
		ctx = context.Background()
		req = reconcileRequest(testName, testNamespace)
	})

	Context("Phase transitions", func() {
		It("should set phase to Pending for new resources", func() {
			reconciler := newFakeReconciler(newTestBootSource(testName, testNamespace))

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			updated, err := getBootSource(ctx, reconciler, testName, testNamespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Status.Phase).To(Equal(isobootv1alpha1.PhasePending))
		})

		It("should not change phase if already set to terminal state", func() {
			bootSource := newTestBootSource(testName, testNamespace)
			bootSource.Status.Phase = isobootv1alpha1.PhaseReady
			reconciler := newFakeReconciler(bootSource)

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			updated, err := getBootSource(ctx, reconciler, testName, testNamespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Status.Phase).To(Equal(isobootv1alpha1.PhaseReady))
		})

		It("should handle not found resources gracefully", func() {
			reconciler := newFakeReconciler()
			_, err := reconciler.Reconcile(ctx, reconcileRequest("nonexistent", testNamespace))
			Expect(err).NotTo(HaveOccurred())
		})

		It("should handle multiple BootSources independently", func() {
			first := newTestBootSource("bootsource-one", "namespace-one")
			second := newTestBootSource("bootsource-two", "namespace-two")
			reconciler := newFakeReconciler(first, second)

			// Reconcile first
			_, err := reconciler.Reconcile(ctx, reconcileRequest("bootsource-one", "namespace-one"))
			Expect(err).NotTo(HaveOccurred())

			// Reconcile second
			_, err = reconciler.Reconcile(ctx, reconcileRequest("bootsource-two", "namespace-two"))
			Expect(err).NotTo(HaveOccurred())

			// Verify both have Pending phase
			updated1, err := getBootSource(ctx, reconciler, "bootsource-one", "namespace-one")
			Expect(err).NotTo(HaveOccurred())
			Expect(updated1.Status.Phase).To(Equal(isobootv1alpha1.PhasePending))

			updated2, err := getBootSource(ctx, reconciler, "bootsource-two", "namespace-two")
			Expect(err).NotTo(HaveOccurred())
			Expect(updated2.Status.Phase).To(Equal(isobootv1alpha1.PhasePending))
		})

		It("should work with ISO-based BootSource", func() {
			reconciler := newFakeReconciler(newTestBootSourceISO(testName, testNamespace))

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			updated, err := getBootSource(ctx, reconciler, testName, testNamespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Status.Phase).To(Equal(isobootv1alpha1.PhasePending))
		})
	})

	Context("Pending phase", func() {
		It("should create Job and transition to Downloading", func() {
			bootSource := newTestBootSource(testName, testNamespace)
			bootSource.Status.Phase = isobootv1alpha1.PhasePending
			reconciler := newFakeReconciler(bootSource)

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			updated, err := getBootSource(ctx, reconciler, testName, testNamespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Status.Phase).To(Equal(isobootv1alpha1.PhaseDownloading))
			Expect(updated.Status.DownloadJobName).To(HavePrefix(testName + "-download-"))
			Expect(updated.Status.Message).To(Equal("Download job created"))

			// Verify Job was created using name from status
			job := &batchv1.Job{}
			err = reconciler.Get(ctx, types.NamespacedName{Name: updated.Status.DownloadJobName, Namespace: testNamespace}, job)
			Expect(err).NotTo(HaveOccurred())
			Expect(job.Spec.Template.Spec.Containers[0].Image).To(Equal("busybox:latest"))
		})
	})

	Context("Downloading phase", func() {
		newDownloadingBootSource := func() *isobootv1alpha1.BootSource {
			bootSource := newTestBootSource(testName, testNamespace)
			bootSource.Status.Phase = isobootv1alpha1.PhaseDownloading
			bootSource.Status.DownloadJobName = testName + "-download"
			return bootSource
		}

		newJob := func(succeeded, failed, active int32) *batchv1.Job {
			return &batchv1.Job{
				ObjectMeta: batchv1.Job{}.ObjectMeta,
				Status: batchv1.JobStatus{
					Succeeded: succeeded,
					Failed:    failed,
					Active:    active,
				},
			}
		}

		BeforeEach(func() {
			// Override newJob to set name/namespace
			newJob = func(succeeded, failed, active int32) *batchv1.Job {
				job := &batchv1.Job{}
				job.Name = testName + "-download"
				job.Namespace = testNamespace
				job.Status.Succeeded = succeeded
				job.Status.Failed = failed
				job.Status.Active = active
				return job
			}
		})

		It("should transition to Verifying when Job succeeds", func() {
			reconciler := newFakeReconciler(newDownloadingBootSource(), newJob(1, 0, 0))

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			updated, err := getBootSource(ctx, reconciler, testName, testNamespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Status.Phase).To(Equal(isobootv1alpha1.PhaseVerifying))
			Expect(updated.Status.Message).To(Equal("Download completed, verifying"))
		})

		It("should transition to Failed when Job fails", func() {
			reconciler := newFakeReconciler(newDownloadingBootSource(), newJob(0, 1, 0))

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			updated, err := getBootSource(ctx, reconciler, testName, testNamespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Status.Phase).To(Equal(isobootv1alpha1.PhaseFailed))
			Expect(updated.Status.Message).To(Equal("Download job failed"))
		})

		It("should stay in Downloading while Job is running", func() {
			reconciler := newFakeReconciler(newDownloadingBootSource(), newJob(0, 0, 1))

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			updated, err := getBootSource(ctx, reconciler, testName, testNamespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Status.Phase).To(Equal(isobootv1alpha1.PhaseDownloading))
		})

		It("should return to Pending if Job not found", func() {
			reconciler := newFakeReconciler(newDownloadingBootSource())

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			updated, err := getBootSource(ctx, reconciler, testName, testNamespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Status.Phase).To(Equal(isobootv1alpha1.PhasePending))
			Expect(updated.Status.DownloadJobName).To(BeEmpty())
		})

		It("should return to Pending if DownloadJobName is empty", func() {
			bootSource := newTestBootSource(testName, testNamespace)
			bootSource.Status.Phase = isobootv1alpha1.PhaseDownloading
			bootSource.Status.DownloadJobName = ""
			reconciler := newFakeReconciler(bootSource)

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			updated, err := getBootSource(ctx, reconciler, testName, testNamespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Status.Phase).To(Equal(isobootv1alpha1.PhasePending))
			Expect(updated.Status.Message).To(Equal("No download job found, retrying"))
		})
	})

	Context("Error handling", func() {
		DescribeTable("should return appropriate errors",
			func(errCfg errorClient, bootSource *isobootv1alpha1.BootSource, expectedErrSubstring string) {
				var objs []client.Object
				if bootSource != nil {
					objs = append(objs, bootSource)
				}
				reconciler := newErrorReconciler(errCfg, objs...)

				_, err := reconciler.Reconcile(ctx, req)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(expectedErrSubstring))
			},
			Entry("Get fails with non-NotFound error",
				errorClient{getErr: fmt.Errorf("connection refused")},
				nil,
				"connection refused",
			),
			Entry("status update fails for initial Pending",
				errorClient{statusUpdateErr: fmt.Errorf("status update failed")},
				newTestBootSource(testName, testNamespace),
				"status update failed",
			),
			Entry("Job creation fails",
				errorClient{createErr: fmt.Errorf("quota exceeded")},
				func() *isobootv1alpha1.BootSource {
					bootSource := newTestBootSource(testName, testNamespace)
					bootSource.Status.Phase = isobootv1alpha1.PhasePending
					return bootSource
				}(),
				"quota exceeded",
			),
			Entry("status update fails in handlePending",
				errorClient{statusUpdateErr: fmt.Errorf("status update failed")},
				func() *isobootv1alpha1.BootSource {
					bootSource := newTestBootSource(testName, testNamespace)
					bootSource.Status.Phase = isobootv1alpha1.PhasePending
					return bootSource
				}(),
				"status update failed",
			),
		)
	})

	Context("DefaultJobBuilder", func() {
		var (
			builder    *DefaultJobBuilder
			bootSource *isobootv1alpha1.BootSource
			job        *batchv1.Job
		)

		BeforeEach(func() {
			builder = &DefaultJobBuilder{}
			bootSource = newTestBootSource(testName, testNamespace)
			job = builder.Build(bootSource)
		})

		It("should build Job with correct metadata", func() {
			Expect(job.GenerateName).To(Equal(testName + "-download-"))
			Expect(job.Namespace).To(Equal(testNamespace))
			Expect(job.Labels).To(HaveKeyWithValue("app.kubernetes.io/name", "isoboot"))
			Expect(job.Labels).To(HaveKeyWithValue("app.kubernetes.io/component", "download"))
			Expect(job.Labels).To(HaveKeyWithValue("app.kubernetes.io/managed-by", "isoboot-controller"))
			Expect(job.Labels).To(HaveKeyWithValue("isoboot.github.io/bootsource", testName))
		})

		It("should configure Job spec correctly", func() {
			Expect(job.Spec.BackoffLimit).NotTo(BeNil())
			Expect(*job.Spec.BackoffLimit).To(Equal(int32(0)))
			Expect(job.Spec.Template.Spec.RestartPolicy).To(Equal(corev1.RestartPolicyNever))
		})

		It("should configure container correctly", func() {
			Expect(job.Spec.Template.Spec.Containers).To(HaveLen(1))
			container := job.Spec.Template.Spec.Containers[0]
			Expect(container.Name).To(Equal("download"))
			Expect(container.Image).To(Equal("busybox:latest"))
			Expect(container.Command).To(Equal([]string{"sleep", "10"}))
		})
	})

	Context("Integration tests with envtest", func() {
		var typeNamespacedName types.NamespacedName

		BeforeEach(func() {
			typeNamespacedName = types.NamespacedName{Name: testName, Namespace: testNamespace}

			err := k8sClient.Get(ctx, typeNamespacedName, &isobootv1alpha1.BootSource{})
			if errors.IsNotFound(err) {
				Expect(k8sClient.Create(ctx, newTestBootSource(testName, testNamespace))).To(Succeed())
			} else {
				Expect(err).NotTo(HaveOccurred())
			}
		})

		AfterEach(func() {
			resource := &isobootv1alpha1.BootSource{}
			if err := k8sClient.Get(ctx, typeNamespacedName, resource); err == nil {
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
			}
		})

		It("should reconcile successfully with real API server", func() {
			reconciler := &BootSourceReconciler{
				Client:     k8sClient,
				Scheme:     k8sClient.Scheme(),
				JobBuilder: &DefaultJobBuilder{},
			}

			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			updated := &isobootv1alpha1.BootSource{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(isobootv1alpha1.PhasePending))
		})
	})
})
