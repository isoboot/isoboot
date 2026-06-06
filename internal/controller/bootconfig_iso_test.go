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
	"os"
	"path"
	"path/filepath"

	diskfs "github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/disk"
	"github.com/diskfs/go-diskfs/filesystem"
	"github.com/diskfs/go-diskfs/filesystem/iso9660"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	isobootgithubiov1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
)

// writeTestISO creates a small ISO9660 image (Rock Ridge, so lowercase nested
// paths survive, as on real Linux ISOs) at isoPath containing files (path->content).
func writeTestISO(isoPath string, files map[string]string) error {
	// ISO9660 requires a 2048-byte logical block size.
	d, err := diskfs.Create(isoPath, 10<<20, diskfs.SectorSize(2048))
	if err != nil {
		return err
	}
	fsys, err := d.CreateFilesystem(disk.FilesystemSpec{Partition: 0, FSType: filesystem.TypeISO9660})
	if err != nil {
		return err
	}
	made := map[string]bool{}
	for p := range files {
		dir := path.Dir("/" + p)
		if dir != "/" && !made[dir] {
			if err := fsys.Mkdir(dir); err != nil {
				return err
			}
			made[dir] = true
		}
	}
	for p, content := range files {
		f, err := fsys.OpenFile("/"+p, os.O_CREATE|os.O_RDWR)
		if err != nil {
			return err
		}
		if _, err := f.Write([]byte(content)); err != nil {
			return err
		}
	}
	iso, ok := fsys.(*iso9660.FileSystem)
	if !ok {
		return fmt.Errorf("not an iso9660 filesystem")
	}
	return iso.Finalize(iso9660.FinalizeOptions{RockRidge: true})
}

