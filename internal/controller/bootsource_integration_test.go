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
	isoDownloads      int
	checksumResponses map[string]string // path -> response content for custom checksum endpoints
}

// newMockHTTPServer creates a mock HTTPS server with fake boot resources.
func newMockHTTPServer() *mockHTTPServer {
	m := &mockHTTPServer{
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

	m.ISOContent = createTestISOWithPaths("/linux", "/initrd.gz")
	m.KernelSHA256 = sha256sum(m.KernelContent)
	m.InitrdSHA256 = sha256sum(m.InitrdContent)
	m.FirmwareSHA256 = sha256sum(m.FirmwareContent)
	m.ISOSHA256 = sha256sum(m.ISOContent)

	mux := http.NewServeMux()

	// Generic handler factory for resource endpoints
	serveResource := func(content *[]byte, downloads *int, fail *bool) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			m.mu.Lock()
			*downloads++
			shouldFail := *fail
			status := m.httpStatusCode
			m.mu.Unlock()

			if shouldFail {
				if status == 0 {
					status = http.StatusNotFound
				}
				http.Error(w, "not found", status)
				return
			}
			_, _ = w.Write(*content)
		}
	}

	mux.HandleFunc("/kernel", serveResource(&m.KernelContent, &m.kernelDownloads, &m.failKernel))
	mux.HandleFunc("/initrd", serveResource(&m.InitrdContent, &m.initrdDownloads, &m.failInitrd))
	mux.HandleFunc("/firmware", serveResource(&m.FirmwareContent, &m.firmwareDownloads, &m.failFirmware))
	mux.HandleFunc("/boot.iso", serveResource(&m.ISOContent, &m.isoDownloads, &m.failISO))

	// Checksum handler: serves custom responses from checksumResponses map, or default SHA256SUMS
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		m.mu.RLock()
		customResponse, hasCustom := m.checksumResponses[r.URL.Path]
		fail := m.failChecksumURL
		wrongHash := m.returnWrongHash
		m.mu.RUnlock()

		// Handle custom checksum responses
		if hasCustom {
			_, _ = w.Write([]byte(customResponse))
			return
		}

		// Handle default SHA256SUMS path
		if r.URL.Path == "/SHA256SUMS" {
			if fail {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}

			kernelHash, initrdHash := m.KernelSHA256, m.InitrdSHA256
			if wrongHash {
				kernelHash = "0000000000000000000000000000000000000000000000000000000000000000"
				initrdHash = "1111111111111111111111111111111111111111111111111111111111111111"
			}

			_, _ = fmt.Fprintf(w, "%s  kernel\n%s  initrd\n%s  firmware\n%s  boot.iso\n",
				kernelHash, initrdHash, m.FirmwareSHA256, m.ISOSHA256)
			return
		}

		http.NotFound(w, r)
	})

	m.Server = httptest.NewTLSServer(mux)
	m.Client = m.Server.Client()
	return m
}

func (m *mockHTTPServer) Close()                 { m.Server.Close() }
func (m *mockHTTPServer) URL(path string) string { return m.Server.URL + path }
func (m *mockHTTPServer) SetFailKernel(f bool)   { m.mu.Lock(); m.failKernel = f; m.mu.Unlock() }
func (m *mockHTTPServer) SetFailChecksumURL(f bool) {
	m.mu.Lock()
	m.failChecksumURL = f
	m.mu.Unlock()
}
func (m *mockHTTPServer) SetReturnWrongHash(f bool) {
	m.mu.Lock()
	m.returnWrongHash = f
	m.mu.Unlock()
}
func (m *mockHTTPServer) SetHTTPStatusCode(c int) { m.mu.Lock(); m.httpStatusCode = c; m.mu.Unlock() }
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
func (m *mockHTTPServer) GetFirmwareDownloads() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.firmwareDownloads
}
func (m *mockHTTPServer) GetISODownloads() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.isoDownloads
}
func (m *mockHTTPServer) SetFailInitrd(f bool)   { m.mu.Lock(); m.failInitrd = f; m.mu.Unlock() }
func (m *mockHTTPServer) SetFailFirmware(f bool) { m.mu.Lock(); m.failFirmware = f; m.mu.Unlock() }
func (m *mockHTTPServer) SetFailISO(f bool)      { m.mu.Lock(); m.failISO = f; m.mu.Unlock() }
func (m *mockHTTPServer) SetChecksumResponse(path, content string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.checksumResponses == nil {
		m.checksumResponses = make(map[string]string)
	}
	m.checksumResponses[path] = content
}
func (m *mockHTTPServer) ClearChecksumResponses() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checksumResponses = nil
}

