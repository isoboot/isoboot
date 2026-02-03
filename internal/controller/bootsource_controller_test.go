package controller

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	isobootv1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
	"github.com/isoboot/isoboot/internal/filewatcher"
)

// sha256sum computes the SHA-256 hash of data and returns it as a hex string.
func sha256sum(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

// sha512sum computes the SHA-512 hash of data and returns it as a hex string.
func sha512sum(data []byte) string {
	hash := sha512.Sum512(data)
	return hex.EncodeToString(hash[:])
}

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

// createTestISO creates a minimal ISO 9660 image with /linux and /initrd.gz files.
func createTestISO() []byte {
	return createMinimalISO("/linux", "/initrd.gz")
}

// createTestISOWithPaths creates a minimal ISO 9660 image with the specified paths.
func createTestISOWithPaths(kernelPath, initrdPath string) []byte {
	return createMinimalISO(kernelPath, initrdPath)
}

// createMinimalISO creates a minimal but valid ISO 9660 image in memory.
func createMinimalISO(kernelPath, initrdPath string) []byte {
	// ISO 9660 sector size
	const sectorSize = 2048

	// We need at least 17 sectors: 16 system area + 1 PVD
	// Plus directory records and file data
	kernelContent := []byte("test kernel content")
	initrdContent := []byte("test initrd content")

	// Calculate sectors needed
	// Sector 16: Primary Volume Descriptor
	// Sector 17: Root directory
	// Sector 18+: File data
	kernelSectors := (len(kernelContent) + sectorSize - 1) / sectorSize
	initrdSectors := (len(initrdContent) + sectorSize - 1) / sectorSize

	totalSectors := 19 + kernelSectors + initrdSectors
	isoData := make([]byte, totalSectors*sectorSize)

	// Write Primary Volume Descriptor at sector 16
	pvd := isoData[16*sectorSize : 17*sectorSize]
	pvd[0] = 1                              // Type: PVD
	copy(pvd[1:6], "CD001")                 // Standard identifier
	pvd[6] = 1                              // Version
	copy(pvd[8:40], padString("", 32))      // System identifier
	copy(pvd[40:72], padString("TEST", 32)) // Volume identifier

	// Volume space size (little endian at 80, big endian at 84)
	writeInt32Both(pvd[80:88], uint32(totalSectors))

	// Set size (little endian at 120)
	writeInt16Both(pvd[120:124], 1)

	// Volume set size
	writeInt16Both(pvd[124:128], 1)

	// Volume sequence number
	writeInt16Both(pvd[128:132], 1)

	// Logical block size
	writeInt16Both(pvd[132:136], sectorSize)

	// Path table size (placeholder)
	writeInt32Both(pvd[136:144], 10)

	// Root directory record at offset 156 (34 bytes)
	rootDir := pvd[156:190]
	rootDir[0] = 34                            // Length of directory record
	rootDir[1] = 0                             // Extended attribute record length
	writeInt32Both(rootDir[2:10], 17)          // Location of extent (sector 17)
	writeInt32Both(rootDir[10:18], sectorSize) // Data length
	rootDir[25] = 0x02                         // File flags: directory
	rootDir[32] = 1                            // File identifier length
	rootDir[33] = 0                            // File identifier (root)

	// Volume descriptor set terminator at sector 17 would go here,
	// but for simplicity we'll put root directory at sector 17

	// Write root directory at sector 17
	rootDirData := isoData[17*sectorSize : 18*sectorSize]

	// . entry (self)
	offset := 0
	dotEntry := rootDirData[offset : offset+34]
	dotEntry[0] = 34
	writeInt32Both(dotEntry[2:10], 17)
	writeInt32Both(dotEntry[10:18], sectorSize)
	dotEntry[25] = 0x02
	dotEntry[32] = 1
	dotEntry[33] = 0x00
	offset += 34

	// .. entry (parent, same as self for root)
	dotDotEntry := rootDirData[offset : offset+34]
	copy(dotDotEntry, dotEntry)
	dotDotEntry[33] = 0x01
	offset += 34

	// linux file entry (without ";1" version suffix - isoextract strips it anyway)
	kernelName := strings.TrimPrefix(kernelPath, "/")
	kernelRecLen := 33 + len(kernelName)
	if kernelRecLen%2 == 1 {
		kernelRecLen++ // Pad to even length
	}
	kernelEntry := rootDirData[offset : offset+kernelRecLen]
	kernelEntry[0] = byte(kernelRecLen)
	writeInt32Both(kernelEntry[2:10], 18) // Location at sector 18
	writeInt32Both(kernelEntry[10:18], uint32(len(kernelContent)))
	kernelEntry[25] = 0x00                  // File flags: regular file
	kernelEntry[32] = byte(len(kernelName)) // File ID length
	copy(kernelEntry[33:33+len(kernelName)], kernelName)
	offset += kernelRecLen

	// initrd.gz file entry (without ";1" version suffix)
	initrdName := strings.TrimPrefix(initrdPath, "/")
	initrdRecLen := 33 + len(initrdName)
	if initrdRecLen%2 == 1 {
		initrdRecLen++ // Pad to even length
	}
	initrdEntry := rootDirData[offset : offset+initrdRecLen]
	initrdEntry[0] = byte(initrdRecLen)
	writeInt32Both(initrdEntry[2:10], uint32(18+kernelSectors))
	writeInt32Both(initrdEntry[10:18], uint32(len(initrdContent)))
	initrdEntry[25] = 0x00
	initrdEntry[32] = byte(len(initrdName))
	copy(initrdEntry[33:33+len(initrdName)], initrdName)

	// Write file data
	copy(isoData[18*sectorSize:], kernelContent)
	copy(isoData[(18+kernelSectors)*sectorSize:], initrdContent)

	return isoData
}

func padString(s string, length int) string {
	if len(s) >= length {
		return s[:length]
	}
	return s + strings.Repeat(" ", length-len(s))
}

func writeInt32Both(b []byte, v uint32) {
	// Little endian
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
	// Big endian
	b[4] = byte(v >> 24)
	b[5] = byte(v >> 16)
	b[6] = byte(v >> 8)
	b[7] = byte(v)
}

func writeInt16Both(b []byte, v uint16) {
	// Little endian
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	// Big endian
	b[2] = byte(v >> 8)
	b[3] = byte(v)
}

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

	// ── Reconciliation tests ────────────────────────────────────────────

	Context("Reconciliation", func() {
		ctx := context.Background()
		var tempDir string
		var fetcher *mockFetcher
		var reconciler *BootSourceReconciler

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

		AfterEach(func() {
			// Clean up test resources
			for _, name := range []string{
				"test-reconcile-iso",
				"test-kernel-initrd",
				"test-kernel-initrd-firmware",
				"test-hash-mismatch",
				"test-network-failure",
				"test-delete-cleanup",
				"test-iso-extraction",
				"test-iso-extraction-failure",
				"test-iso-firmware",
				"test-initrd-firmware-direct",
				"test-initrd-firmware-rebuild",
				"test-initrd-firmware-missing",
			} {
				deleteBootSource(ctx, name)
			}
		})

		It("should reach Ready phase for ISO mode with extraction", func() {
			isoContent := createTestISO()
			isoHash := sha256sum(isoContent)

			fetcher.fetchContentFunc = func(_ context.Context, url string) ([]byte, error) {
				if url == debianSHA256 {
					return fmt.Appendf(nil, "%s  mini.iso\n", isoHash), nil
				}
				return nil, fmt.Errorf("unexpected URL: %s", url)
			}
			fetcher.downloadFunc = func(_ context.Context, url, destPath string) error {
				if url == debianMiniISO {
					return os.WriteFile(destPath, isoContent, 0o644)
				}
				return fmt.Errorf("unexpected URL: %s", url)
			}

			Expect(createBootSource(ctx, "test-iso-extraction", isobootv1alpha1.BootSourceSpec{
				ISO: &isobootv1alpha1.ISOSource{
					DownloadableResource: isobootv1alpha1.DownloadableResource{
						URL:       debianMiniISO,
						ShasumURL: ptr.To(debianSHA256),
					},
					KernelPath: "/linux",
					InitrdPath: "/initrd.gz",
				},
			})).To(Succeed())

			// First reconcile adds finalizer
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-iso-extraction", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(1 * time.Millisecond))

			// Second reconcile downloads ISO and extracts
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-iso-extraction", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify status
			var bs isobootv1alpha1.BootSource
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "test-iso-extraction", Namespace: "default"}, &bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseReady))
			Expect(bs.Status.Resources).To(HaveKey("iso"))
			Expect(bs.Status.Resources).To(HaveKey("kernel"))
			Expect(bs.Status.Resources).To(HaveKey("initrd"))

			// Verify extracted files exist
			Expect(filepath.Join(tempDir, "default", "test-iso-extraction", "kernel")).To(BeAnExistingFile())
			Expect(filepath.Join(tempDir, "default", "test-iso-extraction", "initrd")).To(BeAnExistingFile())
		})

		It("should set Failed phase when ISO extraction fails", func() {
			// Create an ISO that doesn't contain the expected paths (using different top-level filenames)
			isoContent := createTestISOWithPaths("/otherlinux", "/otherinitrd")
			isoHash := sha256sum(isoContent)

			fetcher.fetchContentFunc = func(_ context.Context, url string) ([]byte, error) {
				if url == debianSHA256 {
					return fmt.Appendf(nil, "%s  mini.iso\n", isoHash), nil
				}
				return nil, fmt.Errorf("unexpected URL: %s", url)
			}
			fetcher.downloadFunc = func(_ context.Context, url, destPath string) error {
				if url == debianMiniISO {
					return os.WriteFile(destPath, isoContent, 0o644)
				}
				return fmt.Errorf("unexpected URL: %s", url)
			}

			Expect(createBootSource(ctx, "test-iso-extraction-failure", isobootv1alpha1.BootSourceSpec{
				ISO: &isobootv1alpha1.ISOSource{
					DownloadableResource: isobootv1alpha1.DownloadableResource{
						URL:       debianMiniISO,
						ShasumURL: ptr.To(debianSHA256),
					},
					KernelPath: "/linux",     // This path doesn't exist in our test ISO
					InitrdPath: "/initrd.gz", // This path doesn't exist either
				},
			})).To(Succeed())

			// First reconcile adds finalizer
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-iso-extraction-failure", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile downloads ISO but fails extraction
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-iso-extraction-failure", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(5 * time.Minute)) // Error requeue

			// Verify status
			var bs isobootv1alpha1.BootSource
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "test-iso-extraction-failure", Namespace: "default"}, &bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseFailed))
			Expect(bs.Status.Message).To(ContainSubstring("extraction"))
		})

		It("should build initrdWithFirmware for ISO mode with firmware", func() {
			isoContent := createTestISO()
			isoHash := sha256sum(isoContent)
			firmwareContent := []byte("firmware cpio content")
			firmwareHash := sha512sum(firmwareContent)

			fetcher.fetchContentFunc = func(_ context.Context, url string) ([]byte, error) {
				if url == debianSHA256 {
					return fmt.Appendf(nil, "%s  mini.iso\n", isoHash), nil
				}
				if url == debianFwSHA512 {
					return fmt.Appendf(nil, "%s  firmware.cpio.gz\n", firmwareHash), nil
				}
				return nil, fmt.Errorf("unexpected URL: %s", url)
			}
			fetcher.downloadFunc = func(_ context.Context, url, destPath string) error {
				switch url {
				case debianMiniISO:
					return os.WriteFile(destPath, isoContent, 0o644)
				case debianFirmware:
					return os.WriteFile(destPath, firmwareContent, 0o644)
				default:
					return fmt.Errorf("unexpected URL: %s", url)
				}
			}

			Expect(createBootSource(ctx, "test-iso-firmware", isobootv1alpha1.BootSourceSpec{
				ISO: &isobootv1alpha1.ISOSource{
					DownloadableResource: isobootv1alpha1.DownloadableResource{
						URL:       debianMiniISO,
						ShasumURL: ptr.To(debianSHA256),
					},
					KernelPath: "/linux",
					InitrdPath: "/initrd.gz",
				},
				Firmware: validFirmware(),
			})).To(Succeed())

			// First reconcile adds finalizer
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-iso-firmware", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile downloads, extracts, and builds initrdWithFirmware
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-iso-firmware", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify status
			var bs isobootv1alpha1.BootSource
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "test-iso-firmware", Namespace: "default"}, &bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseReady))
			Expect(bs.Status.Resources).To(HaveKey("initrdWithFirmware"))
			Expect(bs.Status.Resources["initrdWithFirmware"].Path).NotTo(BeEmpty())

			// Verify file exists
			Expect(filepath.Join(tempDir, "default", "test-iso-firmware", "initrdWithFirmware")).To(BeAnExistingFile())
		})

		It("should build initrdWithFirmware for direct mode with firmware", func() {
			kernelContent := []byte("kernel binary content")
			initrdContent := []byte("initrd binary content")
			firmwareContent := []byte("firmware cpio content")
			kernelHash := sha256sum(kernelContent)
			initrdHash := sha256sum(initrdContent)
			firmwareHash := sha512sum(firmwareContent)

			fetcher.fetchContentFunc = func(_ context.Context, url string) ([]byte, error) {
				if url == debianSHA256 {
					return fmt.Appendf(nil, "%s  linux\n%s  initrd.gz\n", kernelHash, initrdHash), nil
				}
				if url == debianFwSHA512 {
					return fmt.Appendf(nil, "%s  firmware.cpio.gz\n", firmwareHash), nil
				}
				return nil, fmt.Errorf("unexpected URL: %s", url)
			}
			fetcher.downloadFunc = func(_ context.Context, url, destPath string) error {
				switch url {
				case debianKernel:
					return os.WriteFile(destPath, kernelContent, 0o644)
				case debianInitrd:
					return os.WriteFile(destPath, initrdContent, 0o644)
				case debianFirmware:
					return os.WriteFile(destPath, firmwareContent, 0o644)
				default:
					return fmt.Errorf("unexpected URL: %s", url)
				}
			}

			Expect(createBootSource(ctx, "test-initrd-firmware-direct", isobootv1alpha1.BootSourceSpec{
				Kernel:   validKernel(),
				Initrd:   validInitrd(),
				Firmware: validFirmware(),
			})).To(Succeed())

			// First reconcile adds finalizer
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-initrd-firmware-direct", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile downloads and builds initrdWithFirmware
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-initrd-firmware-direct", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify status
			var bs isobootv1alpha1.BootSource
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "test-initrd-firmware-direct", Namespace: "default"}, &bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseReady))
			Expect(bs.Status.Resources).To(HaveKey("initrdWithFirmware"))

			// Verify initrdWithFirmware has correct concatenated content
			combinedPath := filepath.Join(tempDir, "default", "test-initrd-firmware-direct", "initrdWithFirmware")
			Expect(combinedPath).To(BeAnExistingFile())

			combinedContent, err := os.ReadFile(combinedPath)
			Expect(err).NotTo(HaveOccurred())
			expectedContent := append(initrdContent, firmwareContent...)
			Expect(combinedContent).To(Equal(expectedContent))
		})

		It("should rebuild corrupted initrdWithFirmware", func() {
			kernelContent := []byte("kernel binary content")
			initrdContent := []byte("initrd binary content")
			firmwareContent := []byte("firmware cpio content")
			kernelHash := sha256sum(kernelContent)
			initrdHash := sha256sum(initrdContent)
			firmwareHash := sha512sum(firmwareContent)

			fetcher.fetchContentFunc = func(_ context.Context, url string) ([]byte, error) {
				if url == debianSHA256 {
					return fmt.Appendf(nil, "%s  linux\n%s  initrd.gz\n", kernelHash, initrdHash), nil
				}
				if url == debianFwSHA512 {
					return fmt.Appendf(nil, "%s  firmware.cpio.gz\n", firmwareHash), nil
				}
				return nil, fmt.Errorf("unexpected URL: %s", url)
			}
			fetcher.downloadFunc = func(_ context.Context, url, destPath string) error {
				switch url {
				case debianKernel:
					return os.WriteFile(destPath, kernelContent, 0o644)
				case debianInitrd:
					return os.WriteFile(destPath, initrdContent, 0o644)
				case debianFirmware:
					return os.WriteFile(destPath, firmwareContent, 0o644)
				default:
					return fmt.Errorf("unexpected URL: %s", url)
				}
			}

			Expect(createBootSource(ctx, "test-initrd-firmware-rebuild", isobootv1alpha1.BootSourceSpec{
				Kernel:   validKernel(),
				Initrd:   validInitrd(),
				Firmware: validFirmware(),
			})).To(Succeed())

			// First reconcile adds finalizer
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-initrd-firmware-rebuild", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile builds initrdWithFirmware
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-initrd-firmware-rebuild", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Corrupt the initrdWithFirmware file
			combinedPath := filepath.Join(tempDir, "default", "test-initrd-firmware-rebuild", "initrdWithFirmware")
			Expect(os.WriteFile(combinedPath, []byte("corrupted"), 0o644)).To(Succeed())

			// Third reconcile should detect corruption and rebuild
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-initrd-firmware-rebuild", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify file was rebuilt correctly
			combinedContent, err := os.ReadFile(combinedPath)
			Expect(err).NotTo(HaveOccurred())
			expectedContent := append(initrdContent, firmwareContent...)
			Expect(combinedContent).To(Equal(expectedContent))
		})

		It("should rebuild missing initrdWithFirmware", func() {
			kernelContent := []byte("kernel binary content")
			initrdContent := []byte("initrd binary content")
			firmwareContent := []byte("firmware cpio content")
			kernelHash := sha256sum(kernelContent)
			initrdHash := sha256sum(initrdContent)
			firmwareHash := sha512sum(firmwareContent)

			fetcher.fetchContentFunc = func(_ context.Context, url string) ([]byte, error) {
				if url == debianSHA256 {
					return fmt.Appendf(nil, "%s  linux\n%s  initrd.gz\n", kernelHash, initrdHash), nil
				}
				if url == debianFwSHA512 {
					return fmt.Appendf(nil, "%s  firmware.cpio.gz\n", firmwareHash), nil
				}
				return nil, fmt.Errorf("unexpected URL: %s", url)
			}
			fetcher.downloadFunc = func(_ context.Context, url, destPath string) error {
				switch url {
				case debianKernel:
					return os.WriteFile(destPath, kernelContent, 0o644)
				case debianInitrd:
					return os.WriteFile(destPath, initrdContent, 0o644)
				case debianFirmware:
					return os.WriteFile(destPath, firmwareContent, 0o644)
				default:
					return fmt.Errorf("unexpected URL: %s", url)
				}
			}

			Expect(createBootSource(ctx, "test-initrd-firmware-missing", isobootv1alpha1.BootSourceSpec{
				Kernel:   validKernel(),
				Initrd:   validInitrd(),
				Firmware: validFirmware(),
			})).To(Succeed())

			// First reconcile adds finalizer
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-initrd-firmware-missing", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile builds initrdWithFirmware
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-initrd-firmware-missing", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Delete the initrdWithFirmware file
			combinedPath := filepath.Join(tempDir, "default", "test-initrd-firmware-missing", "initrdWithFirmware")
			Expect(os.Remove(combinedPath)).To(Succeed())

			// Third reconcile should detect missing file and rebuild
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-initrd-firmware-missing", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify file was rebuilt
			Expect(combinedPath).To(BeAnExistingFile())
			combinedContent, err := os.ReadFile(combinedPath)
			Expect(err).NotTo(HaveOccurred())
			expectedContent := append(initrdContent, firmwareContent...)
			Expect(combinedContent).To(Equal(expectedContent))
		})

		It("should reach Ready phase for kernel+initrd", func() {
			kernelContent := []byte("kernel binary content")
			initrdContent := []byte("initrd binary content")
			kernelHash := sha256sum(kernelContent)
			initrdHash := sha256sum(initrdContent)

			// Mock fetcher returns hash for shasumURL and downloads content
			fetcher.fetchContentFunc = func(_ context.Context, url string) ([]byte, error) {
				return fmt.Appendf(nil, "%s  linux\n%s  initrd.gz\n", kernelHash, initrdHash), nil
			}
			fetcher.downloadFunc = func(_ context.Context, url, destPath string) error {
				if url == debianKernel {
					return os.WriteFile(destPath, kernelContent, 0o644)
				}
				if url == debianInitrd {
					return os.WriteFile(destPath, initrdContent, 0o644)
				}
				return fmt.Errorf("unexpected URL: %s", url)
			}

			Expect(createBootSource(ctx, "test-kernel-initrd", isobootv1alpha1.BootSourceSpec{
				Kernel: validKernel(),
				Initrd: validInitrd(),
			})).To(Succeed())

			// First reconcile adds finalizer
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-kernel-initrd", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(1 * time.Millisecond))

			// Second reconcile downloads resources
			result, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-kernel-initrd", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify status
			var bs isobootv1alpha1.BootSource
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "test-kernel-initrd", Namespace: "default"}, &bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseReady))
			Expect(bs.Status.Resources).To(HaveKey("kernel"))
			Expect(bs.Status.Resources).To(HaveKey("initrd"))
			Expect(bs.Status.Resources["kernel"].URL).To(Equal(debianKernel))
			Expect(bs.Status.Resources["initrd"].URL).To(Equal(debianInitrd))

			// Verify files exist
			Expect(filepath.Join(tempDir, "default", "test-kernel-initrd", "kernel")).To(BeAnExistingFile())
			Expect(filepath.Join(tempDir, "default", "test-kernel-initrd", "initrd")).To(BeAnExistingFile())
		})

		It("should reach Ready phase for kernel+initrd+firmware", func() {
			kernelContent := []byte("kernel binary content")
			initrdContent := []byte("initrd binary content")
			firmwareContent := []byte("firmware cpio content")
			kernelHash := sha256sum(kernelContent)
			initrdHash := sha256sum(initrdContent)
			firmwareHash := sha512sum(firmwareContent)

			fetcher.fetchContentFunc = func(_ context.Context, url string) ([]byte, error) {
				if url == debianSHA256 {
					return fmt.Appendf(nil, "%s  linux\n%s  initrd.gz\n", kernelHash, initrdHash), nil
				}
				if url == debianFwSHA512 {
					return fmt.Appendf(nil, "%s  firmware.cpio.gz\n", firmwareHash), nil
				}
				return nil, fmt.Errorf("unexpected URL: %s", url)
			}
			fetcher.downloadFunc = func(_ context.Context, url, destPath string) error {
				switch url {
				case debianKernel:
					return os.WriteFile(destPath, kernelContent, 0o644)
				case debianInitrd:
					return os.WriteFile(destPath, initrdContent, 0o644)
				case debianFirmware:
					return os.WriteFile(destPath, firmwareContent, 0o644)
				default:
					return fmt.Errorf("unexpected URL: %s", url)
				}
			}

			Expect(createBootSource(ctx, "test-kernel-initrd-firmware", isobootv1alpha1.BootSourceSpec{
				Kernel:   validKernel(),
				Initrd:   validInitrd(),
				Firmware: validFirmware(),
			})).To(Succeed())

			// First reconcile adds finalizer
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-kernel-initrd-firmware", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile downloads resources
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-kernel-initrd-firmware", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify status
			var bs isobootv1alpha1.BootSource
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "test-kernel-initrd-firmware", Namespace: "default"}, &bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseReady))
			Expect(bs.Status.Resources).To(HaveKey("kernel"))
			Expect(bs.Status.Resources).To(HaveKey("initrd"))
			Expect(bs.Status.Resources).To(HaveKey("firmware"))
		})

		It("should set Corrupted phase on hash mismatch", func() {
			kernelContent := []byte("kernel binary content")
			initrdContent := []byte("initrd binary content")
			kernelHash := sha256sum(kernelContent)
			wrongInitrdHash := exampleSHA256Sum // Wrong hash

			fetcher.fetchContentFunc = func(_ context.Context, _ string) ([]byte, error) {
				return fmt.Appendf(nil, "%s  linux\n%s  initrd.gz\n", kernelHash, wrongInitrdHash), nil
			}
			fetcher.downloadFunc = func(_ context.Context, url, destPath string) error {
				if url == debianKernel {
					return os.WriteFile(destPath, kernelContent, 0o644)
				}
				if url == debianInitrd {
					return os.WriteFile(destPath, initrdContent, 0o644) // Correct content but wrong expected hash
				}
				return fmt.Errorf("unexpected URL: %s", url)
			}

			Expect(createBootSource(ctx, "test-hash-mismatch", isobootv1alpha1.BootSourceSpec{
				Kernel: validKernel(),
				Initrd: validInitrd(),
			})).To(Succeed())

			// First reconcile adds finalizer
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-hash-mismatch", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile attempts downloads
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-hash-mismatch", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(5 * time.Minute)) // Error requeue interval

			// Verify status
			var bs isobootv1alpha1.BootSource
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "test-hash-mismatch", Namespace: "default"}, &bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseCorrupted))
			Expect(bs.Status.Message).To(ContainSubstring("initrd"))
		})

		It("should set Failed phase on network failure", func() {
			fetcher.fetchContentFunc = func(_ context.Context, _ string) ([]byte, error) {
				return nil, errors.New("connection refused")
			}

			Expect(createBootSource(ctx, "test-network-failure", isobootv1alpha1.BootSourceSpec{
				Kernel: validKernel(),
				Initrd: validInitrd(),
			})).To(Succeed())

			// First reconcile adds finalizer
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-network-failure", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile attempts downloads
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-network-failure", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(5 * time.Minute)) // Error requeue interval

			// Verify status
			var bs isobootv1alpha1.BootSource
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "test-network-failure", Namespace: "default"}, &bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseFailed))
		})

		It("should clean up files on deletion", func() {
			kernelContent := []byte("kernel binary content")
			initrdContent := []byte("initrd binary content")
			kernelHash := sha256sum(kernelContent)
			initrdHash := sha256sum(initrdContent)

			fetcher.fetchContentFunc = func(_ context.Context, _ string) ([]byte, error) {
				return fmt.Appendf(nil, "%s  linux\n%s  initrd.gz\n", kernelHash, initrdHash), nil
			}
			fetcher.downloadFunc = func(_ context.Context, url, destPath string) error {
				if url == debianKernel {
					return os.WriteFile(destPath, kernelContent, 0o644)
				}
				if url == debianInitrd {
					return os.WriteFile(destPath, initrdContent, 0o644)
				}
				return fmt.Errorf("unexpected URL: %s", url)
			}

			Expect(createBootSource(ctx, "test-delete-cleanup", isobootv1alpha1.BootSourceSpec{
				Kernel: validKernel(),
				Initrd: validInitrd(),
			})).To(Succeed())

			// First reconcile adds finalizer
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-delete-cleanup", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile downloads resources
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-delete-cleanup", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify files exist
			resourceDir := filepath.Join(tempDir, "default", "test-delete-cleanup")
			Expect(resourceDir).To(BeADirectory())
			Expect(filepath.Join(resourceDir, "kernel")).To(BeAnExistingFile())

			// Delete the BootSource
			var bs isobootv1alpha1.BootSource
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "test-delete-cleanup", Namespace: "default"}, &bs)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &bs)).To(Succeed())

			// Reconcile to process deletion
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-delete-cleanup", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify files are cleaned up
			Expect(resourceDir).NotTo(BeADirectory())
		})

		It("should return nil error for missing BootSource", func() {
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "nonexistent-bootsource", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))
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
						return fmt.Appendf(nil, "%s  file.bin\n", exampleSHA256Sum), nil
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
				wrongHash := exampleSHA256Sum // SHA-256 of empty file, used here as an incorrect hash

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

			It("returns error when BaseDir is empty", func() {
				reconciler.BaseDir = ""
				_, err := reconciler.ensureDirectory("ns", "name")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("BaseDir is not configured"))
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

			It("returns Failed for unknown phase", func() {
				phases := []isobootv1alpha1.BootSourcePhase{
					isobootv1alpha1.BootSourcePhaseReady,
					isobootv1alpha1.BootSourcePhase("UnknownPhase"),
				}
				Expect(worstPhase(phases)).To(Equal(isobootv1alpha1.BootSourcePhaseFailed))
			})
		})
	})

	// ── Filewatcher integration tests ─────────────────────────────────────

	Context("Filewatcher integration", func() {
		ctx := context.Background()
		var tempDir string
		var fetcher *mockFetcher
		var reconciler *BootSourceReconciler
		var watcher *filewatcher.Watcher

		BeforeEach(func() {
			tempDir = GinkgoT().TempDir()
			fetcher = &mockFetcher{}

			var err error
			watcher, err = filewatcher.New(100)
			Expect(err).NotTo(HaveOccurred())

			// Start watcher in background
			watcherCtx, watcherCancel := context.WithCancel(ctx)
			go func() {
				_ = watcher.Start(watcherCtx)
			}()
			DeferCleanup(func() {
				watcherCancel()
				_ = watcher.Close()
			})

			reconciler = &BootSourceReconciler{
				Client:  k8sClient,
				Scheme:  k8sClient.Scheme(),
				BaseDir: tempDir,
				Fetcher: fetcher,
				Watcher: watcher,
			}
		})

		AfterEach(func() {
			for _, name := range []string{
				"test-fw-watch-paths",
				"test-fw-deletion-unwatch",
				"test-fw-nil-watcher",
				"test-fw-multiple-resources",
				"test-fw-initrd-firmware-watched",
				"test-fw-file-deleted",
				"test-fw-file-corrupted",
				"test-fw-watch-error",
			} {
				deleteBootSource(ctx, name)
			}
		})

		It("should register watches for all resource paths after reconcile", func() {
			kernelContent := []byte("kernel binary content")
			initrdContent := []byte("initrd binary content")
			kernelHash := sha256sum(kernelContent)
			initrdHash := sha256sum(initrdContent)

			fetcher.fetchContentFunc = func(_ context.Context, _ string) ([]byte, error) {
				return fmt.Appendf(nil, "%s  linux\n%s  initrd.gz\n", kernelHash, initrdHash), nil
			}
			fetcher.downloadFunc = func(_ context.Context, url, destPath string) error {
				if url == debianKernel {
					return os.WriteFile(destPath, kernelContent, 0o644)
				}
				if url == debianInitrd {
					return os.WriteFile(destPath, initrdContent, 0o644)
				}
				return fmt.Errorf("unexpected URL: %s", url)
			}

			Expect(createBootSource(ctx, "test-fw-watch-paths", isobootv1alpha1.BootSourceSpec{
				Kernel: validKernel(),
				Initrd: validInitrd(),
			})).To(Succeed())

			// First reconcile adds finalizer
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-fw-watch-paths", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile downloads resources and registers watches
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-fw-watch-paths", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify status is Ready
			var bs isobootv1alpha1.BootSource
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "test-fw-watch-paths", Namespace: "default"}, &bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseReady))

			// Verify paths are watched by attempting to watch with a DIFFERENT key
			// This should FAIL because the path is already watched by the controller's key
			kernelPath := filepath.Join(tempDir, "default", "test-fw-watch-paths", "kernel")
			initrdPath := filepath.Join(tempDir, "default", "test-fw-watch-paths", "initrd")
			differentKey := types.NamespacedName{Name: "different-resource", Namespace: "default"}

			// Watch with different key should fail - proves the path is already watched
			err = watcher.Watch(kernelPath, differentKey)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("already watched"))

			err = watcher.Watch(initrdPath, differentKey)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("already watched"))

			// Watch with same key should succeed (idempotent)
			key := types.NamespacedName{Name: "test-fw-watch-paths", Namespace: "default"}
			Expect(watcher.Watch(kernelPath, key)).To(Succeed())
			Expect(watcher.Watch(initrdPath, key)).To(Succeed())
		})

		It("should unwatch all paths on CR deletion", func() {
			kernelContent := []byte("kernel binary content")
			initrdContent := []byte("initrd binary content")
			kernelHash := sha256sum(kernelContent)
			initrdHash := sha256sum(initrdContent)

			fetcher.fetchContentFunc = func(_ context.Context, _ string) ([]byte, error) {
				return fmt.Appendf(nil, "%s  linux\n%s  initrd.gz\n", kernelHash, initrdHash), nil
			}
			fetcher.downloadFunc = func(_ context.Context, url, destPath string) error {
				if url == debianKernel {
					return os.WriteFile(destPath, kernelContent, 0o644)
				}
				if url == debianInitrd {
					return os.WriteFile(destPath, initrdContent, 0o644)
				}
				return fmt.Errorf("unexpected URL: %s", url)
			}

			Expect(createBootSource(ctx, "test-fw-deletion-unwatch", isobootv1alpha1.BootSourceSpec{
				Kernel: validKernel(),
				Initrd: validInitrd(),
			})).To(Succeed())

			// First reconcile adds finalizer
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-fw-deletion-unwatch", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile downloads and watches
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-fw-deletion-unwatch", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify the path IS watched by attempting to watch with a different key
			// This should FAIL, proving the path is currently watched
			kernelPath := filepath.Join(tempDir, "default", "test-fw-deletion-unwatch", "kernel")
			differentKey := types.NamespacedName{Name: "other-resource", Namespace: "default"}

			err = watcher.Watch(kernelPath, differentKey)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("already watched"))

			// Delete the BootSource
			var bs isobootv1alpha1.BootSource
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "test-fw-deletion-unwatch", Namespace: "default"}, &bs)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &bs)).To(Succeed())

			// Reconcile deletion - this should unwatch paths
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-fw-deletion-unwatch", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// After deletion, the paths should be unwatched
			// The same watch call that FAILED before should now SUCCEED
			// Create a dummy file so the watch doesn't fail due to missing file
			Expect(os.MkdirAll(filepath.Dir(kernelPath), 0o755)).To(Succeed())
			Expect(os.WriteFile(kernelPath, []byte("test"), 0o644)).To(Succeed())

			// This should now succeed because the path was unwatched during deletion
			err = watcher.Watch(kernelPath, differentKey)
			Expect(err).To(Succeed())
		})

		It("should work correctly with nil Watcher", func() {
			kernelContent := []byte("kernel binary content")
			initrdContent := []byte("initrd binary content")
			kernelHash := sha256sum(kernelContent)
			initrdHash := sha256sum(initrdContent)

			fetcher.fetchContentFunc = func(_ context.Context, _ string) ([]byte, error) {
				return fmt.Appendf(nil, "%s  linux\n%s  initrd.gz\n", kernelHash, initrdHash), nil
			}
			fetcher.downloadFunc = func(_ context.Context, url, destPath string) error {
				if url == debianKernel {
					return os.WriteFile(destPath, kernelContent, 0o644)
				}
				if url == debianInitrd {
					return os.WriteFile(destPath, initrdContent, 0o644)
				}
				return fmt.Errorf("unexpected URL: %s", url)
			}

			// Create a reconciler WITHOUT a watcher
			nilWatcherReconciler := &BootSourceReconciler{
				Client:  k8sClient,
				Scheme:  k8sClient.Scheme(),
				BaseDir: tempDir,
				Fetcher: fetcher,
				Watcher: nil, // No watcher
			}

			Expect(createBootSource(ctx, "test-fw-nil-watcher", isobootv1alpha1.BootSourceSpec{
				Kernel: validKernel(),
				Initrd: validInitrd(),
			})).To(Succeed())

			// First reconcile adds finalizer - should not panic
			_, err := nilWatcherReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-fw-nil-watcher", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile downloads - should not panic
			_, err = nilWatcherReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-fw-nil-watcher", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify status is Ready
			var bs isobootv1alpha1.BootSource
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "test-fw-nil-watcher", Namespace: "default"}, &bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseReady))

			// Delete should also work without panic
			Expect(k8sClient.Delete(ctx, &bs)).To(Succeed())
			_, err = nilWatcherReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-fw-nil-watcher", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should watch all resources including kernel, initrd, and firmware", func() {
			kernelContent := []byte("kernel binary content")
			initrdContent := []byte("initrd binary content")
			firmwareContent := []byte("firmware cpio content")
			kernelHash := sha256sum(kernelContent)
			initrdHash := sha256sum(initrdContent)
			firmwareHash := sha512sum(firmwareContent)

			fetcher.fetchContentFunc = func(_ context.Context, url string) ([]byte, error) {
				if url == debianSHA256 {
					return fmt.Appendf(nil, "%s  linux\n%s  initrd.gz\n", kernelHash, initrdHash), nil
				}
				if url == debianFwSHA512 {
					return fmt.Appendf(nil, "%s  firmware.cpio.gz\n", firmwareHash), nil
				}
				return nil, fmt.Errorf("unexpected URL: %s", url)
			}
			fetcher.downloadFunc = func(_ context.Context, url, destPath string) error {
				switch url {
				case debianKernel:
					return os.WriteFile(destPath, kernelContent, 0o644)
				case debianInitrd:
					return os.WriteFile(destPath, initrdContent, 0o644)
				case debianFirmware:
					return os.WriteFile(destPath, firmwareContent, 0o644)
				default:
					return fmt.Errorf("unexpected URL: %s", url)
				}
			}

			Expect(createBootSource(ctx, "test-fw-multiple-resources", isobootv1alpha1.BootSourceSpec{
				Kernel:   validKernel(),
				Initrd:   validInitrd(),
				Firmware: validFirmware(),
			})).To(Succeed())

			// First reconcile adds finalizer
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-fw-multiple-resources", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile downloads and watches all resources
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-fw-multiple-resources", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify status is Ready with all resources
			var bs isobootv1alpha1.BootSource
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "test-fw-multiple-resources", Namespace: "default"}, &bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseReady))
			Expect(bs.Status.Resources).To(HaveKey("kernel"))
			Expect(bs.Status.Resources).To(HaveKey("initrd"))
			Expect(bs.Status.Resources).To(HaveKey("firmware"))
			Expect(bs.Status.Resources).To(HaveKey("initrdWithFirmware"))

			// Verify all paths are watched
			key := types.NamespacedName{Name: "test-fw-multiple-resources", Namespace: "default"}
			kernelPath := bs.Status.Resources["kernel"].Path
			initrdPath := bs.Status.Resources["initrd"].Path
			firmwarePath := bs.Status.Resources["firmware"].Path
			combinedPath := bs.Status.Resources["initrdWithFirmware"].Path

			// Idempotent watch should succeed for all paths
			Expect(watcher.Watch(kernelPath, key)).To(Succeed())
			Expect(watcher.Watch(initrdPath, key)).To(Succeed())
			Expect(watcher.Watch(firmwarePath, key)).To(Succeed())
			Expect(watcher.Watch(combinedPath, key)).To(Succeed())
		})

		It("should watch initrdWithFirmware after successful build", func() {
			isoContent := createTestISO()
			isoHash := sha256sum(isoContent)
			firmwareContent := []byte("firmware cpio content")
			firmwareHash := sha512sum(firmwareContent)

			fetcher.fetchContentFunc = func(_ context.Context, url string) ([]byte, error) {
				if url == debianSHA256 {
					return fmt.Appendf(nil, "%s  mini.iso\n", isoHash), nil
				}
				if url == debianFwSHA512 {
					return fmt.Appendf(nil, "%s  firmware.cpio.gz\n", firmwareHash), nil
				}
				return nil, fmt.Errorf("unexpected URL: %s", url)
			}
			fetcher.downloadFunc = func(_ context.Context, url, destPath string) error {
				switch url {
				case debianMiniISO:
					return os.WriteFile(destPath, isoContent, 0o644)
				case debianFirmware:
					return os.WriteFile(destPath, firmwareContent, 0o644)
				default:
					return fmt.Errorf("unexpected URL: %s", url)
				}
			}

			Expect(createBootSource(ctx, "test-fw-initrd-firmware-watched", isobootv1alpha1.BootSourceSpec{
				ISO: &isobootv1alpha1.ISOSource{
					DownloadableResource: isobootv1alpha1.DownloadableResource{
						URL:       debianMiniISO,
						ShasumURL: ptr.To(debianSHA256),
					},
					KernelPath: "/linux",
					InitrdPath: "/initrd.gz",
				},
				Firmware: validFirmware(),
			})).To(Succeed())

			// First reconcile adds finalizer
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-fw-initrd-firmware-watched", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile downloads ISO, extracts, builds initrdWithFirmware
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-fw-initrd-firmware-watched", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify status is Ready
			var bs isobootv1alpha1.BootSource
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "test-fw-initrd-firmware-watched", Namespace: "default"}, &bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseReady))
			Expect(bs.Status.Resources).To(HaveKey("initrdWithFirmware"))

			// Verify initrdWithFirmware path is watched
			combinedPath := bs.Status.Resources["initrdWithFirmware"].Path
			Expect(combinedPath).NotTo(BeEmpty())

			key := types.NamespacedName{Name: "test-fw-initrd-firmware-watched", Namespace: "default"}
			// Idempotent watch should succeed
			Expect(watcher.Watch(combinedPath, key)).To(Succeed())
		})

		// ── Negative tests ────────────────────────────────────────────────────

		It("should re-download file when watched file is deleted", func() {
			kernelContent := []byte("kernel binary content")
			initrdContent := []byte("initrd binary content")
			kernelHash := sha256sum(kernelContent)
			initrdHash := sha256sum(initrdContent)
			downloadCount := 0

			fetcher.fetchContentFunc = func(_ context.Context, _ string) ([]byte, error) {
				return fmt.Appendf(nil, "%s  linux\n%s  initrd.gz\n", kernelHash, initrdHash), nil
			}
			fetcher.downloadFunc = func(_ context.Context, url, destPath string) error {
				downloadCount++
				if url == debianKernel {
					return os.WriteFile(destPath, kernelContent, 0o644)
				}
				if url == debianInitrd {
					return os.WriteFile(destPath, initrdContent, 0o644)
				}
				return fmt.Errorf("unexpected URL: %s", url)
			}

			Expect(createBootSource(ctx, "test-fw-file-deleted", isobootv1alpha1.BootSourceSpec{
				Kernel: validKernel(),
				Initrd: validInitrd(),
			})).To(Succeed())

			// First reconcile adds finalizer
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-fw-file-deleted", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile downloads resources
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-fw-file-deleted", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify Ready and record download count
			var bs isobootv1alpha1.BootSource
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "test-fw-file-deleted", Namespace: "default"}, &bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseReady))
			initialDownloads := downloadCount

			// Delete the kernel file (simulating file deletion that watcher would detect)
			kernelPath := filepath.Join(tempDir, "default", "test-fw-file-deleted", "kernel")
			Expect(os.Remove(kernelPath)).To(Succeed())

			// Reconcile again - should re-download the missing file
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-fw-file-deleted", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify file was re-downloaded
			Expect(downloadCount).To(BeNumerically(">", initialDownloads))
			Expect(kernelPath).To(BeAnExistingFile())

			// Verify still Ready
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "test-fw-file-deleted", Namespace: "default"}, &bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseReady))
		})

		It("should detect corruption and re-download when watched file is modified", func() {
			kernelContent := []byte("kernel binary content")
			initrdContent := []byte("initrd binary content")
			kernelHash := sha256sum(kernelContent)
			initrdHash := sha256sum(initrdContent)
			downloadCount := 0

			fetcher.fetchContentFunc = func(_ context.Context, _ string) ([]byte, error) {
				return fmt.Appendf(nil, "%s  linux\n%s  initrd.gz\n", kernelHash, initrdHash), nil
			}
			fetcher.downloadFunc = func(_ context.Context, url, destPath string) error {
				downloadCount++
				if url == debianKernel {
					return os.WriteFile(destPath, kernelContent, 0o644)
				}
				if url == debianInitrd {
					return os.WriteFile(destPath, initrdContent, 0o644)
				}
				return fmt.Errorf("unexpected URL: %s", url)
			}

			Expect(createBootSource(ctx, "test-fw-file-corrupted", isobootv1alpha1.BootSourceSpec{
				Kernel: validKernel(),
				Initrd: validInitrd(),
			})).To(Succeed())

			// First reconcile adds finalizer
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-fw-file-corrupted", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile downloads resources
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-fw-file-corrupted", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify Ready
			var bs isobootv1alpha1.BootSource
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "test-fw-file-corrupted", Namespace: "default"}, &bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseReady))
			initialDownloads := downloadCount

			// Corrupt the kernel file (simulating modification that watcher would detect)
			kernelPath := filepath.Join(tempDir, "default", "test-fw-file-corrupted", "kernel")
			Expect(os.WriteFile(kernelPath, []byte("corrupted content"), 0o644)).To(Succeed())

			// Reconcile again - should detect hash mismatch and re-download
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-fw-file-corrupted", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify file was re-downloaded
			Expect(downloadCount).To(BeNumerically(">", initialDownloads))

			// Verify content is correct again
			content, err := os.ReadFile(kernelPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(content).To(Equal(kernelContent))

			// Verify back to Ready
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "test-fw-file-corrupted", Namespace: "default"}, &bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseReady))
		})

		It("should not fail reconcile when watch call fails for non-existent path", func() {
			kernelContent := []byte("kernel binary content")
			initrdContent := []byte("initrd binary content")
			kernelHash := sha256sum(kernelContent)
			initrdHash := sha256sum(initrdContent)

			fetcher.fetchContentFunc = func(_ context.Context, _ string) ([]byte, error) {
				return fmt.Appendf(nil, "%s  linux\n%s  initrd.gz\n", kernelHash, initrdHash), nil
			}
			fetcher.downloadFunc = func(_ context.Context, url, destPath string) error {
				if url == debianKernel {
					return os.WriteFile(destPath, kernelContent, 0o644)
				}
				if url == debianInitrd {
					return os.WriteFile(destPath, initrdContent, 0o644)
				}
				return fmt.Errorf("unexpected URL: %s", url)
			}

			Expect(createBootSource(ctx, "test-fw-watch-error", isobootv1alpha1.BootSourceSpec{
				Kernel: validKernel(),
				Initrd: validInitrd(),
			})).To(Succeed())

			// First reconcile adds finalizer
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-fw-watch-error", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile downloads resources
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-fw-watch-error", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify Ready
			var bs isobootv1alpha1.BootSource
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "test-fw-watch-error", Namespace: "default"}, &bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseReady))

			// Delete the files so watch will fail on next reconcile
			kernelPath := filepath.Join(tempDir, "default", "test-fw-watch-error", "kernel")
			initrdPath := filepath.Join(tempDir, "default", "test-fw-watch-error", "initrd")
			Expect(os.Remove(kernelPath)).To(Succeed())
			Expect(os.Remove(initrdPath)).To(Succeed())

			// Reconcile - watch will fail for paths but reconcile should still succeed
			// (it will re-download the files, then try to watch them)
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-fw-watch-error", Namespace: "default"},
			})
			// Reconcile should not return an error even if watch fails
			Expect(err).NotTo(HaveOccurred())

			// Should still reach Ready after re-downloading
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "test-fw-watch-error", Namespace: "default"}, &bs)).To(Succeed())
			Expect(bs.Status.Phase).To(Equal(isobootv1alpha1.BootSourcePhaseReady))
		})
	})
})
