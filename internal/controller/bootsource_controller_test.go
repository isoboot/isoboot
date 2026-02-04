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
	"sync/atomic"
	"time"

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

// mockDownloader is a configurable mock for testing
type mockDownloader struct {
	downloadCount atomic.Int32
	blockChan     chan struct{}
	returnErr     error
}

func newMockDownloader() *mockDownloader {
	return &mockDownloader{
		blockChan: make(chan struct{}),
	}
}

func (m *mockDownloader) Download(ctx context.Context, bootSource *isobootv1alpha1.BootSource) error {
	m.downloadCount.Add(1)
	select {
	case <-m.blockChan:
		return m.returnErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *mockDownloader) Complete() {
	close(m.blockChan)
}

// errorClient wraps a client and can inject errors for specific operations
type errorClient struct {
	client.Client
	getErr          error
	statusUpdateErr error
}

func (e *errorClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if e.getErr != nil {
		return e.getErr
	}
	return e.Client.Get(ctx, key, obj, opts...)
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
	if err := isobootv1alpha1.AddToScheme(scheme); err != nil {
		panic(err)
	}
	return scheme
}

// newFakeReconciler creates a reconciler with a fake client for unit testing
func newFakeReconciler(downloader Downloader, objs ...client.Object) *BootSourceReconciler {
	scheme := testScheme()
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&isobootv1alpha1.BootSource{}).
		Build()

	return &BootSourceReconciler{
		Client:          fakeClient,
		Scheme:          scheme,
		DownloadManager: NewDownloadManager(context.Background(), fakeClient, downloader),
	}
}

