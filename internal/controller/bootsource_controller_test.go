package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	isobootv1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
)

// mockFetcher is a test double for ResourceFetcher.
type mockFetcher struct {
	fetchContentFunc func(ctx context.Context, url string) ([]byte, error)
	downloadFunc     func(ctx context.Context, url, destPath string) error
}

func (m *mockFetcher) FetchContent(ctx context.Context, url string) ([]byte, error) {
	if m.fetchContentFunc != nil {
		return m.fetchContentFunc(ctx, url)
	}
	return nil, errors.New("FetchContent not implemented")
}

func (m *mockFetcher) Download(ctx context.Context, url, destPath string) error {
	if m.downloadFunc != nil {
		return m.downloadFunc(ctx, url, destPath)
	}
	return errors.New("Download not implemented")
}

const (
	debianNetboot    = "https://ftp.debian.org/debian/dists/trixie/main/installer-amd64/current/images"
	debianSHA256     = debianNetboot + "/SHA256SUMS"
	debianKernel     = debianNetboot + "/netboot/debian-installer/amd64/linux"
	debianInitrd     = debianNetboot + "/netboot/debian-installer/amd64/initrd.gz"
	debianMiniISO    = debianNetboot + "/netboot/mini.iso"
	debianFirmware   = "https://cdimage.debian.org/cdimage/firmware/trixie/13.3.0/firmware.cpio.gz"
	debianFwSHA512   = "https://cdimage.debian.org/cdimage/firmware/trixie/13.3.0/SHA512SUMS"
	exampleSHA256Sum = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

// validKernel returns a DownloadableResource for the Debian netboot kernel.
func validKernel() *isobootv1alpha1.DownloadableResource {
	return &isobootv1alpha1.DownloadableResource{
		URL:       debianKernel,
		ShasumURL: ptr.To(debianSHA256),
	}
}

// validInitrd returns a DownloadableResource for the Debian netboot initrd.
func validInitrd() *isobootv1alpha1.DownloadableResource {
	return &isobootv1alpha1.DownloadableResource{
		URL:       debianInitrd,
		ShasumURL: ptr.To(debianSHA256),
	}
}

// validISO returns an ISOSource for the Debian netboot mini.iso.
func validISO() *isobootv1alpha1.ISOSource {
	return &isobootv1alpha1.ISOSource{
		DownloadableResource: isobootv1alpha1.DownloadableResource{
			URL:       debianMiniISO,
			ShasumURL: ptr.To(debianSHA256),
		},
		KernelPath: "/linux",
		InitrdPath: "/initrd.gz",
	}
}

// validFirmware returns a DownloadableResource for Debian non-free firmware.
func validFirmware() *isobootv1alpha1.DownloadableResource {
	return &isobootv1alpha1.DownloadableResource{
		URL:       debianFirmware,
		ShasumURL: ptr.To(debianFwSHA512),
	}
}

// createBootSource is a helper that attempts to create a BootSource and returns the error.
func createBootSource(ctx context.Context, name string, spec isobootv1alpha1.BootSourceSpec) error {
	resource := &isobootv1alpha1.BootSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: spec,
	}
	return k8sClient.Create(ctx, resource)
}

// deleteBootSource deletes a BootSource by name if it exists.
func deleteBootSource(ctx context.Context, name string) {
	resource := &isobootv1alpha1.BootSource{}
	key := types.NamespacedName{Name: name, Namespace: "default"}
	if err := k8sClient.Get(ctx, key, resource); err == nil {
		Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
	}
}

