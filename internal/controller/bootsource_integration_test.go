package controller

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	isobootv1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
)

// mockHTTPServer holds the test server and generated file content/hashes.
type mockHTTPServer struct {
	Server *httptest.Server
	Client *http.Client

	// File content
	KernelContent   []byte
	InitrdContent   []byte
	FirmwareContent []byte
	ISOContent      []byte

	// Computed hashes
	KernelSHA256   string
	InitrdSHA256   string
	FirmwareSHA256 string
	ISOSHA256      string

	// Mutable state for error injection
	mu                sync.RWMutex
	failKernel        bool
	failInitrd        bool
	failFirmware      bool
	failISO           bool
	failChecksumURL   bool
	returnWrongHash   bool
	httpStatusCode    int
	kernelDownloads   int
	initrdDownloads   int
	firmwareDownloads int
}

// newMockHTTPServer creates a mock HTTPS server with fake boot resources.
func newMockHTTPServer() *mockHTTPServer {
	m := &mockHTTPServer{
		// Generate fake file content (~1KB each)
		KernelContent:   append([]byte("KERNEL:"), make([]byte, 1024)...),
		InitrdContent:   append([]byte("INITRD:"), make([]byte, 1024)...),
		FirmwareContent: append([]byte("FIRMWARE:"), make([]byte, 1024)...),
	}

	// Fill with recognizable patterns
	for i := range m.KernelContent[7:] {
		m.KernelContent[7+i] = byte(i % 256)
	}
	for i := range m.InitrdContent[7:] {
		m.InitrdContent[7+i] = byte((i + 50) % 256)
	}
	for i := range m.FirmwareContent[9:] {
		m.FirmwareContent[9+i] = byte((i + 100) % 256)
	}

	// Create a minimal ISO with kernel and initrd
	m.ISOContent = createTestISOWithPaths("/linux", "/initrd.gz")

	// Compute hashes
	m.KernelSHA256 = sha256sum(m.KernelContent)
	m.InitrdSHA256 = sha256sum(m.InitrdContent)
	m.FirmwareSHA256 = sha256sum(m.FirmwareContent)
	m.ISOSHA256 = sha256sum(m.ISOContent)

	mux := http.NewServeMux()

	// Serve kernel
	mux.HandleFunc("/kernel", func(w http.ResponseWriter, _ *http.Request) {
		m.mu.Lock()
		m.kernelDownloads++
		fail := m.failKernel
		status := m.httpStatusCode
		m.mu.Unlock()

		if fail {
			if status == 0 {
				status = http.StatusNotFound
			}
			http.Error(w, "not found", status)
			return
		}
		_, _ = w.Write(m.KernelContent)
	})

	// Serve initrd
	mux.HandleFunc("/initrd", func(w http.ResponseWriter, _ *http.Request) {
		m.mu.Lock()
		m.initrdDownloads++
		fail := m.failInitrd
		status := m.httpStatusCode
		m.mu.Unlock()

		if fail {
			if status == 0 {
				status = http.StatusNotFound
			}
			http.Error(w, "not found", status)
			return
		}
		_, _ = w.Write(m.InitrdContent)
	})

	// Serve firmware
	mux.HandleFunc("/firmware", func(w http.ResponseWriter, _ *http.Request) {
		m.mu.Lock()
		m.firmwareDownloads++
		fail := m.failFirmware
		status := m.httpStatusCode
		m.mu.Unlock()

		if fail {
			if status == 0 {
				status = http.StatusNotFound
			}
			http.Error(w, "not found", status)
			return
		}
		_, _ = w.Write(m.FirmwareContent)
	})

	// Serve ISO
	mux.HandleFunc("/boot.iso", func(w http.ResponseWriter, _ *http.Request) {
		m.mu.RLock()
		fail := m.failISO
		status := m.httpStatusCode
		m.mu.RUnlock()

		if fail {
			if status == 0 {
				status = http.StatusNotFound
			}
			http.Error(w, "not found", status)
			return
		}
		_, _ = w.Write(m.ISOContent)
	})

	// Serve SHA256SUMS checksum file
	mux.HandleFunc("/SHA256SUMS", func(w http.ResponseWriter, _ *http.Request) {
		m.mu.RLock()
		fail := m.failChecksumURL
		wrongHash := m.returnWrongHash
		m.mu.RUnlock()

		if fail {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		kernelHash := m.KernelSHA256
		initrdHash := m.InitrdSHA256
		firmwareHash := m.FirmwareSHA256
		isoHash := m.ISOSHA256

		if wrongHash {
			// Return wrong hashes
			kernelHash = "0000000000000000000000000000000000000000000000000000000000000000"
			initrdHash = "1111111111111111111111111111111111111111111111111111111111111111"
		}

		_, _ = fmt.Fprintf(w, "%s  kernel\n", kernelHash)
		_, _ = fmt.Fprintf(w, "%s  initrd\n", initrdHash)
		_, _ = fmt.Fprintf(w, "%s  firmware\n", firmwareHash)
		_, _ = fmt.Fprintf(w, "%s  boot.iso\n", isoHash)
	})

	m.Server = httptest.NewTLSServer(mux)
	m.Client = m.Server.Client()

	return m
}

func (m *mockHTTPServer) Close() {
	m.Server.Close()
}

func (m *mockHTTPServer) URL(path string) string {
	return m.Server.URL + path
}

func (m *mockHTTPServer) SetFailKernel(fail bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failKernel = fail
}

func (m *mockHTTPServer) SetFailInitrd(fail bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failInitrd = fail
}

func (m *mockHTTPServer) SetFailChecksumURL(fail bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failChecksumURL = fail
}

func (m *mockHTTPServer) SetReturnWrongHash(wrong bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.returnWrongHash = wrong
}

func (m *mockHTTPServer) SetHTTPStatusCode(code int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.httpStatusCode = code
}

func (m *mockHTTPServer) GetKernelDownloads() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.kernelDownloads
}

