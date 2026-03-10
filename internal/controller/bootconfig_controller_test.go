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
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	isobootgithubiov1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
)

var _ = Describe("BootConfig Controller", func() {
	Context("Validation", func() {
		ctx := context.Background()

		newConfig := func(name string, spec isobootgithubiov1alpha1.BootConfigSpec) *isobootgithubiov1alpha1.BootConfig {
			return &isobootgithubiov1alpha1.BootConfig{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
				Spec:       spec,
			}
		}

		DescribeTable("should accept valid specs",
			func(name string, spec isobootgithubiov1alpha1.BootConfigSpec) {
				resource := newConfig(name, spec)
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
			},
			Entry("mode A: kernel and initrd", "valid-mode-a", isobootgithubiov1alpha1.BootConfigSpec{
				KernelRef: ptr.To("my-kernel"),
				InitrdRef: ptr.To("my-initrd"),
			}),
			Entry("mode A: with firmware", "valid-mode-a-fw", isobootgithubiov1alpha1.BootConfigSpec{
				KernelRef:   ptr.To("my-kernel"),
				InitrdRef:   ptr.To("my-initrd"),
				FirmwareRef: ptr.To("my-firmware"),
			}),
			Entry("mode B: iso", "valid-mode-b", isobootgithubiov1alpha1.BootConfigSpec{
				ISO: &isobootgithubiov1alpha1.BootConfigISOSpec{
					ArtifactRef: "my-iso",
					KernelPath:  "casper/vmlinuz",
					InitrdPath:  "casper/initrd",
				},
			}),
		)

		DescribeTable("should reject invalid specs",
			func(name string, spec isobootgithubiov1alpha1.BootConfigSpec) {
				resource := newConfig(name, spec)
				Expect(k8sClient.Create(ctx, resource)).NotTo(Succeed())
			},
			Entry("neither mode", "no-mode", isobootgithubiov1alpha1.BootConfigSpec{}),
			Entry("both modes", "both-modes", isobootgithubiov1alpha1.BootConfigSpec{
				KernelRef: ptr.To("my-kernel"),
				InitrdRef: ptr.To("my-initrd"),
				ISO: &isobootgithubiov1alpha1.BootConfigISOSpec{
					ArtifactRef: "my-iso",
					KernelPath:  "casper/vmlinuz",
					InitrdPath:  "casper/initrd",
				},
			}),
			Entry("kernelRef without initrdRef", "kernel-only", isobootgithubiov1alpha1.BootConfigSpec{
				KernelRef: ptr.To("my-kernel"),
			}),
			Entry("initrdRef without kernelRef", "initrd-only", isobootgithubiov1alpha1.BootConfigSpec{
				InitrdRef: ptr.To("my-initrd"),
			}),
			Entry("firmwareRef with iso mode", "fw-with-iso", isobootgithubiov1alpha1.BootConfigSpec{
				FirmwareRef: ptr.To("my-firmware"),
				ISO: &isobootgithubiov1alpha1.BootConfigISOSpec{
					ArtifactRef: "my-iso",
					KernelPath:  "casper/vmlinuz",
					InitrdPath:  "casper/initrd",
				},
			}),
		)
	})

	Context("Reconcile", func() {
		var (
			ctx        context.Context
			dataDir    string
			reconciler *BootConfigReconciler
		)

		BeforeEach(func() {
			ctx = context.Background()
			var err error
			dataDir, err = os.MkdirTemp("", "isoboot-bc-test-*")
			Expect(err).NotTo(HaveOccurred())
			reconciler = &BootConfigReconciler{
				Client:  k8sClient,
				Scheme:  k8sClient.Scheme(),
				DataDir: dataDir,
			}
		})

		AfterEach(func() { Expect(os.RemoveAll(dataDir)).To(Succeed()) })

		doReconcile := func(name string) (reconcile.Result, error) {
			return reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: name, Namespace: "default"},
			})
		}

		getStatus := func(name string) isobootgithubiov1alpha1.BootConfigStatus {
			var bc isobootgithubiov1alpha1.BootConfig
			ExpectWithOffset(1, k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, &bc)).To(Succeed())
			return bc.Status
		}

		createArtifact := func(name string, phase isobootgithubiov1alpha1.BootArtifactPhase, url string) {
			a := &isobootgithubiov1alpha1.BootArtifact{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
				Spec:       isobootgithubiov1alpha1.BootArtifactSpec{URL: url, SHA256: ptr.To(validSHA256)},
			}
			ExpectWithOffset(1, k8sClient.Create(ctx, a)).To(Succeed())
			a.Status.Phase = phase
			ExpectWithOffset(1, k8sClient.Status().Update(ctx, a)).To(Succeed())
		}

		deleteResource := func(name string) {
			bc := &isobootgithubiov1alpha1.BootConfig{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, bc); err == nil {
				_ = k8sClient.Delete(ctx, bc)
			}
		}

		deleteArtifact := func(name string) {
			a := &isobootgithubiov1alpha1.BootArtifact{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, a); err == nil {
				_ = k8sClient.Delete(ctx, a)
			}
		}

		createBootConfig := func(name, kernelRef, initrdRef string) {
			bc := &isobootgithubiov1alpha1.BootConfig{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
				Spec: isobootgithubiov1alpha1.BootConfigSpec{
					KernelRef: ptr.To(kernelRef),
					InitrdRef: ptr.To(initrdRef),
				},
			}
			ExpectWithOffset(1, k8sClient.Create(ctx, bc)).To(Succeed())
		}

		// Place artifact files on disk so symlinks resolve
		placeArtifactFile := func(artifactName, filename string) {
			dir := filepath.Join(dataDir, "artifacts", artifactName)
			ExpectWithOffset(1, os.MkdirAll(dir, 0o755)).To(Succeed())
			ExpectWithOffset(1, os.WriteFile(filepath.Join(dir, filename), []byte("data"), 0o644)).To(Succeed())
		}

		It("should return without error for deleted resource", func() {
			result, err := doReconcile("nonexistent-bc")
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))
		})

		It("should set Ready and create symlinks when all artifacts are Ready", func() {
			kernelName := "bc-test-kernel"
			initrdName := "bc-test-initrd"
			bcName := "bc-all-ready"

			createArtifact(kernelName, isobootgithubiov1alpha1.BootArtifactPhaseReady, "https://example.com/vmlinuz")
			defer deleteArtifact(kernelName)
			placeArtifactFile(kernelName, "vmlinuz")

			createArtifact(initrdName, isobootgithubiov1alpha1.BootArtifactPhaseReady, "https://example.com/initrd.img")
			defer deleteArtifact(initrdName)
			placeArtifactFile(initrdName, "initrd.img")

			createBootConfig(bcName, kernelName, initrdName)
			defer deleteResource(bcName)

			result, err := doReconcile(bcName)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			status := getStatus(bcName)
			Expect(status.Phase).To(Equal(isobootgithubiov1alpha1.BootConfigPhaseReady))

			// Verify symlinks exist and point to correct targets
			kernelLink := filepath.Join(dataDir, "boot", bcName, "kernel", "vmlinuz")
			target, err := os.Readlink(kernelLink)
			Expect(err).NotTo(HaveOccurred())
			Expect(target).To(Equal(filepath.Join("..", "..", "..", "artifacts", kernelName, "vmlinuz")))

			initrdLink := filepath.Join(dataDir, "boot", bcName, "initrd", "initrd.img")
			target, err = os.Readlink(initrdLink)
			Expect(err).NotTo(HaveOccurred())
			Expect(target).To(Equal(filepath.Join("..", "..", "..", "artifacts", initrdName, "initrd.img")))

			// Verify symlinks resolve to actual files
			_, err = os.Stat(kernelLink)
			Expect(err).NotTo(HaveOccurred())
			_, err = os.Stat(initrdLink)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should set Pending when artifact is not Ready", func() {
			kernelName := "bc-pend-kernel"
			initrdName := "bc-pend-initrd"
			bcName := "bc-pending"

			createArtifact(kernelName, isobootgithubiov1alpha1.BootArtifactPhaseReady, "https://example.com/vmlinuz")
			defer deleteArtifact(kernelName)

			createArtifact(initrdName, isobootgithubiov1alpha1.BootArtifactPhaseDownloading, "https://example.com/initrd.img")
			defer deleteArtifact(initrdName)

			createBootConfig(bcName, kernelName, initrdName)
			defer deleteResource(bcName)

			result, err := doReconcile(bcName)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).NotTo(BeZero())

			status := getStatus(bcName)
			Expect(status.Phase).To(Equal(isobootgithubiov1alpha1.BootConfigPhasePending))
		})

		It("should set Error when artifact is missing", func() {
			bcName := "bc-missing-art"

			createBootConfig(bcName, "does-not-exist-kernel", "does-not-exist-initrd")
			defer deleteResource(bcName)

			result, err := doReconcile(bcName)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).NotTo(BeZero())

			status := getStatus(bcName)
			Expect(status.Phase).To(Equal(isobootgithubiov1alpha1.BootConfigPhaseError))
			Expect(status.Message).To(ContainSubstring("not found"))
		})

		It("should be idempotent on repeated reconcile", func() {
			kernelName := "bc-idem-kernel"
			initrdName := "bc-idem-initrd"
			bcName := "bc-idempotent"

			createArtifact(kernelName, isobootgithubiov1alpha1.BootArtifactPhaseReady, "https://example.com/vmlinuz")
			defer deleteArtifact(kernelName)
			placeArtifactFile(kernelName, "vmlinuz")

			createArtifact(initrdName, isobootgithubiov1alpha1.BootArtifactPhaseReady, "https://example.com/initrd.img")
			defer deleteArtifact(initrdName)
			placeArtifactFile(initrdName, "initrd.img")

			createBootConfig(bcName, kernelName, initrdName)
			defer deleteResource(bcName)

			// First reconcile
			_, err := doReconcile(bcName)
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile — should not error
			result, err := doReconcile(bcName)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())
			Expect(getStatus(bcName).Phase).To(Equal(isobootgithubiov1alpha1.BootConfigPhaseReady))
		})
	})

	Context("When reconciling a resource", func() {
		const resourceName = "test-bootconfig"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}
		bootconfig := &isobootgithubiov1alpha1.BootConfig{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind BootConfig")
			err := k8sClient.Get(ctx, typeNamespacedName, bootconfig)
			if err != nil && errors.IsNotFound(err) {
				resource := &isobootgithubiov1alpha1.BootConfig{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: isobootgithubiov1alpha1.BootConfigSpec{
						KernelRef: ptr.To("test-kernel"),
						InitrdRef: ptr.To("test-initrd"),
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &isobootgithubiov1alpha1.BootConfig{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance BootConfig")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &BootConfigReconciler{
				Client:  k8sClient,
				Scheme:  k8sClient.Scheme(),
				DataDir: os.TempDir(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			// Error is expected since test-kernel artifact doesn't exist
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
