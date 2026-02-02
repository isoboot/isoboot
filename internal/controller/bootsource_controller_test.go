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
	debianBase     = "https://ftp.debian.org/debian/dists/trixie/main/installer-amd64/current/images"
	debianSHA256   = debianBase + "/SHA256SUMS"
	debianFirmware = debianBase + "/netboot/debian-installer/amd64/boot-screens/f9.txt"
)

var _ = Describe("BootSource Controller", func() {
	Context("When reconciling a kernel+initrd resource", func() {
		const resourceName = "test-kernel-initrd"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}
		bootsource := &isobootv1alpha1.BootSource{}

		BeforeEach(func() {
			By("creating a BootSource with kernel+initrd spec")
			err := k8sClient.Get(ctx, typeNamespacedName, bootsource)
			if err != nil && errors.IsNotFound(err) {
				resource := &isobootv1alpha1.BootSource{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: isobootv1alpha1.BootSourceSpec{
						Kernel: &isobootv1alpha1.DownloadableResource{
							URL:       debianBase + "/netboot/debian-installer/amd64/linux",
							ShasumURL: ptr.To(debianSHA256),
						},
						Initrd: &isobootv1alpha1.DownloadableResource{
							URL:       debianBase + "/netboot/debian-installer/amd64/initrd.gz",
							ShasumURL: ptr.To(debianSHA256),
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &isobootv1alpha1.BootSource{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the BootSource")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})

		It("should successfully reconcile the resource", func() {
			controllerReconciler := &BootSourceReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("When reconciling an ISO resource", func() {
		const resourceName = "test-iso"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}
		bootsource := &isobootv1alpha1.BootSource{}

		BeforeEach(func() {
			By("creating a BootSource with ISO spec")
			err := k8sClient.Get(ctx, typeNamespacedName, bootsource)
			if err != nil && errors.IsNotFound(err) {
				resource := &isobootv1alpha1.BootSource{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: isobootv1alpha1.BootSourceSpec{
						ISO: &isobootv1alpha1.ISOSource{
							DownloadableResource: isobootv1alpha1.DownloadableResource{
								URL:       debianBase + "/netboot/mini.iso",
								ShasumURL: ptr.To(debianSHA256),
							},
							KernelPath: "/linux",
							InitrdPath: "/initrd.gz",
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &isobootv1alpha1.BootSource{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the BootSource")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})

		It("should successfully reconcile the resource", func() {
			controllerReconciler := &BootSourceReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("When reconciling a kernel+initrd resource with firmware", func() {
		const resourceName = "test-kernel-initrd-firmware"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}
		bootsource := &isobootv1alpha1.BootSource{}

		BeforeEach(func() {
			By("creating a BootSource with kernel+initrd+firmware spec")
			err := k8sClient.Get(ctx, typeNamespacedName, bootsource)
			if err != nil && errors.IsNotFound(err) {
				resource := &isobootv1alpha1.BootSource{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: isobootv1alpha1.BootSourceSpec{
						Kernel: &isobootv1alpha1.DownloadableResource{
							URL:       debianBase + "/netboot/debian-installer/amd64/linux",
							ShasumURL: ptr.To(debianSHA256),
						},
						Initrd: &isobootv1alpha1.DownloadableResource{
							URL:       debianBase + "/netboot/debian-installer/amd64/initrd.gz",
							ShasumURL: ptr.To(debianSHA256),
						},
						Firmware: &isobootv1alpha1.DownloadableResource{
							URL:       debianFirmware,
							ShasumURL: ptr.To(debianSHA256),
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &isobootv1alpha1.BootSource{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the BootSource")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})

		It("should successfully reconcile the resource", func() {
			controllerReconciler := &BootSourceReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("When reconciling an ISO resource with firmware", func() {
		const resourceName = "test-iso-firmware"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}
		bootsource := &isobootv1alpha1.BootSource{}

		BeforeEach(func() {
			By("creating a BootSource with ISO+firmware spec")
			err := k8sClient.Get(ctx, typeNamespacedName, bootsource)
			if err != nil && errors.IsNotFound(err) {
				resource := &isobootv1alpha1.BootSource{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: isobootv1alpha1.BootSourceSpec{
						ISO: &isobootv1alpha1.ISOSource{
							DownloadableResource: isobootv1alpha1.DownloadableResource{
								URL:       debianBase + "/netboot/mini.iso",
								ShasumURL: ptr.To(debianSHA256),
							},
							KernelPath: "/linux",
							InitrdPath: "/initrd.gz",
						},
						Firmware: &isobootv1alpha1.DownloadableResource{
							URL:       debianFirmware,
							ShasumURL: ptr.To(debianSHA256),
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &isobootv1alpha1.BootSource{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the BootSource")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})

		It("should successfully reconcile the resource", func() {
			controllerReconciler := &BootSourceReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