// newErrorReconciler creates a reconciler with an error-injecting client
func newErrorReconciler(errCfg errorClient, downloader Downloader, objs ...client.Object) *BootSourceReconciler {
	scheme := testScheme()
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&isobootv1alpha1.BootSource{}).
		Build()

	errCfg.Client = fakeClient
	return &BootSourceReconciler{
		Client:          &errCfg,
		Scheme:          scheme,
		DownloadManager: NewDownloadManager(context.Background(), fakeClient, downloader),
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
			downloader := newMockDownloader()
			reconciler := newFakeReconciler(downloader, newTestBootSource(testName, testNamespace))

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			updated, err := getBootSource(ctx, reconciler, testName, testNamespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Status.Phase).To(Equal(isobootv1alpha1.PhasePending))
		})

		It("should not change phase if already set to terminal state", func() {
			downloader := newMockDownloader()
			bootSource := newTestBootSource(testName, testNamespace)
			bootSource.Status.Phase = isobootv1alpha1.PhaseReady
			reconciler := newFakeReconciler(downloader, bootSource)

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			updated, err := getBootSource(ctx, reconciler, testName, testNamespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Status.Phase).To(Equal(isobootv1alpha1.PhaseReady))
		})

		It("should handle not found resources gracefully", func() {
			downloader := newMockDownloader()
			reconciler := newFakeReconciler(downloader)
			_, err := reconciler.Reconcile(ctx, reconcileRequest("nonexistent", testNamespace))
			Expect(err).NotTo(HaveOccurred())
		})

		It("should handle multiple BootSources independently", func() {
			downloader := newMockDownloader()
			first := newTestBootSource("bootsource-one", "namespace-one")
			second := newTestBootSource("bootsource-two", "namespace-two")
			reconciler := newFakeReconciler(downloader, first, second)

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
			downloader := newMockDownloader()
			reconciler := newFakeReconciler(downloader, newTestBootSourceISO(testName, testNamespace))

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			updated, err := getBootSource(ctx, reconciler, testName, testNamespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Status.Phase).To(Equal(isobootv1alpha1.PhasePending))
		})
	})

	Context("Pending phase", func() {
		It("should start download and transition to Downloading", func() {
			downloader := newMockDownloader()
			bootSource := newTestBootSource(testName, testNamespace)
			bootSource.Status.Phase = isobootv1alpha1.PhasePending
			reconciler := newFakeReconciler(downloader, bootSource)

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			updated, err := getBootSource(ctx, reconciler, testName, testNamespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Status.Phase).To(Equal(isobootv1alpha1.PhaseDownloading))
			Expect(updated.Status.Message).To(Equal("Download started"))

			// Wait for goroutine to start
			Eventually(func() int32 {
				return downloader.downloadCount.Load()
			}, 5*time.Second, 100*time.Millisecond).Should(Equal(int32(1)))
		})

		It("should not start duplicate downloads (idempotency)", func() {
			downloader := newMockDownloader()
			bootSource := newTestBootSource(testName, testNamespace)
			bootSource.Status.Phase = isobootv1alpha1.PhasePending
			reconciler := newFakeReconciler(downloader, bootSource)

			// First reconcile starts download
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Manually set back to Pending to simulate re-reconcile
			updated, _ := getBootSource(ctx, reconciler, testName, testNamespace)
			updated.Status.Phase = isobootv1alpha1.PhasePending
			Expect(reconciler.Status().Update(ctx, updated)).To(Succeed())

			// Second reconcile should not start another download
			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Only one download should have been started
			Expect(downloader.downloadCount.Load()).To(Equal(int32(1)))
		})
	})

	Context("Downloading phase", func() {
		It("should stay in Downloading while download is in progress", func() {
			downloader := newMockDownloader()
			bootSource := newTestBootSource(testName, testNamespace)
			bootSource.Status.Phase = isobootv1alpha1.PhasePending
			reconciler := newFakeReconciler(downloader, bootSource)

			// Start download
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Reconcile again while download is in progress
			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			updated, err := getBootSource(ctx, reconciler, testName, testNamespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Status.Phase).To(Equal(isobootv1alpha1.PhaseDownloading))
		})

		It("should transition to Verifying when download completes", func() {
			downloader := newMockDownloader()
			bootSource := newTestBootSource(testName, testNamespace)
			bootSource.Status.Phase = isobootv1alpha1.PhasePending
			reconciler := newFakeReconciler(downloader, bootSource)

			// Start download
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Complete download
			downloader.Complete()

			// Wait for goroutine to update status
			Eventually(func() isobootv1alpha1.BootSourcePhase {
				updated, _ := getBootSource(ctx, reconciler, testName, testNamespace)
				return updated.Status.Phase
			}, 5*time.Second, 100*time.Millisecond).Should(Equal(isobootv1alpha1.PhaseVerifying))

			updated, err := getBootSource(ctx, reconciler, testName, testNamespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Status.Message).To(Equal("Download completed, verifying"))
		})

		It("should transition to Failed when download fails", func() {
			downloader := newMockDownloader()
			downloader.returnErr = fmt.Errorf("network error")
			bootSource := newTestBootSource(testName, testNamespace)
			bootSource.Status.Phase = isobootv1alpha1.PhasePending
			reconciler := newFakeReconciler(downloader, bootSource)

			// Start download
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Complete download with error
			downloader.Complete()

			// Wait for goroutine to update status
			Eventually(func() isobootv1alpha1.BootSourcePhase {
				updated, _ := getBootSource(ctx, reconciler, testName, testNamespace)
				return updated.Status.Phase
			}, 5*time.Second, 100*time.Millisecond).Should(Equal(isobootv1alpha1.PhaseFailed))

			updated, err := getBootSource(ctx, reconciler, testName, testNamespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Status.Message).To(ContainSubstring("network error"))
		})

		It("should return to Pending if download is not tracked (controller restart)", func() {
			downloader := newMockDownloader()
			bootSource := newTestBootSource(testName, testNamespace)
			bootSource.Status.Phase = isobootv1alpha1.PhaseDownloading
			bootSource.Status.Message = "Download started"
			// Note: No download is actually in progress in the manager
			reconciler := newFakeReconciler(downloader, bootSource)

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			updated, err := getBootSource(ctx, reconciler, testName, testNamespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Status.Phase).To(Equal(isobootv1alpha1.PhasePending))
			Expect(updated.Status.Message).To(Equal("Download interrupted, retrying"))
		})
	})

	Context("Error handling", func() {
		DescribeTable("should return appropriate errors",
			func(errCfg errorClient, bootSource *isobootv1alpha1.BootSource, expectedErrSubstring string) {
				downloader := newMockDownloader()
				var objs []client.Object
				if bootSource != nil {
					objs = append(objs, bootSource)
				}
				reconciler := newErrorReconciler(errCfg, downloader, objs...)

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

	Context("DownloadManager", func() {
		It("should track in-flight downloads", func() {
			downloader := newMockDownloader()
			scheme := testScheme()
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithStatusSubresource(&isobootv1alpha1.BootSource{}).
				Build()

			manager := NewDownloadManager(context.Background(), fakeClient, downloader)
			bootSource := newTestBootSource(testName, testNamespace)
			bootSource.UID = "test-uid"

			Expect(manager.IsDownloading(bootSource.UID)).To(BeFalse())

			manager.StartDownload(bootSource)
			Expect(manager.IsDownloading(bootSource.UID)).To(BeTrue())

			// Wait for goroutine to start
			Eventually(func() int32 {
				return downloader.downloadCount.Load()
			}, 5*time.Second, 100*time.Millisecond).Should(Equal(int32(1)))

			// Starting again should not create duplicate
			manager.StartDownload(bootSource)
			// Give time for potential duplicate to start (it shouldn't)
			time.Sleep(100 * time.Millisecond)
			Expect(downloader.downloadCount.Load()).To(Equal(int32(1)))

			// Complete and verify cleanup
			downloader.Complete()
			Eventually(func() bool {
				return manager.IsDownloading(bootSource.UID)
			}, 5*time.Second, 100*time.Millisecond).Should(BeFalse())
		})

		It("should cancel downloads", func() {
			downloader := newMockDownloader()
			scheme := testScheme()
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithStatusSubresource(&isobootv1alpha1.BootSource{}).
				Build()

			manager := NewDownloadManager(context.Background(), fakeClient, downloader)
			bootSource := newTestBootSource(testName, testNamespace)
			bootSource.UID = "test-uid"

			manager.StartDownload(bootSource)
			Expect(manager.IsDownloading(bootSource.UID)).To(BeTrue())

			manager.CancelDownload(bootSource.UID)
			Expect(manager.IsDownloading(bootSource.UID)).To(BeFalse())
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
			downloader := newMockDownloader()
			reconciler := &BootSourceReconciler{
				Client:          k8sClient,
				Scheme:          k8sClient.Scheme(),
				DownloadManager: NewDownloadManager(ctx, k8sClient, downloader),
			}

			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			updated := &isobootv1alpha1.BootSource{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(isobootv1alpha1.PhasePending))
		})
	})
})