func (m *mockHTTPServer) GetInitrdDownloads() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.initrdDownloads
}

func (m *mockHTTPServer) ResetDownloadCounts() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.kernelDownloads = 0
	m.initrdDownloads = 0
	m.firmwareDownloads = 0
}

var _ = Describe("BootSource Integration", func() {
	var (
		mockServer *mockHTTPServer
		tempDir    string
		reconciler *BootSourceReconciler
		ctx        context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
		tempDir = GinkgoT().TempDir()
		mockServer = newMockHTTPServer()

		reconciler = &BootSourceReconciler{
			Client:  k8sClient,
			Scheme:  k8sClient.Scheme(),
			BaseDir: tempDir,
			Fetcher: &HTTPResourceFetcher{Client: mockServer.Client},
		}
	})

	AfterEach(func() {
		mockServer.Close()
		// Clean up test resources
		for _, name := range []string{
			"int-direct-inline-shasum",
			"int-direct-shasumurl",
			"int-iso-inline-shasum",
			"int-iso-shasumurl",
			"int-mixed-checksum",
			"int-direct-with-firmware",
			"int-download-404",
			"int-download-500",
			"int-checksum-url-404",
			"int-wrong-hash",
			"int-corrupt-kernel",
			"int-corrupt-initrd",
			"int-delete-kernel",
			"int-corrupt-firmware",
			"int-corrupt-initrd-firmware",
			"int-spec-change",
		} {
			deleteBootSource(ctx, name)
		}
	})

	// ── Checksum Verification Paths ──────────────────────────────────────

	Context("Checksum verification paths", func() {
		It("should download and verify with inline shasum (direct mode)", func() {
			Expect(createBootSource(ctx, "int-direct-inline-shasum", isobootv1alpha1.BootSourceSpec{
				Kernel: &isobootv1alpha1.DownloadableResource{
					URL:    mockServer.URL("/kernel"),
					Shasum: ptr.To(mockServer.KernelSHA256),
				},
				Initrd: &isobootv1alpha1.DownloadableResource{
					URL:    mockServer.URL("/initrd"),
					Shasum: ptr.To(mockServer.InitrdSHA256),
				},
			})).To(Succeed())

			// First reconcile adds finalizer
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "int-direct-inline-shasum", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile downloads and verifies
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "int-direct-inline-shasum", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify status
			var bs isobootv1alpha1.BootSource
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "int-direct-inline-shasum", Namespace: "default"}, &bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseReady))
			Expect(bs.Status.Resources["kernel"].Shasum).To(Equal(mockServer.KernelSHA256))
			Expect(bs.Status.Resources["initrd"].Shasum).To(Equal(mockServer.InitrdSHA256))

			// Verify files
			kernelPath := filepath.Join(tempDir, "default", "int-direct-inline-shasum", "kernel")
			Expect(kernelPath).To(BeAnExistingFile())
			content, err := os.ReadFile(kernelPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(content).To(Equal(mockServer.KernelContent))
		})

		It("should download and verify with shasumURL (direct mode)", func() {
			Expect(createBootSource(ctx, "int-direct-shasumurl", isobootv1alpha1.BootSourceSpec{
				Kernel: &isobootv1alpha1.DownloadableResource{
					URL:       mockServer.URL("/kernel"),
					ShasumURL: ptr.To(mockServer.URL("/SHA256SUMS")),
				},
				Initrd: &isobootv1alpha1.DownloadableResource{
					URL:       mockServer.URL("/initrd"),
					ShasumURL: ptr.To(mockServer.URL("/SHA256SUMS")),
				},
			})).To(Succeed())

			// First reconcile adds finalizer
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "int-direct-shasumurl", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile downloads and verifies
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "int-direct-shasumurl", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify status
			var bs isobootv1alpha1.BootSource
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "int-direct-shasumurl", Namespace: "default"}, &bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseReady))
		})

		It("should download and verify ISO with inline shasum", func() {
			Expect(createBootSource(ctx, "int-iso-inline-shasum", isobootv1alpha1.BootSourceSpec{
				ISO: &isobootv1alpha1.ISOSource{
					DownloadableResource: isobootv1alpha1.DownloadableResource{
						URL:    mockServer.URL("/boot.iso"),
						Shasum: ptr.To(mockServer.ISOSHA256),
					},
					KernelPath: "/linux",
					InitrdPath: "/initrd.gz",
				},
			})).To(Succeed())

			// First reconcile adds finalizer
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "int-iso-inline-shasum", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile downloads, verifies, and extracts
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "int-iso-inline-shasum", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify status
			var bs isobootv1alpha1.BootSource
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "int-iso-inline-shasum", Namespace: "default"}, &bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseReady))
			Expect(bs.Status.Resources).To(HaveKey("iso"))
			Expect(bs.Status.Resources).To(HaveKey("kernel"))
			Expect(bs.Status.Resources).To(HaveKey("initrd"))
		})

		It("should download and verify ISO with shasumURL", func() {
			Expect(createBootSource(ctx, "int-iso-shasumurl", isobootv1alpha1.BootSourceSpec{
				ISO: &isobootv1alpha1.ISOSource{
					DownloadableResource: isobootv1alpha1.DownloadableResource{
						URL:       mockServer.URL("/boot.iso"),
						ShasumURL: ptr.To(mockServer.URL("/SHA256SUMS")),
					},
					KernelPath: "/linux",
					InitrdPath: "/initrd.gz",
				},
			})).To(Succeed())

			// First reconcile adds finalizer
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "int-iso-shasumurl", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile downloads, verifies, and extracts
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "int-iso-shasumurl", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify status
			var bs isobootv1alpha1.BootSource
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "int-iso-shasumurl", Namespace: "default"}, &bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseReady))
		})

		It("should handle mixed checksum sources (kernel with shasum, initrd with shasumURL)", func() {
			Expect(createBootSource(ctx, "int-mixed-checksum", isobootv1alpha1.BootSourceSpec{
				Kernel: &isobootv1alpha1.DownloadableResource{
					URL:    mockServer.URL("/kernel"),
					Shasum: ptr.To(mockServer.KernelSHA256),
				},
				Initrd: &isobootv1alpha1.DownloadableResource{
					URL:       mockServer.URL("/initrd"),
					ShasumURL: ptr.To(mockServer.URL("/SHA256SUMS")),
				},
			})).To(Succeed())

			// First reconcile adds finalizer
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "int-mixed-checksum", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile downloads and verifies
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "int-mixed-checksum", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify status
			var bs isobootv1alpha1.BootSource
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "int-mixed-checksum", Namespace: "default"}, &bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseReady))
		})
	})

	// ── Download Tests ───────────────────────────────────────────────────

	Context("Download tests", func() {
		It("should build initrdWithFirmware for direct mode with firmware", func() {
			Expect(createBootSource(ctx, "int-direct-with-firmware", isobootv1alpha1.BootSourceSpec{
				Kernel: &isobootv1alpha1.DownloadableResource{
					URL:    mockServer.URL("/kernel"),
					Shasum: ptr.To(mockServer.KernelSHA256),
				},
				Initrd: &isobootv1alpha1.DownloadableResource{
					URL:    mockServer.URL("/initrd"),
					Shasum: ptr.To(mockServer.InitrdSHA256),
				},
				Firmware: &isobootv1alpha1.DownloadableResource{
					URL:    mockServer.URL("/firmware"),
					Shasum: ptr.To(mockServer.FirmwareSHA256),
				},
			})).To(Succeed())

			// First reconcile adds finalizer
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "int-direct-with-firmware", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile downloads and builds initrdWithFirmware
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "int-direct-with-firmware", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify status
			var bs isobootv1alpha1.BootSource
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "int-direct-with-firmware", Namespace: "default"}, &bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseReady))
			Expect(bs.Status.Resources).To(HaveKey("initrdWithFirmware"))

			// Verify initrdWithFirmware content is concatenation of initrd + firmware
			combinedPath := filepath.Join(tempDir, "default", "int-direct-with-firmware", "initrdWithFirmware")
			Expect(combinedPath).To(BeAnExistingFile())
			content, err := os.ReadFile(combinedPath)
			Expect(err).NotTo(HaveOccurred())
			expectedContent := append(mockServer.InitrdContent, mockServer.FirmwareContent...)
			Expect(content).To(Equal(expectedContent))
		})

		It("should set Failed phase on HTTP 404", func() {
			mockServer.SetFailKernel(true)

			Expect(createBootSource(ctx, "int-download-404", isobootv1alpha1.BootSourceSpec{
				Kernel: &isobootv1alpha1.DownloadableResource{
					URL:    mockServer.URL("/kernel"),
					Shasum: ptr.To(mockServer.KernelSHA256),
				},
				Initrd: &isobootv1alpha1.DownloadableResource{
					URL:    mockServer.URL("/initrd"),
					Shasum: ptr.To(mockServer.InitrdSHA256),
				},
			})).To(Succeed())

			// First reconcile adds finalizer
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "int-download-404", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile fails download
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "int-download-404", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(5 * time.Minute))

			// Verify status
			var bs isobootv1alpha1.BootSource
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "int-download-404", Namespace: "default"}, &bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseFailed))
		})

		It("should set Failed phase on HTTP 500", func() {
			mockServer.SetFailKernel(true)
			mockServer.SetHTTPStatusCode(http.StatusInternalServerError)

			Expect(createBootSource(ctx, "int-download-500", isobootv1alpha1.BootSourceSpec{
				Kernel: &isobootv1alpha1.DownloadableResource{
					URL:    mockServer.URL("/kernel"),
					Shasum: ptr.To(mockServer.KernelSHA256),
				},
				Initrd: &isobootv1alpha1.DownloadableResource{
					URL:    mockServer.URL("/initrd"),
					Shasum: ptr.To(mockServer.InitrdSHA256),
				},
			})).To(Succeed())

			// First reconcile adds finalizer
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "int-download-500", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile fails download
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "int-download-500", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(5 * time.Minute))

			// Verify status
			var bs isobootv1alpha1.BootSource
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "int-download-500", Namespace: "default"}, &bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseFailed))
		})

		It("should set Failed phase when shasumURL returns 404", func() {
			mockServer.SetFailChecksumURL(true)

			Expect(createBootSource(ctx, "int-checksum-url-404", isobootv1alpha1.BootSourceSpec{
				Kernel: &isobootv1alpha1.DownloadableResource{
					URL:       mockServer.URL("/kernel"),
					ShasumURL: ptr.To(mockServer.URL("/SHA256SUMS")),
				},
				Initrd: &isobootv1alpha1.DownloadableResource{
					URL:       mockServer.URL("/initrd"),
					ShasumURL: ptr.To(mockServer.URL("/SHA256SUMS")),
				},
			})).To(Succeed())

			// First reconcile adds finalizer
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "int-checksum-url-404", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile fails on checksum fetch
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "int-checksum-url-404", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(5 * time.Minute))

			// Verify status
			var bs isobootv1alpha1.BootSource
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "int-checksum-url-404", Namespace: "default"}, &bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseFailed))
		})

		It("should set Corrupted phase when shasumURL returns wrong hash", func() {
			mockServer.SetReturnWrongHash(true)

			Expect(createBootSource(ctx, "int-wrong-hash", isobootv1alpha1.BootSourceSpec{
				Kernel: &isobootv1alpha1.DownloadableResource{
					URL:       mockServer.URL("/kernel"),
					ShasumURL: ptr.To(mockServer.URL("/SHA256SUMS")),
				},
				Initrd: &isobootv1alpha1.DownloadableResource{
					URL:       mockServer.URL("/initrd"),
					ShasumURL: ptr.To(mockServer.URL("/SHA256SUMS")),
				},
			})).To(Succeed())

			// First reconcile adds finalizer
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "int-wrong-hash", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile downloads but verification fails
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "int-wrong-hash", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(5 * time.Minute))

			// Verify status
			var bs isobootv1alpha1.BootSource
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "int-wrong-hash", Namespace: "default"}, &bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseCorrupted))
		})
	})

	// ── Corruption Detection and Recovery Tests ──────────────────────────

	Context("Corruption detection and recovery", func() {
		It("should detect corrupted kernel file and re-download", func() {
			Expect(createBootSource(ctx, "int-corrupt-kernel", isobootv1alpha1.BootSourceSpec{
				Kernel: &isobootv1alpha1.DownloadableResource{
					URL:    mockServer.URL("/kernel"),
					Shasum: ptr.To(mockServer.KernelSHA256),
				},
				Initrd: &isobootv1alpha1.DownloadableResource{
					URL:    mockServer.URL("/initrd"),
					Shasum: ptr.To(mockServer.InitrdSHA256),
				},
			})).To(Succeed())

			// First reconcile adds finalizer
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "int-corrupt-kernel", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile downloads and reaches Ready
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "int-corrupt-kernel", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify Ready
			var bs isobootv1alpha1.BootSource
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "int-corrupt-kernel", Namespace: "default"}, &bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseReady))

			// Corrupt the kernel file
			kernelPath := filepath.Join(tempDir, "default", "int-corrupt-kernel", "kernel")
			Expect(os.WriteFile(kernelPath, []byte("CORRUPTED"), 0o644)).To(Succeed())

			// Reset download counter
			mockServer.ResetDownloadCounts()

			// Third reconcile should detect corruption and re-download
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "int-corrupt-kernel", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify kernel was re-downloaded
			Expect(mockServer.GetKernelDownloads()).To(Equal(1))

			// Verify Ready phase again
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "int-corrupt-kernel", Namespace: "default"}, &bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseReady))

			// Verify file content is correct
			content, err := os.ReadFile(kernelPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(content).To(Equal(mockServer.KernelContent))
		})

		It("should detect corrupted initrd file and re-download", func() {
			Expect(createBootSource(ctx, "int-corrupt-initrd", isobootv1alpha1.BootSourceSpec{
				Kernel: &isobootv1alpha1.DownloadableResource{
					URL:    mockServer.URL("/kernel"),
					Shasum: ptr.To(mockServer.KernelSHA256),
				},
				Initrd: &isobootv1alpha1.DownloadableResource{
					URL:    mockServer.URL("/initrd"),
					Shasum: ptr.To(mockServer.InitrdSHA256),
				},
			})).To(Succeed())

			// First reconcile adds finalizer
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "int-corrupt-initrd", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile downloads and reaches Ready
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "int-corrupt-initrd", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Corrupt the initrd file
			initrdPath := filepath.Join(tempDir, "default", "int-corrupt-initrd", "initrd")
			Expect(os.WriteFile(initrdPath, []byte("CORRUPTED"), 0o644)).To(Succeed())

			// Reset download counter
			mockServer.ResetDownloadCounts()

			// Third reconcile should detect corruption and re-download
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "int-corrupt-initrd", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify initrd was re-downloaded
			Expect(mockServer.GetInitrdDownloads()).To(Equal(1))

			// Verify Ready phase again
			var bs isobootv1alpha1.BootSource
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "int-corrupt-initrd", Namespace: "default"}, &bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseReady))
		})

		It("should re-download when kernel file is deleted", func() {
			Expect(createBootSource(ctx, "int-delete-kernel", isobootv1alpha1.BootSourceSpec{
				Kernel: &isobootv1alpha1.DownloadableResource{
					URL:    mockServer.URL("/kernel"),
					Shasum: ptr.To(mockServer.KernelSHA256),
				},
				Initrd: &isobootv1alpha1.DownloadableResource{
					URL:    mockServer.URL("/initrd"),
					Shasum: ptr.To(mockServer.InitrdSHA256),
				},
			})).To(Succeed())

			// First reconcile adds finalizer
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "int-delete-kernel", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile downloads and reaches Ready
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "int-delete-kernel", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Delete the kernel file
			kernelPath := filepath.Join(tempDir, "default", "int-delete-kernel", "kernel")
			Expect(os.Remove(kernelPath)).To(Succeed())

			// Reset download counter
			mockServer.ResetDownloadCounts()

			// Third reconcile should detect missing file and re-download
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "int-delete-kernel", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify kernel was re-downloaded
			Expect(mockServer.GetKernelDownloads()).To(Equal(1))

			// Verify file exists again
			Expect(kernelPath).To(BeAnExistingFile())

			// Verify Ready phase
			var bs isobootv1alpha1.BootSource
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "int-delete-kernel", Namespace: "default"}, &bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseReady))
		})

		It("should detect corrupted firmware and re-download", func() {
			Expect(createBootSource(ctx, "int-corrupt-firmware", isobootv1alpha1.BootSourceSpec{
				Kernel: &isobootv1alpha1.DownloadableResource{
					URL:    mockServer.URL("/kernel"),
					Shasum: ptr.To(mockServer.KernelSHA256),
				},
				Initrd: &isobootv1alpha1.DownloadableResource{
					URL:    mockServer.URL("/initrd"),
					Shasum: ptr.To(mockServer.InitrdSHA256),
				},
				Firmware: &isobootv1alpha1.DownloadableResource{
					URL:    mockServer.URL("/firmware"),
					Shasum: ptr.To(mockServer.FirmwareSHA256),
				},
			})).To(Succeed())

			// First reconcile adds finalizer
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "int-corrupt-firmware", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile downloads and builds initrdWithFirmware
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "int-corrupt-firmware", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify Ready
			var bs isobootv1alpha1.BootSource
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "int-corrupt-firmware", Namespace: "default"}, &bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseReady))

			// Corrupt the firmware file
			firmwarePath := filepath.Join(tempDir, "default", "int-corrupt-firmware", "firmware")
			Expect(os.WriteFile(firmwarePath, []byte("CORRUPTED"), 0o644)).To(Succeed())

			// Third reconcile should detect corruption and re-download
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "int-corrupt-firmware", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify Ready phase again
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "int-corrupt-firmware", Namespace: "default"}, &bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseReady))

			// Verify firmware content is correct
			content, err := os.ReadFile(firmwarePath)
			Expect(err).NotTo(HaveOccurred())
			Expect(content).To(Equal(mockServer.FirmwareContent))
		})

		It("should rebuild corrupted initrdWithFirmware", func() {
			Expect(createBootSource(ctx, "int-corrupt-initrd-firmware", isobootv1alpha1.BootSourceSpec{
				Kernel: &isobootv1alpha1.DownloadableResource{
					URL:    mockServer.URL("/kernel"),
					Shasum: ptr.To(mockServer.KernelSHA256),
				},
				Initrd: &isobootv1alpha1.DownloadableResource{
					URL:    mockServer.URL("/initrd"),
					Shasum: ptr.To(mockServer.InitrdSHA256),
				},
				Firmware: &isobootv1alpha1.DownloadableResource{
					URL:    mockServer.URL("/firmware"),
					Shasum: ptr.To(mockServer.FirmwareSHA256),
				},
			})).To(Succeed())

			// First reconcile adds finalizer
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "int-corrupt-initrd-firmware", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile downloads and builds initrdWithFirmware
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "int-corrupt-initrd-firmware", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify Ready
			var bs isobootv1alpha1.BootSource
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "int-corrupt-initrd-firmware", Namespace: "default"}, &bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseReady))

			// Corrupt the initrdWithFirmware file
			combinedPath := filepath.Join(tempDir, "default", "int-corrupt-initrd-firmware", "initrdWithFirmware")
			Expect(os.WriteFile(combinedPath, []byte("CORRUPTED"), 0o644)).To(Succeed())

			// Third reconcile should detect corruption and rebuild
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "int-corrupt-initrd-firmware", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify Ready phase again
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "int-corrupt-initrd-firmware", Namespace: "default"}, &bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseReady))

			// Verify initrdWithFirmware content is correct (initrd + firmware)
			content, err := os.ReadFile(combinedPath)
			Expect(err).NotTo(HaveOccurred())
			expectedContent := append(mockServer.InitrdContent, mockServer.FirmwareContent...)
			Expect(content).To(Equal(expectedContent))
		})
	})

	// ── Spec Change Tests ────────────────────────────────────────────────

	Context("Spec changes", func() {
		It("should update status URL when spec URL changes with same hash", func() {
			// Create BootSource with first URL
			bs := &isobootv1alpha1.BootSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "int-spec-change",
					Namespace: "default",
				},
				Spec: isobootv1alpha1.BootSourceSpec{
					Kernel: &isobootv1alpha1.DownloadableResource{
						URL:    mockServer.URL("/kernel"),
						Shasum: ptr.To(mockServer.KernelSHA256),
					},
					Initrd: &isobootv1alpha1.DownloadableResource{
						URL:    mockServer.URL("/initrd"),
						Shasum: ptr.To(mockServer.InitrdSHA256),
					},
				},
			}
			Expect(k8sClient.Create(ctx, bs)).To(Succeed())

			// First reconcile adds finalizer
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "int-spec-change", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile downloads resources
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "int-spec-change", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify Ready
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "int-spec-change", Namespace: "default"}, bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseReady))
			originalKernelURL := bs.Status.Resources["kernel"].URL

			// Update spec with new kernel URL (same content/hash, different URL)
			bs.Spec.Kernel.URL = mockServer.URL("/kernel") + "?v=2"
			Expect(k8sClient.Update(ctx, bs)).To(Succeed())

			// Reset download counter
			mockServer.ResetDownloadCounts()

			// Third reconcile - file already has correct hash, so no re-download needed
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "int-spec-change", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify kernel was NOT re-downloaded (file already has correct hash)
			// This is correct behavior - no need to download identical content
			Expect(mockServer.GetKernelDownloads()).To(Equal(0))

			// Verify status URL is updated to reflect new spec URL
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "int-spec-change", Namespace: "default"}, bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseReady))
			Expect(bs.Status.Resources["kernel"].URL).NotTo(Equal(originalKernelURL))
		})

		It("should re-download when spec hash changes", func() {
			// Create BootSource
			bs := &isobootv1alpha1.BootSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "int-spec-change-hash",
					Namespace: "default",
				},
				Spec: isobootv1alpha1.BootSourceSpec{
					Kernel: &isobootv1alpha1.DownloadableResource{
						URL:    mockServer.URL("/kernel"),
						Shasum: ptr.To(mockServer.KernelSHA256),
					},
					Initrd: &isobootv1alpha1.DownloadableResource{
						URL:    mockServer.URL("/initrd"),
						Shasum: ptr.To(mockServer.InitrdSHA256),
					},
				},
			}
			Expect(k8sClient.Create(ctx, bs)).To(Succeed())
			DeferCleanup(func() {
				deleteBootSource(ctx, "int-spec-change-hash")
			})

			// First reconcile adds finalizer
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "int-spec-change-hash", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile downloads resources
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "int-spec-change-hash", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify Ready
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "int-spec-change-hash", Namespace: "default"}, bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseReady))

			// Update spec with different hash - this will cause hash mismatch
			// The existing file won't match the new expected hash, so it must be re-downloaded
			wrongHash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			bs.Spec.Kernel.Shasum = ptr.To(wrongHash)
			Expect(k8sClient.Update(ctx, bs)).To(Succeed())

			// Reset download counter
			mockServer.ResetDownloadCounts()

			// Third reconcile - hash mismatch triggers re-download (which will fail verification)
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "int-spec-change-hash", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(5 * time.Minute)) // Error requeue

			// Verify kernel was re-downloaded (because existing file had wrong hash)
			Expect(mockServer.GetKernelDownloads()).To(Equal(1))

			// Verify Corrupted phase (downloaded content doesn't match the wrong expected hash)
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "int-spec-change-hash", Namespace: "default"}, bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseCorrupted))
		})
	})
})