var _ = Describe("BootSource Controller", func() {

	// ── Positive tests: valid specs that should be accepted ──────────────

	Context("Valid specs", func() {
		ctx := context.Background()

		AfterEach(func() {
			for _, name := range []string{
				"valid-kernel-initrd",
				"valid-iso",
				"valid-kernel-initrd-firmware",
				"valid-iso-firmware",
				"valid-kernel-shasum-initrd-shasumurl",
				"valid-iso-shasum-only",
			} {
				deleteBootSource(ctx, name)
			}
		})

		It("should accept kernel+initrd", func() {
			Expect(createBootSource(ctx, "valid-kernel-initrd", isobootv1alpha1.BootSourceSpec{
				Kernel: validKernel(),
				Initrd: validInitrd(),
			})).To(Succeed())
		})

		It("should accept iso", func() {
			Expect(createBootSource(ctx, "valid-iso", isobootv1alpha1.BootSourceSpec{
				ISO: validISO(),
			})).To(Succeed())
		})

		It("should accept kernel+initrd+firmware", func() {
			Expect(createBootSource(ctx, "valid-kernel-initrd-firmware", isobootv1alpha1.BootSourceSpec{
				Kernel:   validKernel(),
				Initrd:   validInitrd(),
				Firmware: validFirmware(),
			})).To(Succeed())
		})

		It("should accept iso+firmware", func() {
			Expect(createBootSource(ctx, "valid-iso-firmware", isobootv1alpha1.BootSourceSpec{
				ISO:      validISO(),
				Firmware: validFirmware(),
			})).To(Succeed())
		})

		It("should accept kernel with shasum + initrd with shasumURL", func() {
			Expect(createBootSource(ctx, "valid-kernel-shasum-initrd-shasumurl", isobootv1alpha1.BootSourceSpec{
				Kernel: &isobootv1alpha1.DownloadableResource{
					URL:    debianKernel,
					Shasum: ptr.To(exampleSHA256Sum),
				},
				Initrd: validInitrd(),
			})).To(Succeed())
		})

		It("should accept iso with shasum only (no shasumURL)", func() {
			Expect(createBootSource(ctx, "valid-iso-shasum-only", isobootv1alpha1.BootSourceSpec{
				ISO: &isobootv1alpha1.ISOSource{
					DownloadableResource: isobootv1alpha1.DownloadableResource{
						URL:    debianMiniISO,
						Shasum: ptr.To(exampleSHA256Sum),
					},
					KernelPath: "/linux",
					InitrdPath: "/initrd.gz",
				},
			})).To(Succeed())
		})
	})

	// ── Negative tests: invalid specs that should be rejected by CEL ─────

	Context("Invalid specs rejected by CEL", func() {
		ctx := context.Background()

		It("should reject empty spec", func() {
			err := createBootSource(ctx, "invalid-empty", isobootv1alpha1.BootSourceSpec{})
			Expect(err).To(HaveOccurred())
			Expect(k8serrors.IsInvalid(err)).To(BeTrue())
		})

		It("should reject kernel only (no initrd)", func() {
			err := createBootSource(ctx, "invalid-kernel-only", isobootv1alpha1.BootSourceSpec{
				Kernel: validKernel(),
			})
			Expect(err).To(HaveOccurred())
			Expect(k8serrors.IsInvalid(err)).To(BeTrue())
		})

		It("should reject initrd only (no kernel)", func() {
			err := createBootSource(ctx, "invalid-initrd-only", isobootv1alpha1.BootSourceSpec{
				Initrd: validInitrd(),
			})
			Expect(err).To(HaveOccurred())
			Expect(k8serrors.IsInvalid(err)).To(BeTrue())
		})

		It("should reject iso + kernel (no mixing)", func() {
			err := createBootSource(ctx, "invalid-iso-kernel", isobootv1alpha1.BootSourceSpec{
				ISO:    validISO(),
				Kernel: validKernel(),
			})
			Expect(err).To(HaveOccurred())
			Expect(k8serrors.IsInvalid(err)).To(BeTrue())
		})

		It("should reject iso + initrd (no mixing)", func() {
			err := createBootSource(ctx, "invalid-iso-initrd", isobootv1alpha1.BootSourceSpec{
				ISO:    validISO(),
				Initrd: validInitrd(),
			})
			Expect(err).To(HaveOccurred())
			Expect(k8serrors.IsInvalid(err)).To(BeTrue())
		})

		It("should reject iso + kernel + initrd (no mixing)", func() {
			err := createBootSource(ctx, "invalid-iso-kernel-initrd", isobootv1alpha1.BootSourceSpec{
				ISO:    validISO(),
				Kernel: validKernel(),
				Initrd: validInitrd(),
			})
			Expect(err).To(HaveOccurred())
			Expect(k8serrors.IsInvalid(err)).To(BeTrue())
		})

		It("should reject iso with kernelPath but no initrdPath", func() {
			err := createBootSource(ctx, "invalid-iso-no-initrdpath", isobootv1alpha1.BootSourceSpec{
				ISO: &isobootv1alpha1.ISOSource{
					DownloadableResource: isobootv1alpha1.DownloadableResource{
						URL:       debianMiniISO,
						ShasumURL: ptr.To(debianSHA256),
					},
					KernelPath: "/linux",
				},
			})
			Expect(err).To(HaveOccurred())
		})

		It("should reject iso with initrdPath but no kernelPath", func() {
			err := createBootSource(ctx, "invalid-iso-no-kernelpath", isobootv1alpha1.BootSourceSpec{
				ISO: &isobootv1alpha1.ISOSource{
					DownloadableResource: isobootv1alpha1.DownloadableResource{
						URL:       debianMiniISO,
						ShasumURL: ptr.To(debianSHA256),
					},
					InitrdPath: "/initrd.gz",
				},
			})
			Expect(err).To(HaveOccurred())
		})

		It("should reject kernel with both shasumURL and shasum", func() {
			err := createBootSource(ctx, "invalid-kernel-both-checksums", isobootv1alpha1.BootSourceSpec{
				Kernel: &isobootv1alpha1.DownloadableResource{
					URL:       debianKernel,
					ShasumURL: ptr.To(debianSHA256),
					Shasum:    ptr.To(exampleSHA256Sum),
				},
				Initrd: validInitrd(),
			})
			Expect(err).To(HaveOccurred())
			Expect(k8serrors.IsInvalid(err)).To(BeTrue())
		})

		It("should reject initrd with both shasumURL and shasum", func() {
			err := createBootSource(ctx, "invalid-initrd-both-checksums", isobootv1alpha1.BootSourceSpec{
				Kernel: validKernel(),
				Initrd: &isobootv1alpha1.DownloadableResource{
					URL:       debianInitrd,
					ShasumURL: ptr.To(debianSHA256),
					Shasum:    ptr.To(exampleSHA256Sum),
				},
			})
			Expect(err).To(HaveOccurred())
			Expect(k8serrors.IsInvalid(err)).To(BeTrue())
		})

		It("should reject iso with both shasumURL and shasum", func() {
			err := createBootSource(ctx, "invalid-iso-both-checksums", isobootv1alpha1.BootSourceSpec{
				ISO: &isobootv1alpha1.ISOSource{
					DownloadableResource: isobootv1alpha1.DownloadableResource{
						URL:       debianMiniISO,
						ShasumURL: ptr.To(debianSHA256),
						Shasum:    ptr.To(exampleSHA256Sum),
					},
					KernelPath: "/linux",
					InitrdPath: "/initrd.gz",
				},
			})
			Expect(err).To(HaveOccurred())
			Expect(k8serrors.IsInvalid(err)).To(BeTrue())
		})

		It("should reject firmware with both shasumURL and shasum", func() {
			err := createBootSource(ctx, "invalid-firmware-both-checksums", isobootv1alpha1.BootSourceSpec{
				Kernel: validKernel(),
				Initrd: validInitrd(),
				Firmware: &isobootv1alpha1.DownloadableResource{
					URL:       debianFirmware,
					ShasumURL: ptr.To(debianFwSHA512),
					Shasum:    ptr.To(exampleSHA256Sum),
				},
			})
			Expect(err).To(HaveOccurred())
			Expect(k8serrors.IsInvalid(err)).To(BeTrue())
		})

		It("should reject kernel without any checksum", func() {
			err := createBootSource(ctx, "invalid-no-checksum", isobootv1alpha1.BootSourceSpec{
				Kernel: &isobootv1alpha1.DownloadableResource{
					URL: debianKernel,
				},
				Initrd: validInitrd(),
			})
			Expect(err).To(HaveOccurred())
			Expect(k8serrors.IsInvalid(err)).To(BeTrue())
		})
	})

	// ── Reconciliation test ─────────────────────────────────────────────

	Context("Reconciliation", func() {
		const resourceName = "test-reconcile"
		ctx := context.Background()

		BeforeEach(func() {
			Expect(createBootSource(ctx, resourceName, isobootv1alpha1.BootSourceSpec{
				ISO:      validISO(),
				Firmware: validFirmware(),
			})).To(Succeed())
		})

		AfterEach(func() {
			deleteBootSource(ctx, resourceName)
		})

		It("should successfully reconcile", func() {
			controllerReconciler := &BootSourceReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: resourceName, Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})

	// ── Helper function tests ─────────────────────────────────────────────

	Context("Helper functions", func() {
		var reconciler *BootSourceReconciler
		var tempDir string
		var fetcher *mockFetcher
		ctx := context.Background()

		BeforeEach(func() {
			tempDir = GinkgoT().TempDir()
			fetcher = &mockFetcher{}
			reconciler = &BootSourceReconciler{
				Client:  k8sClient,
				Scheme:  k8sClient.Scheme(),
				BaseDir: tempDir,
				Fetcher: fetcher,
			}
		})

		Describe("resolveExpectedHash", func() {
			It("returns inline shasum directly", func() {
				dr := &isobootv1alpha1.DownloadableResource{
					URL:    "https://example.com/file.bin",
					Shasum: ptr.To(exampleSHA256Sum),
				}
				hash, err := reconciler.resolveExpectedHash(ctx, dr)
				Expect(err).NotTo(HaveOccurred())
				Expect(hash).To(Equal(exampleSHA256Sum))
			})

			It("fetches and parses shasumURL", func() {
				fetcher.fetchContentFunc = func(_ context.Context, url string) ([]byte, error) {
					if url == "https://example.com/SHA256SUMS" {
						return []byte(fmt.Sprintf("%s  file.bin\n", exampleSHA256Sum)), nil
					}
					return nil, errors.New("not found")
				}

				dr := &isobootv1alpha1.DownloadableResource{
					URL:       "https://example.com/file.bin",
					ShasumURL: ptr.To("https://example.com/SHA256SUMS"),
				}
				hash, err := reconciler.resolveExpectedHash(ctx, dr)
				Expect(err).NotTo(HaveOccurred())
				Expect(hash).To(Equal(exampleSHA256Sum))
			})

			It("returns error for invalid shasumURL", func() {
				fetcher.fetchContentFunc = func(_ context.Context, _ string) ([]byte, error) {
					return nil, errors.New("HTTP 404")
				}

				dr := &isobootv1alpha1.DownloadableResource{
					URL:       "https://example.com/file.bin",
					ShasumURL: ptr.To("https://example.com/SHA256SUMS"),
				}
				_, err := reconciler.resolveExpectedHash(ctx, dr)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("fetching shasum file"))
			})

			It("returns error when no checksum source specified", func() {
				dr := &isobootv1alpha1.DownloadableResource{
					URL: "https://example.com/file.bin",
				}
				_, err := reconciler.resolveExpectedHash(ctx, dr)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("no checksum source specified"))
			})
		})

		Describe("downloadResource", func() {
			It("downloads file successfully", func() {
				content := []byte("test file content")
				fetcher.downloadFunc = func(_ context.Context, _ string, destPath string) error {
					return os.WriteFile(destPath, content, 0o644)
				}

				destPath := filepath.Join(tempDir, "downloaded.bin")
				err := reconciler.downloadResource(ctx, "https://example.com/file.bin", destPath)
				Expect(err).NotTo(HaveOccurred())

				data, err := os.ReadFile(destPath)
				Expect(err).NotTo(HaveOccurred())
				Expect(data).To(Equal(content))
			})

			It("returns error for HTTP 404", func() {
				fetcher.downloadFunc = func(_ context.Context, url string, _ string) error {
					return fmt.Errorf("downloading %s: HTTP 404", url)
				}

				destPath := filepath.Join(tempDir, "notfound.bin")
				err := reconciler.downloadResource(ctx, "https://example.com/notfound.bin", destPath)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("404"))
			})
		})

		Describe("verifyResource", func() {
			It("passes when hash matches", func() {
				content := []byte("test content for hashing")
				hash := sha256.Sum256(content)
				expectedHash := hex.EncodeToString(hash[:])

				filePath := filepath.Join(tempDir, "hashtest.bin")
				Expect(os.WriteFile(filePath, content, 0o644)).To(Succeed())

				err := reconciler.verifyResource(filePath, expectedHash)
				Expect(err).NotTo(HaveOccurred())
			})

			It("fails when hash does not match", func() {
				content := []byte("test content for hashing")
				wrongHash := exampleSHA256Sum // This is the hash of an empty file

				filePath := filepath.Join(tempDir, "hashtest-fail.bin")
				Expect(os.WriteFile(filePath, content, 0o644)).To(Succeed())

				err := reconciler.verifyResource(filePath, wrongHash)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("hash mismatch"))
			})
		})

		Describe("ensureDirectory", func() {
			It("creates nested directories", func() {
				dir, err := reconciler.ensureDirectory("my-namespace", "my-bootsource")
				Expect(err).NotTo(HaveOccurred())
				Expect(dir).To(Equal(filepath.Join(tempDir, "my-namespace", "my-bootsource")))

				info, err := os.Stat(dir)
				Expect(err).NotTo(HaveOccurred())
				Expect(info.IsDir()).To(BeTrue())
			})

			It("is idempotent", func() {
				dir1, err := reconciler.ensureDirectory("ns", "name")
				Expect(err).NotTo(HaveOccurred())

				dir2, err := reconciler.ensureDirectory("ns", "name")
				Expect(err).NotTo(HaveOccurred())
				Expect(dir1).To(Equal(dir2))
			})
		})

		Describe("worstPhase", func() {
			It("returns Pending for empty input", func() {
				Expect(worstPhase(nil)).To(Equal(isobootv1alpha1.BootSourcePhasePending))
				Expect(worstPhase([]isobootv1alpha1.BootSourcePhase{})).To(Equal(isobootv1alpha1.BootSourcePhasePending))
			})

			It("returns the single phase for single-element input", func() {
				Expect(worstPhase([]isobootv1alpha1.BootSourcePhase{
					isobootv1alpha1.BootSourcePhaseReady,
				})).To(Equal(isobootv1alpha1.BootSourcePhaseReady))
			})

			It("returns Failed as worst over all other phases", func() {
				phases := []isobootv1alpha1.BootSourcePhase{
					isobootv1alpha1.BootSourcePhaseReady,
					isobootv1alpha1.BootSourcePhasePending,
					isobootv1alpha1.BootSourcePhaseVerifying,
					isobootv1alpha1.BootSourcePhaseBuilding,
					isobootv1alpha1.BootSourcePhaseExtracting,
					isobootv1alpha1.BootSourcePhaseDownloading,
					isobootv1alpha1.BootSourcePhaseCorrupted,
					isobootv1alpha1.BootSourcePhaseFailed,
				}
				Expect(worstPhase(phases)).To(Equal(isobootv1alpha1.BootSourcePhaseFailed))
			})

			It("returns Corrupted when no Failed phase present", func() {
				phases := []isobootv1alpha1.BootSourcePhase{
					isobootv1alpha1.BootSourcePhaseReady,
					isobootv1alpha1.BootSourcePhaseCorrupted,
					isobootv1alpha1.BootSourcePhaseDownloading,
				}
				Expect(worstPhase(phases)).To(Equal(isobootv1alpha1.BootSourcePhaseCorrupted))
			})

			It("returns Downloading over Extracting/Building/Verifying/Pending/Ready", func() {
				phases := []isobootv1alpha1.BootSourcePhase{
					isobootv1alpha1.BootSourcePhaseReady,
					isobootv1alpha1.BootSourcePhasePending,
					isobootv1alpha1.BootSourcePhaseVerifying,
					isobootv1alpha1.BootSourcePhaseBuilding,
					isobootv1alpha1.BootSourcePhaseExtracting,
					isobootv1alpha1.BootSourcePhaseDownloading,
				}
				Expect(worstPhase(phases)).To(Equal(isobootv1alpha1.BootSourcePhaseDownloading))
			})

			It("returns Ready when all phases are Ready", func() {
				phases := []isobootv1alpha1.BootSourcePhase{
					isobootv1alpha1.BootSourcePhaseReady,
					isobootv1alpha1.BootSourcePhaseReady,
					isobootv1alpha1.BootSourcePhaseReady,
				}
				Expect(worstPhase(phases)).To(Equal(isobootv1alpha1.BootSourcePhaseReady))
			})
		})
	})
})
