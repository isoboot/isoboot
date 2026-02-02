package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	isobootv1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
)

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
			Expect(errors.IsInvalid(err)).To(BeTrue())
		})

		It("should reject kernel only (no initrd)", func() {
			err := createBootSource(ctx, "invalid-kernel-only", isobootv1alpha1.BootSourceSpec{
				Kernel: validKernel(),
			})
			Expect(err).To(HaveOccurred())
			Expect(errors.IsInvalid(err)).To(BeTrue())
		})

		It("should reject initrd only (no kernel)", func() {
			err := createBootSource(ctx, "invalid-initrd-only", isobootv1alpha1.BootSourceSpec{
				Initrd: validInitrd(),
			})
			Expect(err).To(HaveOccurred())
			Expect(errors.IsInvalid(err)).To(BeTrue())
		})

		It("should reject iso + kernel (no mixing)", func() {
			err := createBootSource(ctx, "invalid-iso-kernel", isobootv1alpha1.BootSourceSpec{
				ISO:    validISO(),
				Kernel: validKernel(),
			})
			Expect(err).To(HaveOccurred())
			Expect(errors.IsInvalid(err)).To(BeTrue())
		})

		It("should reject iso + initrd (no mixing)", func() {
			err := createBootSource(ctx, "invalid-iso-initrd", isobootv1alpha1.BootSourceSpec{
				ISO:    validISO(),
				Initrd: validInitrd(),
			})
			Expect(err).To(HaveOccurred())
			Expect(errors.IsInvalid(err)).To(BeTrue())
		})

		It("should reject iso + kernel + initrd (no mixing)", func() {
			err := createBootSource(ctx, "invalid-iso-kernel-initrd", isobootv1alpha1.BootSourceSpec{
				ISO:    validISO(),
				Kernel: validKernel(),
				Initrd: validInitrd(),
			})
			Expect(err).To(HaveOccurred())
			Expect(errors.IsInvalid(err)).To(BeTrue())
		})

		It("should reject kernel without any checksum", func() {
			err := createBootSource(ctx, "invalid-no-checksum", isobootv1alpha1.BootSourceSpec{
				Kernel: &isobootv1alpha1.DownloadableResource{
					URL: debianKernel,
				},
				Initrd: validInitrd(),
			})
			Expect(err).To(HaveOccurred())
			Expect(errors.IsInvalid(err)).To(BeTrue())
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
})