func (m *mockHTTPServer) ResetDownloadCounts() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.kernelDownloads, m.initrdDownloads, m.firmwareDownloads, m.isoDownloads = 0, 0, 0, 0
}

// directSpec returns a kernel+initrd spec with inline shasum.
func (m *mockHTTPServer) directSpec() isobootv1alpha1.BootSourceSpec {
	return isobootv1alpha1.BootSourceSpec{
		Kernel: &isobootv1alpha1.DownloadableResource{URL: m.URL("/kernel"), Shasum: ptr.To(m.KernelSHA256)},
		Initrd: &isobootv1alpha1.DownloadableResource{URL: m.URL("/initrd"), Shasum: ptr.To(m.InitrdSHA256)},
	}
}

// directSpecWithFirmware returns a kernel+initrd+firmware spec.
func (m *mockHTTPServer) directSpecWithFirmware() isobootv1alpha1.BootSourceSpec {
	spec := m.directSpec()
	spec.Firmware = &isobootv1alpha1.DownloadableResource{URL: m.URL("/firmware"), Shasum: ptr.To(m.FirmwareSHA256)}
	return spec
}

// isoSpec returns an ISO spec with inline shasum.
func (m *mockHTTPServer) isoSpec() isobootv1alpha1.BootSourceSpec {
	return isobootv1alpha1.BootSourceSpec{
		ISO: &isobootv1alpha1.ISOSource{
			DownloadableResource: isobootv1alpha1.DownloadableResource{
				URL:    m.URL("/boot.iso"),
				Shasum: ptr.To(m.ISOSHA256),
			},
			KernelPath: "/linux",
			InitrdPath: "/initrd.gz",
		},
	}
}

