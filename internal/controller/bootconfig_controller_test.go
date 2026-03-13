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
	"k8s.io/apimachinery/pkg/types"
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
				Kernel: &isobootgithubiov1alpha1.BootConfigKernelSpec{Ref: "my-kernel"},
				Initrd: &isobootgithubiov1alpha1.BootConfigInitrdSpec{Ref: "my-initrd"},
			}),
			Entry("mode A: with firmware", "valid-mode-a-fw", isobootgithubiov1alpha1.BootConfigSpec{
				Kernel:   &isobootgithubiov1alpha1.BootConfigKernelSpec{Ref: "my-kernel"},
				Initrd:   &isobootgithubiov1alpha1.BootConfigInitrdSpec{Ref: "my-initrd"},
				Firmware: &isobootgithubiov1alpha1.BootConfigFirmwareSpec{Ref: "my-firmware"},
			}),
			Entry("mode A: with kernel args", "valid-mode-a-args", isobootgithubiov1alpha1.BootConfigSpec{
				Kernel: &isobootgithubiov1alpha1.BootConfigKernelSpec{Ref: "my-kernel", Args: "-- quiet"},
				Initrd: &isobootgithubiov1alpha1.BootConfigInitrdSpec{Ref: "my-initrd"},
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
				Kernel: &isobootgithubiov1alpha1.BootConfigKernelSpec{Ref: "my-kernel"},
				Initrd: &isobootgithubiov1alpha1.BootConfigInitrdSpec{Ref: "my-initrd"},
				ISO: &isobootgithubiov1alpha1.BootConfigISOSpec{
					ArtifactRef: "my-iso",
					KernelPath:  "casper/vmlinuz",
					InitrdPath:  "casper/initrd",
				},
			}),
			Entry("kernel without initrd", "kernel-only", isobootgithubiov1alpha1.BootConfigSpec{
				Kernel: &isobootgithubiov1alpha1.BootConfigKernelSpec{Ref: "my-kernel"},
			}),
			Entry("initrd without kernel", "initrd-only", isobootgithubiov1alpha1.BootConfigSpec{
				Initrd: &isobootgithubiov1alpha1.BootConfigInitrdSpec{Ref: "my-initrd"},
			}),
			Entry("firmware only", "fw-only", isobootgithubiov1alpha1.BootConfigSpec{
				Firmware: &isobootgithubiov1alpha1.BootConfigFirmwareSpec{Ref: "my-firmware"},
			}),
			Entry("firmware with iso mode", "fw-with-iso", isobootgithubiov1alpha1.BootConfigSpec{
				Firmware: &isobootgithubiov1alpha1.BootConfigFirmwareSpec{Ref: "my-firmware"},
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
				Spec:       isobootgithubiov1alpha1.BootArtifactSpec{URL: url, SHA256: new(validSHA256)},
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
					Kernel: &isobootgithubiov1alpha1.BootConfigKernelSpec{Ref: kernelRef},
					Initrd: &isobootgithubiov1alpha1.BootConfigInitrdSpec{Ref: initrdRef},
				},
			}
			ExpectWithOffset(1, k8sClient.Create(ctx, bc)).To(Succeed())
		}

		// Creates both artifacts as Ready with files on disk; returns cleanup func
		setupReadyPair := func(kernelName, initrdName string) func() {
			createArtifact(kernelName, isobootgithubiov1alpha1.BootArtifactPhaseReady, "https://example.com/vmlinuz")
			dir := filepath.Join(dataDir, "artifacts", kernelName)
			ExpectWithOffset(1, os.MkdirAll(dir, 0o755)).To(Succeed())
			ExpectWithOffset(1, os.WriteFile(filepath.Join(dir, "vmlinuz"), []byte("data"), 0o644)).To(Succeed())

			createArtifact(initrdName, isobootgithubiov1alpha1.BootArtifactPhaseReady, "https://example.com/initrd.img")
			dir = filepath.Join(dataDir, "artifacts", initrdName)
			ExpectWithOffset(1, os.MkdirAll(dir, 0o755)).To(Succeed())
			ExpectWithOffset(1, os.WriteFile(filepath.Join(dir, "initrd.img"), []byte("data"), 0o644)).To(Succeed())

			return func() { deleteArtifact(kernelName); deleteArtifact(initrdName) }
		}

		// Verifies symlinks exist, point to correct targets, and resolve to files
		expectSymlinksReady := func(bcName, kernelArtifact, initrdArtifact string) {
			kernelLink := filepath.Join(dataDir, "boot", bcName, "kernel", "vmlinuz")
			target, err := os.Readlink(kernelLink)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())
			ExpectWithOffset(1, target).To(Equal(filepath.Join("..", "..", "..", "artifacts", kernelArtifact, "vmlinuz")))
			_, err = os.Stat(kernelLink)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			initrdLink := filepath.Join(dataDir, "boot", bcName, "initrd", "initrd.img")
			target, err = os.Readlink(initrdLink)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())
			ExpectWithOffset(1, target).To(Equal(filepath.Join("..", "..", "..", "artifacts", initrdArtifact, "initrd.img")))
			_, err = os.Stat(initrdLink)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())
		}

		It("should return without error for deleted resource", func() {
			result, err := doReconcile("nonexistent-bc")
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))
		})

		It("should create symlinks, clean up on delete, and recreate on re-create", func() {
			kernelName := "bc-life-kernel"
			initrdName := "bc-life-initrd"
			bcName := "bc-lifecycle"

			cleanup := setupReadyPair(kernelName, initrdName)
			defer cleanup()

			// 1. Create and reconcile — symlinks should exist with correct targets
			createBootConfig(bcName, kernelName, initrdName)

			result, err := doReconcile(bcName)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())
			Expect(getStatus(bcName).Phase).To(Equal(isobootgithubiov1alpha1.BootConfigPhaseReady))
			expectSymlinksReady(bcName, kernelName, initrdName)

			// 2. Delete and reconcile — boot directory should be removed
			deleteResource(bcName)

			result, err = doReconcile(bcName)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			_, err = os.Stat(filepath.Join(dataDir, "boot", bcName))
			Expect(os.IsNotExist(err)).To(BeTrue())

			// 3. Re-create and reconcile — symlinks should come back
			createBootConfig(bcName, kernelName, initrdName)
			defer deleteResource(bcName)

			result, err = doReconcile(bcName)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())
			Expect(getStatus(bcName).Phase).To(Equal(isobootgithubiov1alpha1.BootConfigPhaseReady))
			expectSymlinksReady(bcName, kernelName, initrdName)
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

		It("should remove stale symlinks when ref filename changes", func() {
			kernelName := "bc-stale-kernel"
			initrdName := "bc-stale-initrd"
			bcName := "bc-stale"

			cleanup := setupReadyPair(kernelName, initrdName)
			defer cleanup()

			createBootConfig(bcName, kernelName, initrdName)
			defer deleteResource(bcName)

			_, err := doReconcile(bcName)
			Expect(err).NotTo(HaveOccurred())

			// Simulate a ref change: remove correct symlink and plant a stale one
			correctLink := filepath.Join(dataDir, "boot", bcName, "kernel", "vmlinuz")
			Expect(os.Remove(correctLink)).To(Succeed())
			staleLink := filepath.Join(dataDir, "boot", bcName, "kernel", "old-vmlinuz")
			Expect(os.Symlink("../../../artifacts/old/old-vmlinuz", staleLink)).To(Succeed())

			// Reconcile — should create correct symlink and remove stale one
			_, err = doReconcile(bcName)
			Expect(err).NotTo(HaveOccurred())

			_, err = os.Lstat(staleLink)
			Expect(os.IsNotExist(err)).To(BeTrue())

			// Correct symlink should be recreated
			expectSymlinksReady(bcName, kernelName, initrdName)
		})

		It("should be idempotent on repeated reconcile", func() {
			kernelName := "bc-idem-kernel"
			initrdName := "bc-idem-initrd"
			bcName := "bc-idempotent"

			cleanup := setupReadyPair(kernelName, initrdName)
			defer cleanup()

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

		It("should concatenate initrd + firmware when firmware is set", func() {
			kernelName := "bc-fw-kernel"
			initrdName := "bc-fw-initrd"
			firmwareName := "bc-fw-firmware"
			bcName := "bc-firmware"

			// Create kernel artifact and file
			createArtifact(kernelName, isobootgithubiov1alpha1.BootArtifactPhaseReady, "https://example.com/vmlinuz")
			defer deleteArtifact(kernelName)
			kernelDir := filepath.Join(dataDir, "artifacts", kernelName)
			Expect(os.MkdirAll(kernelDir, 0o755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(kernelDir, "vmlinuz"), []byte("kernel-data"), 0o644)).To(Succeed())

			// Create initrd artifact and file
			createArtifact(initrdName, isobootgithubiov1alpha1.BootArtifactPhaseReady, "https://example.com/initrd.gz")
			defer deleteArtifact(initrdName)
			initrdDir := filepath.Join(dataDir, "artifacts", initrdName)
			Expect(os.MkdirAll(initrdDir, 0o755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(initrdDir, "initrd.gz"), []byte("initrd-data"), 0o644)).To(Succeed())

			// Create firmware artifact and file
			createArtifact(firmwareName, isobootgithubiov1alpha1.BootArtifactPhaseReady, "https://example.com/firmware.cpio.gz")
			defer deleteArtifact(firmwareName)
			fwDir := filepath.Join(dataDir, "artifacts", firmwareName)
			Expect(os.MkdirAll(fwDir, 0o755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(fwDir, "firmware.cpio.gz"), []byte("firmware-data"), 0o644)).To(Succeed())

			// Create BootConfig with firmware
			bc := &isobootgithubiov1alpha1.BootConfig{
				ObjectMeta: metav1.ObjectMeta{Name: bcName, Namespace: "default"},
				Spec: isobootgithubiov1alpha1.BootConfigSpec{
					Kernel:   &isobootgithubiov1alpha1.BootConfigKernelSpec{Ref: kernelName},
					Initrd:   &isobootgithubiov1alpha1.BootConfigInitrdSpec{Ref: initrdName},
					Firmware: &isobootgithubiov1alpha1.BootConfigFirmwareSpec{Ref: firmwareName},
				},
			}
			Expect(k8sClient.Create(ctx, bc)).To(Succeed())
			defer deleteResource(bcName)

			result, err := doReconcile(bcName)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())
			Expect(getStatus(bcName).Phase).To(Equal(isobootgithubiov1alpha1.BootConfigPhaseReady))

			// Kernel should be a symlink
			kernelLink := filepath.Join(dataDir, "boot", bcName, "kernel", "vmlinuz")
			target, err := os.Readlink(kernelLink)
			Expect(err).NotTo(HaveOccurred())
			Expect(target).To(Equal(filepath.Join("..", "..", "..", "artifacts", kernelName, "vmlinuz")))

			// Initrd should be a regular file (concatenated), not a symlink
			combinedPath := filepath.Join(dataDir, "boot", bcName, "initrd", "initrd.gz")
			info, err := os.Lstat(combinedPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(info.Mode().IsRegular()).To(BeTrue())

			// Verify concatenated content
			content, err := os.ReadFile(combinedPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(content)).To(Equal("initrd-datafirmware-data"))
		})

		It("should be idempotent with firmware on repeated reconcile", func() {
			kernelName := "bc-fwidem-kernel"
			initrdName := "bc-fwidem-initrd"
			firmwareName := "bc-fwidem-firmware"
			bcName := "bc-fw-idempotent"

			// Create kernel artifact and file
			createArtifact(kernelName, isobootgithubiov1alpha1.BootArtifactPhaseReady, "https://example.com/vmlinuz")
			defer deleteArtifact(kernelName)
			kernelDir := filepath.Join(dataDir, "artifacts", kernelName)
			Expect(os.MkdirAll(kernelDir, 0o755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(kernelDir, "vmlinuz"), []byte("kernel-data"), 0o644)).To(Succeed())

			// Create initrd artifact and file
			createArtifact(initrdName, isobootgithubiov1alpha1.BootArtifactPhaseReady, "https://example.com/initrd.gz")
			defer deleteArtifact(initrdName)
			initrdArtDir := filepath.Join(dataDir, "artifacts", initrdName)
			Expect(os.MkdirAll(initrdArtDir, 0o755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(initrdArtDir, "initrd.gz"), []byte("initrd-data"), 0o644)).To(Succeed())

			// Create firmware artifact and file
			createArtifact(firmwareName, isobootgithubiov1alpha1.BootArtifactPhaseReady, "https://example.com/firmware.cpio.gz")
			defer deleteArtifact(firmwareName)
			fwDir := filepath.Join(dataDir, "artifacts", firmwareName)
			Expect(os.MkdirAll(fwDir, 0o755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(fwDir, "firmware.cpio.gz"), []byte("firmware-data"), 0o644)).To(Succeed())

			bc := &isobootgithubiov1alpha1.BootConfig{
				ObjectMeta: metav1.ObjectMeta{Name: bcName, Namespace: "default"},
				Spec: isobootgithubiov1alpha1.BootConfigSpec{
					Kernel:   &isobootgithubiov1alpha1.BootConfigKernelSpec{Ref: kernelName},
					Initrd:   &isobootgithubiov1alpha1.BootConfigInitrdSpec{Ref: initrdName},
					Firmware: &isobootgithubiov1alpha1.BootConfigFirmwareSpec{Ref: firmwareName},
				},
			}
			Expect(k8sClient.Create(ctx, bc)).To(Succeed())
			defer deleteResource(bcName)

			// First reconcile
			_, err := doReconcile(bcName)
			Expect(err).NotTo(HaveOccurred())

			// Record mod time of concatenated file
			combinedPath := filepath.Join(dataDir, "boot", bcName, "initrd", "initrd.gz")
			info, err := os.Stat(combinedPath)
			Expect(err).NotTo(HaveOccurred())
			modTime := info.ModTime()

			// Second reconcile — should not recreate the concatenated file
			result, err := doReconcile(bcName)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())
			Expect(getStatus(bcName).Phase).To(Equal(isobootgithubiov1alpha1.BootConfigPhaseReady))

			info, err = os.Stat(combinedPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(info.ModTime()).To(Equal(modTime))
		})

		It("should set Pending when firmware artifact is not Ready", func() {
			kernelName := "bc-fwpend-kernel"
			initrdName := "bc-fwpend-initrd"
			firmwareName := "bc-fwpend-firmware"
			bcName := "bc-fw-pending"

			createArtifact(kernelName, isobootgithubiov1alpha1.BootArtifactPhaseReady, "https://example.com/vmlinuz")
			defer deleteArtifact(kernelName)

			createArtifact(initrdName, isobootgithubiov1alpha1.BootArtifactPhaseReady, "https://example.com/initrd.gz")
			defer deleteArtifact(initrdName)

			createArtifact(firmwareName, isobootgithubiov1alpha1.BootArtifactPhaseDownloading, "https://example.com/firmware.cpio.gz")
			defer deleteArtifact(firmwareName)

			bc := &isobootgithubiov1alpha1.BootConfig{
				ObjectMeta: metav1.ObjectMeta{Name: bcName, Namespace: "default"},
				Spec: isobootgithubiov1alpha1.BootConfigSpec{
					Kernel:   &isobootgithubiov1alpha1.BootConfigKernelSpec{Ref: kernelName},
					Initrd:   &isobootgithubiov1alpha1.BootConfigInitrdSpec{Ref: initrdName},
					Firmware: &isobootgithubiov1alpha1.BootConfigFirmwareSpec{Ref: firmwareName},
				},
			}
			Expect(k8sClient.Create(ctx, bc)).To(Succeed())
			defer deleteResource(bcName)

			result, err := doReconcile(bcName)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).NotTo(BeZero())

			status := getStatus(bcName)
			Expect(status.Phase).To(Equal(isobootgithubiov1alpha1.BootConfigPhasePending))
			Expect(status.Message).To(ContainSubstring("firmware"))
		})
	})

})
