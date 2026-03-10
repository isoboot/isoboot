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
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	isobootgithubiov1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
)

const (
	validSHA256 = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	validSHA512 = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	wrongSHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
)

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func sha512Hex(data []byte) string {
	h := sha512.Sum512(data)
	return hex.EncodeToString(h[:])
}

// withTestServer starts a TLS test server and returns the server URL, its HTTP client, and a cleanup func.
func withTestServer(handler http.HandlerFunc) (serverURL string, httpClient *http.Client, cleanup func()) {
	server := httptest.NewTLSServer(handler)
	return server.URL, server.Client(), func() {
		server.Close()
	}
}

var _ = Describe("BootArtifact Controller", func() {
	Context("Validation", func() {
		ctx := context.Background()

		newArtifact := func(name string, spec isobootgithubiov1alpha1.BootArtifactSpec) *isobootgithubiov1alpha1.BootArtifact {
			return &isobootgithubiov1alpha1.BootArtifact{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
				Spec:       spec,
			}
		}

		DescribeTable("should accept valid specs",
			func(name string, spec isobootgithubiov1alpha1.BootArtifactSpec) {
				resource := newArtifact(name, spec)
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
			},
			Entry("sha256 only", "valid-sha256", isobootgithubiov1alpha1.BootArtifactSpec{URL: "https://example.com/vmlinuz", SHA256: new(validSHA256)}),
			Entry("sha512 only", "valid-sha512", isobootgithubiov1alpha1.BootArtifactSpec{URL: "https://example.com/vmlinuz", SHA512: new(validSHA512)}),
		)

		DescribeTable("should reject invalid specs",
			func(name string, spec isobootgithubiov1alpha1.BootArtifactSpec) {
				resource := newArtifact(name, spec)
				Expect(k8sClient.Create(ctx, resource)).NotTo(Succeed())
			},
			Entry("both hashes set", "both", isobootgithubiov1alpha1.BootArtifactSpec{URL: "https://example.com/f", SHA256: new(validSHA256), SHA512: new(validSHA512)}),
			Entry("no hash set", "none", isobootgithubiov1alpha1.BootArtifactSpec{URL: "https://example.com/f"}),
			Entry("short sha256", "short256", isobootgithubiov1alpha1.BootArtifactSpec{URL: "https://example.com/f", SHA256: new("abcdef")}),
			Entry("short sha512", "short512", isobootgithubiov1alpha1.BootArtifactSpec{URL: "https://example.com/f", SHA512: new("abcdef")}),
			Entry("non-hex sha256", "nonhex256", isobootgithubiov1alpha1.BootArtifactSpec{URL: "https://example.com/f", SHA256: new("zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz")}),
			Entry("non-hex sha512", "nonhex512", isobootgithubiov1alpha1.BootArtifactSpec{URL: "https://example.com/f", SHA512: new("zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz")}),
			Entry("http url", "http", isobootgithubiov1alpha1.BootArtifactSpec{URL: "http://example.com/f", SHA256: new(validSHA256)}),
			Entry("empty url", "empty", isobootgithubiov1alpha1.BootArtifactSpec{URL: "", SHA256: new(validSHA256)}),
			Entry("path traversal", "traversal", isobootgithubiov1alpha1.BootArtifactSpec{URL: "https://example.com/foo/../bar", SHA256: new(validSHA256)}),
			Entry("path traversal at end", "traversal-end", isobootgithubiov1alpha1.BootArtifactSpec{URL: "https://example.com/..", SHA256: new(validSHA256)}),
		)
	})

	Context("filenameFromURL", func() {
		DescribeTable("should extract filename",
			func(rawURL, expected string) {
				Expect(filenameFromURL(rawURL)).To(Equal(expected))
			},
			Entry("simple", "https://example.com/images/vmlinuz", "vmlinuz"),
			Entry("nested", "https://example.com/a/b/c/initrd.img", "initrd.img"),
			Entry("root", "https://example.com/", "artifact"),
			Entry("no path", "https://example.com", "artifact"),
		)
	})

	Context("Reconcile", func() {
		var (
			ctx        context.Context
			dataDir    string
			reconciler *BootArtifactReconciler
		)

		BeforeEach(func() {
			ctx = context.Background()
			var err error
			dataDir, err = os.MkdirTemp("", "isoboot-test-*")
			Expect(err).NotTo(HaveOccurred())
			reconciler = &BootArtifactReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), DataDir: dataDir}
		})

		AfterEach(func() { Expect(os.RemoveAll(dataDir)).To(Succeed()) })

		doReconcile := func(name string) (reconcile.Result, error) {
			return reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: name, Namespace: "default"},
			})
		}

		getStatus := func(name string) isobootgithubiov1alpha1.BootArtifactStatus {
			var a isobootgithubiov1alpha1.BootArtifact
			ExpectWithOffset(1, k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, &a)).To(Succeed())
			return a.Status
		}

		createArtifact := func(name, url, sha string) {
			resource := &isobootgithubiov1alpha1.BootArtifact{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
				Spec:       isobootgithubiov1alpha1.BootArtifactSpec{URL: url, SHA256: new(sha)},
			}
			ExpectWithOffset(1, k8sClient.Create(ctx, resource)).To(Succeed())
		}

		deleteArtifact := func(name string) {
			var a isobootgithubiov1alpha1.BootArtifact
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, &a); err == nil {
				_ = k8sClient.Delete(ctx, &a)
			}
		}

		It("should return without error for deleted resource", func() {
			result, err := doReconcile("nonexistent")
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))
		})

		It("should download and verify a valid artifact", func() {
			content := []byte("test kernel content")
			serverURL, httpClient, cleanup := withTestServer(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(content) })
			defer cleanup()
			reconciler.HTTPClient = httpClient

			name := "dl-valid"
			createArtifact(name, serverURL+"/vmlinuz", sha256Hex(content))
			defer deleteArtifact(name)

			result, err := doReconcile(name)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			status := getStatus(name)
			Expect(status.Phase).To(Equal(isobootgithubiov1alpha1.BootArtifactPhaseReady))
			Expect(status.FailureCount).To(Equal(int32(0)))

			data, err := os.ReadFile(filepath.Join(dataDir, "artifacts", name, "vmlinuz"))
			Expect(err).NotTo(HaveOccurred())
			Expect(data).To(Equal(content))
		})

		It("should download and verify with SHA-512", func() {
			content := []byte("sha512 content")
			serverURL, httpClient, cleanup := withTestServer(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(content) })
			defer cleanup()
			reconciler.HTTPClient = httpClient

			name := "dl-sha512"
			resource := &isobootgithubiov1alpha1.BootArtifact{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
				Spec:       isobootgithubiov1alpha1.BootArtifactSpec{URL: serverURL + "/vmlinuz", SHA512: new(sha512Hex(content))},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			defer deleteArtifact(name)

			result, err := doReconcile(name)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			status := getStatus(name)
			Expect(status.Phase).To(Equal(isobootgithubiov1alpha1.BootArtifactPhaseReady))
			Expect(status.FailureCount).To(Equal(int32(0)))
		})

		It("should set Error on hash mismatch after download", func() {
			serverURL, httpClient, cleanup := withTestServer(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("content")) })
			defer cleanup()
			reconciler.HTTPClient = httpClient

			name := "dl-bad-hash"
			createArtifact(name, serverURL+"/vmlinuz", wrongSHA256)
			defer deleteArtifact(name)

			result, err := doReconcile(name)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).NotTo(BeZero())

			status := getStatus(name)
			Expect(status.Phase).To(Equal(isobootgithubiov1alpha1.BootArtifactPhaseError))
			Expect(status.FailureCount).To(Equal(int32(1)))
			Expect(status.Message).To(ContainSubstring("hash mismatch"))
		})

		It("should set Error on HTTP 404", func() {
			serverURL, httpClient, cleanup := withTestServer(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) })
			defer cleanup()
			reconciler.HTTPClient = httpClient

			name := "dl-404"
			createArtifact(name, serverURL+"/vmlinuz", validSHA256)
			defer deleteArtifact(name)

			result, err := doReconcile(name)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).NotTo(BeZero())

			status := getStatus(name)
			Expect(status.Phase).To(Equal(isobootgithubiov1alpha1.BootArtifactPhaseError))
			Expect(status.Message).To(ContainSubstring("HTTP 404"))
		})

		It("should verify existing file and set Ready", func() {
			content := []byte("existing kernel")
			name := "existing-valid"
			filePath := filepath.Join(dataDir, "artifacts", name, "vmlinuz")
			Expect(os.MkdirAll(filepath.Dir(filePath), 0o755)).To(Succeed())
			Expect(os.WriteFile(filePath, content, 0o644)).To(Succeed())

			createArtifact(name, "https://example.com/vmlinuz", sha256Hex(content))
			defer deleteArtifact(name)

			result, err := doReconcile(name)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())
			Expect(getStatus(name).Phase).To(Equal(isobootgithubiov1alpha1.BootArtifactPhaseReady))
		})

		It("should remove bad file and re-download on hash mismatch", func() {
			content := []byte("fresh download")
			serverURL, httpClient, cleanup := withTestServer(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(content) })
			defer cleanup()
			reconciler.HTTPClient = httpClient

			name := "existing-bad"
			filePath := filepath.Join(dataDir, "artifacts", name, "vmlinuz")
			Expect(os.MkdirAll(filepath.Dir(filePath), 0o755)).To(Succeed())
			Expect(os.WriteFile(filePath, []byte("corrupted"), 0o644)).To(Succeed())

			createArtifact(name, serverURL+"/vmlinuz", sha256Hex(content))
			defer deleteArtifact(name)

			result, err := doReconcile(name)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			status := getStatus(name)
			Expect(status.Phase).To(Equal(isobootgithubiov1alpha1.BootArtifactPhaseReady))
			Expect(status.FailureCount).To(Equal(int32(0)))

			data, err := os.ReadFile(filePath)
			Expect(err).NotTo(HaveOccurred())
			Expect(data).To(Equal(content))
		})

		It("should succeed when Content-Length header is missing (chunked)", func() {
			content := []byte("streamed content")
			serverURL, httpClient, cleanup := withTestServer(func(w http.ResponseWriter, r *http.Request) {
				// Flushing before writing prevents Go from setting Content-Length.
				w.(http.Flusher).Flush()
				_, _ = w.Write(content)
			})
			defer cleanup()
			reconciler.HTTPClient = httpClient

			name := "dl-no-cl"
			createArtifact(name, serverURL+"/vmlinuz", sha256Hex(content))
			defer deleteArtifact(name)

			result, err := doReconcile(name)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			status := getStatus(name)
			Expect(status.Phase).To(Equal(isobootgithubiov1alpha1.BootArtifactPhaseReady))
		})

		It("should set Error on Content-Length size mismatch", func() {
			// Server declares Content-Length 100 but only sends 5 bytes.
			// Go's HTTP client enforces this and returns "unexpected EOF",
			// which the reconciler catches as a write error.
			serverURL, httpClient, cleanup := withTestServer(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Length", "100")
				_, _ = w.Write([]byte("short"))
			})
			defer cleanup()
			reconciler.HTTPClient = httpClient

			name := "dl-cl-mismatch"
			createArtifact(name, serverURL+"/vmlinuz", validSHA256)
			defer deleteArtifact(name)

			result, err := doReconcile(name)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).NotTo(BeZero())

			status := getStatus(name)
			Expect(status.Phase).To(Equal(isobootgithubiov1alpha1.BootArtifactPhaseError))
			Expect(status.FailureCount).To(Equal(int32(1)))
		})

		It("should increment failureCount on repeated failures", func() {
			serverURL, httpClient, cleanup := withTestServer(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) })
			defer cleanup()
			reconciler.HTTPClient = httpClient

			name := "dl-repeat"
			createArtifact(name, serverURL+"/vmlinuz", validSHA256)
			defer deleteArtifact(name)

			for i := int32(1); i <= 3; i++ {
				_, err := doReconcile(name)
				Expect(err).NotTo(HaveOccurred())
				Expect(getStatus(name).FailureCount).To(Equal(i), fmt.Sprintf("attempt %d", i))
			}
		})
	})
})