// isoSpecWithFirmware returns an ISO spec with firmware.
func (m *mockHTTPServer) isoSpecWithFirmware() isobootv1alpha1.BootSourceSpec {
	spec := m.isoSpec()
	spec.Firmware = &isobootv1alpha1.DownloadableResource{
		URL:    m.URL("/firmware"),
		Shasum: ptr.To(m.FirmwareSHA256),
	}
	return spec
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
	})

	// reconcileToReady creates a BootSource, reconciles it twice, and verifies Ready phase.
	reconcileToReady := func(name string, spec isobootv1alpha1.BootSourceSpec) {
		GinkgoHelper()
		Expect(createBootSource(ctx, name, spec)).To(Succeed())
		DeferCleanup(func() { deleteBootSource(ctx, name) })

		key := types.NamespacedName{Name: name, Namespace: "default"}
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())

		var bs isobootv1alpha1.BootSource
		Expect(k8sClient.Get(ctx, key, &bs)).To(Succeed())
		Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseReady))
	}

	// ── Checksum Verification Paths ──────────────────────────────────────

	DescribeTable("Checksum verification paths",
		func(name string, specFn func() isobootv1alpha1.BootSourceSpec, extraChecks func(string)) {
			spec := specFn()
			reconcileToReady(name, spec)
			if extraChecks != nil {
				extraChecks(name)
			}
		},
		Entry("direct mode with inline shasum", "int-direct-inline",
			func() isobootv1alpha1.BootSourceSpec { return mockServer.directSpec() },
			func(name string) {
				var bs isobootv1alpha1.BootSource
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, &bs)).To(Succeed())
				Expect(bs.Status.Resources["kernel"].Shasum).To(Equal(mockServer.KernelSHA256))
			}),
		Entry("direct mode with shasumURL", "int-direct-shasumurl",
			func() isobootv1alpha1.BootSourceSpec {
				return isobootv1alpha1.BootSourceSpec{
					Kernel: &isobootv1alpha1.DownloadableResource{URL: mockServer.URL("/kernel"), ShasumURL: ptr.To(mockServer.URL("/SHA256SUMS"))},
					Initrd: &isobootv1alpha1.DownloadableResource{URL: mockServer.URL("/initrd"), ShasumURL: ptr.To(mockServer.URL("/SHA256SUMS"))},
				}
			}, nil),
		Entry("ISO mode with inline shasum", "int-iso-inline",
			func() isobootv1alpha1.BootSourceSpec {
				return isobootv1alpha1.BootSourceSpec{
					ISO: &isobootv1alpha1.ISOSource{
						DownloadableResource: isobootv1alpha1.DownloadableResource{URL: mockServer.URL("/boot.iso"), Shasum: ptr.To(mockServer.ISOSHA256)},
						KernelPath:           "/linux", InitrdPath: "/initrd.gz",
					},
				}
			},
			func(name string) {
				var bs isobootv1alpha1.BootSource
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, &bs)).To(Succeed())
				Expect(bs.Status.Resources).To(HaveKey("iso"))
				Expect(bs.Status.Resources).To(HaveKey("kernel"))
				Expect(bs.Status.Resources).To(HaveKey("initrd"))
			}),
		Entry("ISO mode with shasumURL", "int-iso-shasumurl",
			func() isobootv1alpha1.BootSourceSpec {
				return isobootv1alpha1.BootSourceSpec{
					ISO: &isobootv1alpha1.ISOSource{
						DownloadableResource: isobootv1alpha1.DownloadableResource{URL: mockServer.URL("/boot.iso"), ShasumURL: ptr.To(mockServer.URL("/SHA256SUMS"))},
						KernelPath:           "/linux", InitrdPath: "/initrd.gz",
					},
				}
			}, nil),
		Entry("mixed: kernel with shasum, initrd with shasumURL", "int-mixed",
			func() isobootv1alpha1.BootSourceSpec {
				return isobootv1alpha1.BootSourceSpec{
					Kernel: &isobootv1alpha1.DownloadableResource{URL: mockServer.URL("/kernel"), Shasum: ptr.To(mockServer.KernelSHA256)},
					Initrd: &isobootv1alpha1.DownloadableResource{URL: mockServer.URL("/initrd"), ShasumURL: ptr.To(mockServer.URL("/SHA256SUMS"))},
				}
			}, nil),
	)

	// ── Download Error Tests ─────────────────────────────────────────────

	DescribeTable("Download errors",
		func(name string, setup func(), expectedPhase isobootv1alpha1.BootSourcePhase) {
			if setup != nil {
				setup()
			}
			spec := mockServer.directSpec()
			// Tests that involve shasumURL need to use shasumURL spec instead of inline shasum
			if name == "int-checksum-url-404" || name == "int-wrong-hash" {
				spec = isobootv1alpha1.BootSourceSpec{
					Kernel: &isobootv1alpha1.DownloadableResource{URL: mockServer.URL("/kernel"), ShasumURL: ptr.To(mockServer.URL("/SHA256SUMS"))},
					Initrd: &isobootv1alpha1.DownloadableResource{URL: mockServer.URL("/initrd"), ShasumURL: ptr.To(mockServer.URL("/SHA256SUMS"))},
				}
			}

			Expect(createBootSource(ctx, name, spec)).To(Succeed())
			DeferCleanup(func() { deleteBootSource(ctx, name) })

			key := types.NamespacedName{Name: name, Namespace: "default"}
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(5 * time.Minute))

			var bs isobootv1alpha1.BootSource
			Expect(k8sClient.Get(ctx, key, &bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(expectedPhase))
		},
		Entry("HTTP 404", "int-download-404",
			func() { mockServer.SetFailKernel(true) },
			isobootv1alpha1.BootSourcePhaseFailed),
		Entry("HTTP 500", "int-download-500",
			func() { mockServer.SetFailKernel(true); mockServer.SetHTTPStatusCode(http.StatusInternalServerError) },
			isobootv1alpha1.BootSourcePhaseFailed),
		Entry("shasumURL 404", "int-checksum-url-404",
			func() { mockServer.SetFailChecksumURL(true) },
			isobootv1alpha1.BootSourcePhaseFailed),
		Entry("wrong hash in shasumURL", "int-wrong-hash",
			func() { mockServer.SetReturnWrongHash(true) },
			isobootv1alpha1.BootSourcePhaseCorrupted),
	)

	// ── Corruption Detection and Recovery ────────────────────────────────

	DescribeTable("Corruption detection and recovery",
		func(name string, withFirmware bool, corruptFile string, getDownloads func() int) {
			spec := mockServer.directSpec()
			if withFirmware {
				spec = mockServer.directSpecWithFirmware()
			}
			reconcileToReady(name, spec)

			// Corrupt or delete the file
			filePath := filepath.Join(tempDir, "default", name, corruptFile)
			if corruptFile == "kernel-deleted" {
				Expect(os.Remove(filepath.Join(tempDir, "default", name, "kernel"))).To(Succeed())
				filePath = filepath.Join(tempDir, "default", name, "kernel")
			} else {
				Expect(os.WriteFile(filePath, []byte("CORRUPTED"), 0o644)).To(Succeed())
			}

			mockServer.ResetDownloadCounts()

			key := types.NamespacedName{Name: name, Namespace: "default"}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())

			// Verify recovery
			var bs isobootv1alpha1.BootSource
			Expect(k8sClient.Get(ctx, key, &bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseReady))

			if getDownloads != nil {
				Expect(getDownloads()).To(Equal(1))
			}

			// Verify file content restored (except for initrdWithFirmware which is rebuilt)
			if corruptFile != "initrdWithFirmware" && corruptFile != "kernel-deleted" {
				content, err := os.ReadFile(filePath)
				Expect(err).NotTo(HaveOccurred())
				switch corruptFile {
				case "kernel":
					Expect(content).To(Equal(mockServer.KernelContent))
				case "initrd":
					Expect(content).To(Equal(mockServer.InitrdContent))
				case "firmware":
					Expect(content).To(Equal(mockServer.FirmwareContent))
				}
			}
		},
		Entry("corrupted kernel → re-download", "int-corrupt-kernel", false, "kernel",
			func() int { return mockServer.GetKernelDownloads() }),
		Entry("corrupted initrd → re-download", "int-corrupt-initrd", false, "initrd",
			func() int { return mockServer.GetInitrdDownloads() }),
		Entry("deleted kernel → re-download", "int-delete-kernel", false, "kernel-deleted",
			func() int { return mockServer.GetKernelDownloads() }),
		Entry("corrupted firmware → re-download", "int-corrupt-firmware", true, "firmware", nil),
		Entry("corrupted initrdWithFirmware → rebuild", "int-corrupt-combined", true, "initrdWithFirmware", nil),
	)

	// ── Firmware Building ────────────────────────────────────────────────

	It("should build initrdWithFirmware with correct content", func() {
		name := "int-firmware-build"
		reconcileToReady(name, mockServer.directSpecWithFirmware())

		combinedPath := filepath.Join(tempDir, "default", name, "initrdWithFirmware")
		Expect(combinedPath).To(BeAnExistingFile())

		content, err := os.ReadFile(combinedPath)
		Expect(err).NotTo(HaveOccurred())
		Expect(content).To(Equal(append(mockServer.InitrdContent, mockServer.FirmwareContent...)))
	})

	// ── Spec Changes ─────────────────────────────────────────────────────

	Context("Spec changes", func() {
		It("should not re-download when URL changes but hash matches", func() {
			name := "int-spec-url-change"
			bs := &isobootv1alpha1.BootSource{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
				Spec:       mockServer.directSpec(),
			}
			Expect(k8sClient.Create(ctx, bs)).To(Succeed())
			DeferCleanup(func() { deleteBootSource(ctx, name) })

			key := types.NamespacedName{Name: name, Namespace: "default"}
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})

			Expect(k8sClient.Get(ctx, key, bs)).To(Succeed())
			originalURL := bs.Status.Resources["kernel"].URL

			bs.Spec.Kernel.URL = mockServer.URL("/kernel") + "?v=2"
			Expect(k8sClient.Update(ctx, bs)).To(Succeed())

			mockServer.ResetDownloadCounts()
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})

			Expect(mockServer.GetKernelDownloads()).To(Equal(0))
			Expect(k8sClient.Get(ctx, key, bs)).To(Succeed())
			Expect(bs.Status.Resources["kernel"].URL).NotTo(Equal(originalURL))
		})

		It("should re-download when hash changes", func() {
			name := "int-spec-hash-change"
			bs := &isobootv1alpha1.BootSource{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
				Spec:       mockServer.directSpec(),
			}
			Expect(k8sClient.Create(ctx, bs)).To(Succeed())
			DeferCleanup(func() { deleteBootSource(ctx, name) })

			key := types.NamespacedName{Name: name, Namespace: "default"}
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})

			Expect(k8sClient.Get(ctx, key, bs)).To(Succeed())
			bs.Spec.Kernel.Shasum = ptr.To("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
			Expect(k8sClient.Update(ctx, bs)).To(Succeed())

			mockServer.ResetDownloadCounts()
			result, _ := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})

			Expect(mockServer.GetKernelDownloads()).To(Equal(1))
			Expect(result.RequeueAfter).To(Equal(5 * time.Minute))

			Expect(k8sClient.Get(ctx, key, bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseCorrupted))
		})
	})

	// ── shasumURL Resolution Edge Cases ──────────────────────────────────

	// checksumContentFn type for generating checksum content at test time
	type checksumContentFn func(m *mockHTTPServer) string

	DescribeTable("shasumURL resolution edge cases",
		func(name string, checksumPath string, contentFn checksumContentFn, expectSuccess bool) {
			checksumContent := contentFn(mockServer)
			mockServer.SetChecksumResponse(checksumPath, checksumContent)
			DeferCleanup(func() { mockServer.ClearChecksumResponses() })

			spec := isobootv1alpha1.BootSourceSpec{
				Kernel: &isobootv1alpha1.DownloadableResource{
					URL:       mockServer.URL("/kernel"),
					ShasumURL: ptr.To(mockServer.URL(checksumPath)),
				},
				Initrd: &isobootv1alpha1.DownloadableResource{
					URL:       mockServer.URL("/initrd"),
					ShasumURL: ptr.To(mockServer.URL(checksumPath)),
				},
			}

			Expect(createBootSource(ctx, name, spec)).To(Succeed())
			DeferCleanup(func() { deleteBootSource(ctx, name) })

			key := types.NamespacedName{Name: name, Namespace: "default"}
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})

			var bs isobootv1alpha1.BootSource
			Expect(k8sClient.Get(ctx, key, &bs)).To(Succeed())

			if expectSuccess {
				Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseReady))
			} else {
				Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseFailed))
			}
		},
		Entry("hash-first format (standard)", "int-shasum-hash-first", "/SHASUMS-hash-first",
			checksumContentFn(func(m *mockHTTPServer) string {
				return fmt.Sprintf("%s  kernel\n%s  initrd\n", m.KernelSHA256, m.InitrdSHA256)
			}), true),
		Entry("filename-first format", "int-shasum-filename-first", "/SHASUMS-filename-first",
			checksumContentFn(func(m *mockHTTPServer) string {
				return fmt.Sprintf("kernel  %s\ninitrd  %s\n", m.KernelSHA256, m.InitrdSHA256)
			}), true),
		Entry("relative path with ./prefix", "int-shasum-relative", "/SHASUMS-relative",
			checksumContentFn(func(m *mockHTTPServer) string {
				return fmt.Sprintf("%s  ./kernel\n%s  ./initrd\n", m.KernelSHA256, m.InitrdSHA256)
			}), true),
		Entry("bare filename (no prefix)", "int-shasum-bare", "/SHASUMS-bare",
			checksumContentFn(func(m *mockHTTPServer) string {
				return fmt.Sprintf("%s  kernel\n%s  initrd\n", m.KernelSHA256, m.InitrdSHA256)
			}), true),
		// Note: "longest suffix fallback" is tested in checksum unit tests;
		// it requires multi-component file URLs which the mock server doesn't use.
		Entry("ambiguous suffix (should fail)", "int-shasum-ambiguous", "/SHASUMS-ambiguous",
			checksumContentFn(func(m *mockHTTPServer) string {
				// Two entries match the suffix "kernel"
				return fmt.Sprintf("%s  path1/kernel\n%s  path2/kernel\n%s  initrd\n",
					m.KernelSHA256, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", m.InitrdSHA256)
			}), false),
		Entry("SHA512 algorithm (128-char hash)", "int-shasum-sha512", "/SHA512SUMS",
			checksumContentFn(func(m *mockHTTPServer) string {
				return fmt.Sprintf("%s  kernel\n%s  initrd\n", sha512sum(m.KernelContent), sha512sum(m.InitrdContent))
			}), true),
	)

	// ── Multi-delete Recovery ────────────────────────────────────────────

	Describe("Multi-delete recovery", func() {
		Context("Direct mode", func() {
			It("should recover when initrd + initrdWithFirmware both deleted", func() {
				name := "int-multi-delete-direct-2"
				reconcileToReady(name, mockServer.directSpecWithFirmware())

				// Delete both initrd and initrdWithFirmware
				initrdPath := filepath.Join(tempDir, "default", name, "initrd")
				combinedPath := filepath.Join(tempDir, "default", name, "initrdWithFirmware")
				Expect(os.Remove(initrdPath)).To(Succeed())
				Expect(os.Remove(combinedPath)).To(Succeed())

				mockServer.ResetDownloadCounts()

				key := types.NamespacedName{Name: name, Namespace: "default"}
				_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
				Expect(err).NotTo(HaveOccurred())

				var bs isobootv1alpha1.BootSource
				Expect(k8sClient.Get(ctx, key, &bs)).To(Succeed())
				Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseReady))

				// Verify files were restored
				Expect(initrdPath).To(BeAnExistingFile())
				Expect(combinedPath).To(BeAnExistingFile())
				Expect(mockServer.GetInitrdDownloads()).To(Equal(1))
			})

			It("should recover when all three deleted (initrd + firmware + derived)", func() {
				name := "int-multi-delete-direct-3"
				reconcileToReady(name, mockServer.directSpecWithFirmware())

				// Delete initrd, firmware, and initrdWithFirmware
				initrdPath := filepath.Join(tempDir, "default", name, "initrd")
				firmwarePath := filepath.Join(tempDir, "default", name, "firmware")
				combinedPath := filepath.Join(tempDir, "default", name, "initrdWithFirmware")
				Expect(os.Remove(initrdPath)).To(Succeed())
				Expect(os.Remove(firmwarePath)).To(Succeed())
				Expect(os.Remove(combinedPath)).To(Succeed())

				mockServer.ResetDownloadCounts()

				key := types.NamespacedName{Name: name, Namespace: "default"}
				_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
				Expect(err).NotTo(HaveOccurred())

				var bs isobootv1alpha1.BootSource
				Expect(k8sClient.Get(ctx, key, &bs)).To(Succeed())
				Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseReady))

				// Verify all files restored
				Expect(initrdPath).To(BeAnExistingFile())
				Expect(firmwarePath).To(BeAnExistingFile())
				Expect(combinedPath).To(BeAnExistingFile())
				Expect(mockServer.GetInitrdDownloads()).To(Equal(1))
				Expect(mockServer.GetFirmwareDownloads()).To(Equal(1))
			})

			It("should fail derived when source download fails during recovery", func() {
				name := "int-multi-delete-fail-recovery"
				reconcileToReady(name, mockServer.directSpecWithFirmware())

				// Delete initrd and initrdWithFirmware
				initrdPath := filepath.Join(tempDir, "default", name, "initrd")
				combinedPath := filepath.Join(tempDir, "default", name, "initrdWithFirmware")
				Expect(os.Remove(initrdPath)).To(Succeed())
				Expect(os.Remove(combinedPath)).To(Succeed())

				// Make initrd download fail
				mockServer.SetFailInitrd(true)
				mockServer.ResetDownloadCounts()

				key := types.NamespacedName{Name: name, Namespace: "default"}
				_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
				Expect(err).NotTo(HaveOccurred())

				var bs isobootv1alpha1.BootSource
				Expect(k8sClient.Get(ctx, key, &bs)).To(Succeed())
				Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseFailed))
			})
		})

		Context("ISO mode", func() {
			It("should recover when extracted initrd + derived both deleted", func() {
				name := "int-multi-delete-iso-2"
				reconcileToReady(name, mockServer.isoSpecWithFirmware())

				// Delete extracted initrd and initrdWithFirmware
				initrdPath := filepath.Join(tempDir, "default", name, "initrd")
				combinedPath := filepath.Join(tempDir, "default", name, "initrdWithFirmware")
				Expect(os.Remove(initrdPath)).To(Succeed())
				Expect(os.Remove(combinedPath)).To(Succeed())

				mockServer.ResetDownloadCounts()

				key := types.NamespacedName{Name: name, Namespace: "default"}
				_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
				Expect(err).NotTo(HaveOccurred())

				var bs isobootv1alpha1.BootSource
				Expect(k8sClient.Get(ctx, key, &bs)).To(Succeed())
				Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseReady))

				// Verify files restored
				Expect(initrdPath).To(BeAnExistingFile())
				Expect(combinedPath).To(BeAnExistingFile())
				// ISO should not be re-downloaded (still exists)
				Expect(mockServer.GetISODownloads()).To(Equal(0))
			})

			It("should full-recover when ISO + firmware + derived all deleted", func() {
				name := "int-multi-delete-iso-full"
				reconcileToReady(name, mockServer.isoSpecWithFirmware())

				// Delete everything
				isoPath := filepath.Join(tempDir, "default", name, "iso")
				kernelPath := filepath.Join(tempDir, "default", name, "kernel")
				initrdPath := filepath.Join(tempDir, "default", name, "initrd")
				firmwarePath := filepath.Join(tempDir, "default", name, "firmware")
				combinedPath := filepath.Join(tempDir, "default", name, "initrdWithFirmware")
				Expect(os.Remove(isoPath)).To(Succeed())
				Expect(os.Remove(kernelPath)).To(Succeed())
				Expect(os.Remove(initrdPath)).To(Succeed())
				Expect(os.Remove(firmwarePath)).To(Succeed())
				Expect(os.Remove(combinedPath)).To(Succeed())

				mockServer.ResetDownloadCounts()

				key := types.NamespacedName{Name: name, Namespace: "default"}
				_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
				Expect(err).NotTo(HaveOccurred())

				var bs isobootv1alpha1.BootSource
				Expect(k8sClient.Get(ctx, key, &bs)).To(Succeed())
				Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseReady))

				// Verify all files restored
				Expect(isoPath).To(BeAnExistingFile())
				Expect(kernelPath).To(BeAnExistingFile())
				Expect(initrdPath).To(BeAnExistingFile())
				Expect(firmwarePath).To(BeAnExistingFile())
				Expect(combinedPath).To(BeAnExistingFile())

				// ISO and firmware should be re-downloaded
				Expect(mockServer.GetISODownloads()).To(Equal(1))
				Expect(mockServer.GetFirmwareDownloads()).To(Equal(1))
			})
		})
	})

	// ── Idempotent Behavior ──────────────────────────────────────────────

	Context("Idempotent behavior", func() {
		It("should skip download when files pre-exist with correct hash", func() {
			name := "int-idempotent-preexist"

			// Pre-create directory and files before creating BootSource
			resourceDir := filepath.Join(tempDir, "default", name)
			Expect(os.MkdirAll(resourceDir, 0o755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(resourceDir, "kernel"), mockServer.KernelContent, 0o644)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(resourceDir, "initrd"), mockServer.InitrdContent, 0o644)).To(Succeed())

			mockServer.ResetDownloadCounts()

			spec := mockServer.directSpec()
			Expect(createBootSource(ctx, name, spec)).To(Succeed())
			DeferCleanup(func() { deleteBootSource(ctx, name) })

			key := types.NamespacedName{Name: name, Namespace: "default"}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())
			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())

			var bs isobootv1alpha1.BootSource
			Expect(k8sClient.Get(ctx, key, &bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseReady))

			// No downloads should have occurred
			Expect(mockServer.GetKernelDownloads()).To(Equal(0))
			Expect(mockServer.GetInitrdDownloads()).To(Equal(0))
		})

		It("should re-download when files pre-exist with wrong hash", func() {
			name := "int-idempotent-wrong-hash"

			// Pre-create directory and files with wrong content
			resourceDir := filepath.Join(tempDir, "default", name)
			Expect(os.MkdirAll(resourceDir, 0o755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(resourceDir, "kernel"), []byte("wrong content"), 0o644)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(resourceDir, "initrd"), []byte("wrong content"), 0o644)).To(Succeed())

			mockServer.ResetDownloadCounts()

			spec := mockServer.directSpec()
			Expect(createBootSource(ctx, name, spec)).To(Succeed())
			DeferCleanup(func() { deleteBootSource(ctx, name) })

			key := types.NamespacedName{Name: name, Namespace: "default"}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())
			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())

			var bs isobootv1alpha1.BootSource
			Expect(k8sClient.Get(ctx, key, &bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseReady))

			// Both files should have been re-downloaded
			Expect(mockServer.GetKernelDownloads()).To(Equal(1))
			Expect(mockServer.GetInitrdDownloads()).To(Equal(1))
		})
	})

	// ── CR Deletion Cleanup ──────────────────────────────────────────────

	Context("CR deletion cleanup", func() {
		It("should remove resource directory on CR deletion", func() {
			name := "int-deletion-cleanup"
			reconcileToReady(name, mockServer.directSpec())

			resourceDir := filepath.Join(tempDir, "default", name)
			Expect(resourceDir).To(BeADirectory())

			// Delete the CR (DeferCleanup from reconcileToReady handles this, but we need to verify)
			key := types.NamespacedName{Name: name, Namespace: "default"}
			var bs isobootv1alpha1.BootSource
			Expect(k8sClient.Get(ctx, key, &bs)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &bs)).To(Succeed())

			// Reconcile to trigger cleanup
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())

			// Directory should be removed
			Expect(resourceDir).NotTo(BeADirectory())
		})

		It("should handle already-removed directory gracefully", func() {
			name := "int-deletion-already-removed"
			reconcileToReady(name, mockServer.directSpec())

			resourceDir := filepath.Join(tempDir, "default", name)

			// Manually remove the directory first
			Expect(os.RemoveAll(resourceDir)).To(Succeed())
			Expect(resourceDir).NotTo(BeADirectory())

			// Delete the CR
			key := types.NamespacedName{Name: name, Namespace: "default"}
			var bs isobootv1alpha1.BootSource
			Expect(k8sClient.Get(ctx, key, &bs)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &bs)).To(Succeed())

			// Reconcile should not error even though directory is already gone
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