var _ = Describe("BootConfig Controller ISO mode", func() {
	var (
		ctx        context.Context
		dataDir    string
		reconciler *BootConfigReconciler
	)

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		dataDir, err = os.MkdirTemp("", "isoboot-iso-test-*")
		Expect(err).NotTo(HaveOccurred())
		reconciler = &BootConfigReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), DataDir: dataDir}
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

	makeISOArtifact := func(name string, phase isobootgithubiov1alpha1.BootArtifactPhase) func() {
		a := &isobootgithubiov1alpha1.BootArtifact{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			Spec:       isobootgithubiov1alpha1.BootArtifactSpec{URL: "https://example.com/test.iso", SHA256: new(validSHA256)},
		}
		ExpectWithOffset(1, k8sClient.Create(ctx, a)).To(Succeed())
		a.Status.Phase = phase
		ExpectWithOffset(1, k8sClient.Status().Update(ctx, a)).To(Succeed())
		return func() {
			obj := &isobootgithubiov1alpha1.BootArtifact{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, obj); err == nil {
				_ = k8sClient.Delete(ctx, obj)
			}
		}
	}

	// readyISOArtifact creates a Ready ISO artifact and writes a real test ISO to disk.
	readyISOArtifact := func(name string, files map[string]string) func() {
		cleanup := makeISOArtifact(name, isobootgithubiov1alpha1.BootArtifactPhaseReady)
		dir := filepath.Join(dataDir, "artifacts", name)
		ExpectWithOffset(1, os.MkdirAll(dir, 0o755)).To(Succeed())
		ExpectWithOffset(1, writeTestISO(filepath.Join(dir, "test.iso"), files)).To(Succeed())
		return cleanup
	}

	makeISOConfig := func(name, artifactRef, kernelPath, initrdPath string) func() {
		bc := &isobootgithubiov1alpha1.BootConfig{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			Spec: isobootgithubiov1alpha1.BootConfigSpec{
				ISO: &isobootgithubiov1alpha1.BootConfigISOSpec{ArtifactRef: artifactRef, KernelPath: kernelPath, InitrdPath: initrdPath},
			},
		}
		ExpectWithOffset(1, k8sClient.Create(ctx, bc)).To(Succeed())
		return func() {
			obj := &isobootgithubiov1alpha1.BootConfig{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, obj); err == nil {
				_ = k8sClient.Delete(ctx, obj)
			}
		}
	}

	contents := map[string]string{"casper/vmlinuz": "KERNEL-BYTES", "casper/initrd": "INITRD-BYTES"}

	It("extracts kernel and initrd when the ISO artifact is Ready", func() {
		defer readyISOArtifact("iso-ok", contents)()
		defer makeISOConfig("iso-bc-ok", "iso-ok", "casper/vmlinuz", "casper/initrd")()

		result, err := doReconcile("iso-bc-ok")
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeZero())
		Expect(getStatus("iso-bc-ok").Phase).To(Equal(isobootgithubiov1alpha1.BootConfigPhaseReady))

		kernel, err := os.ReadFile(filepath.Join(dataDir, "boot", "iso-bc-ok", "vmlinuz"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(kernel)).To(Equal("KERNEL-BYTES"))
		initrd, err := os.ReadFile(filepath.Join(dataDir, "boot", "iso-bc-ok", "initrd"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(initrd)).To(Equal("INITRD-BYTES"))

		// The ISO is also served as image.iso — a sibling symlink to the artifact
		// that resolves to the real file (so installers can fetch their root fs).
		isoLink := filepath.Join(dataDir, "boot", "iso-bc-ok", "image.iso")
		target, err := os.Readlink(isoLink)
		Expect(err).NotTo(HaveOccurred())
		Expect(target).To(Equal(filepath.Join("..", "..", "artifacts", "iso-ok", "test.iso")))
		info, err := os.Stat(isoLink) // follows the symlink
		Expect(err).NotTo(HaveOccurred())
		Expect(info.IsDir()).To(BeFalse())
	})

	It("does not re-extract on repeated reconcile when up to date", func() {
		defer readyISOArtifact("iso-idem", contents)()
		defer makeISOConfig("iso-bc-idem", "iso-idem", "casper/vmlinuz", "casper/initrd")()

		_, err := doReconcile("iso-bc-idem")
		Expect(err).NotTo(HaveOccurred())
		Expect(getStatus("iso-bc-idem").Phase).To(Equal(isobootgithubiov1alpha1.BootConfigPhaseReady))
		vmlinuzPath := filepath.Join(dataDir, "boot", "iso-bc-idem", "vmlinuz")
		before, err := os.Stat(vmlinuzPath)
		Expect(err).NotTo(HaveOccurred())

		// A second reconcile must leave the extracted file in place (same inode),
		// proving it was not re-extracted.
		_, err = doReconcile("iso-bc-idem")
		Expect(err).NotTo(HaveOccurred())
		after, err := os.Stat(vmlinuzPath)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.SameFile(before, after)).To(BeTrue())
	})

	It("is Pending when the ISO artifact is not Ready", func() {
		defer makeISOArtifact("iso-pending", isobootgithubiov1alpha1.BootArtifactPhasePending)()
		defer makeISOConfig("iso-bc-pending", "iso-pending", "casper/vmlinuz", "casper/initrd")()

		result, err := doReconcile("iso-bc-pending")
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).NotTo(BeZero())
		Expect(getStatus("iso-bc-pending").Phase).To(Equal(isobootgithubiov1alpha1.BootConfigPhasePending))
	})

	It("is Error when the ISO artifact does not exist", func() {
		defer makeISOConfig("iso-bc-missing", "no-such-iso", "casper/vmlinuz", "casper/initrd")()
		_, err := doReconcile("iso-bc-missing")
		Expect(err).NotTo(HaveOccurred())
		Expect(getStatus("iso-bc-missing").Phase).To(Equal(isobootgithubiov1alpha1.BootConfigPhaseError))
	})

	It("is Error when the kernel path is not in the ISO", func() {
		defer readyISOArtifact("iso-badk", contents)()
		defer makeISOConfig("iso-bc-badk", "iso-badk", "casper/nope", "casper/initrd")()
		_, err := doReconcile("iso-bc-badk")
		Expect(err).NotTo(HaveOccurred())
		Expect(getStatus("iso-bc-badk").Phase).To(Equal(isobootgithubiov1alpha1.BootConfigPhaseError))
	})

	It("is Error when the initrd path is not in the ISO", func() {
		defer readyISOArtifact("iso-badi", contents)()
		defer makeISOConfig("iso-bc-badi", "iso-badi", "casper/vmlinuz", "casper/nope")()
		_, err := doReconcile("iso-bc-badi")
		Expect(err).NotTo(HaveOccurred())
		Expect(getStatus("iso-bc-badi").Phase).To(Equal(isobootgithubiov1alpha1.BootConfigPhaseError))
	})

	It("is Error when a path contains traversal", func() {
		defer readyISOArtifact("iso-trav", contents)()
		defer makeISOConfig("iso-bc-trav", "iso-trav", "../etc/passwd", "casper/initrd")()
		_, err := doReconcile("iso-bc-trav")
		Expect(err).NotTo(HaveOccurred())
		Expect(getStatus("iso-bc-trav").Phase).To(Equal(isobootgithubiov1alpha1.BootConfigPhaseError))
	})
})
