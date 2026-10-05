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
	"path/filepath"
	"strings"
	"time"

	diskfs "github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/disk"
	"github.com/diskfs/go-diskfs/filesystem"
	"github.com/diskfs/go-diskfs/filesystem/iso9660"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	isobootgithubiov1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
)

// writeTestISO creates a small ISO9660 image with Rock Ridge (so lowercase
// nested names and symlinks survive, as on real Linux ISOs) at isoPath. files
// maps path to content; links maps a symlink path to its target.
func writeTestISO(isoPath string, files, links map[string]string) error {
	_ = os.Remove(isoPath)
	// ISO9660 requires a 2048-byte logical block size.
	d, err := diskfs.Create(isoPath, 10<<20, diskfs.SectorSize(2048))
	if err != nil {
		return err
	}
	fsys, err := d.CreateFilesystem(disk.FilesystemSpec{Partition: 0, FSType: filesystem.TypeISO9660})
	if err != nil {
		return err
	}
	iso, ok := fsys.(*iso9660.FileSystem)
	if !ok {
		return fmt.Errorf("not an iso9660 filesystem")
	}
	ws := iso.Workspace()
	for p, content := range files {
		full := filepath.Join(ws, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, []byte(content), 0o444); err != nil {
			return err
		}
	}
	for p, target := range links {
		full := filepath.Join(ws, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.Symlink(target, full); err != nil {
			return err
		}
	}
	return iso.Finalize(iso9660.FinalizeOptions{RockRidge: true})
}

var _ = Describe("BootConfig Controller ISO mode", func() {
	var (
		ctx        context.Context
		dataDir    string
		nfsDir     string
		reconciler *BootConfigReconciler
	)

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		dataDir, err = os.MkdirTemp("", "isoboot-iso-test-*")
		Expect(err).NotTo(HaveOccurred())
		nfsDir = filepath.Join(dataDir, "nfs")
		reconciler = &BootConfigReconciler{
			Client: k8sClient, Scheme: k8sClient.Scheme(), DataDir: dataDir, NFSDir: nfsDir,
		}
	})
	AfterEach(func() { Expect(os.RemoveAll(dataDir)).To(Succeed()) })

	doReconcile := func(name string) (reconcile.Result, error) {
		return reconciler.Reconcile(ctx, reconcile.Request{
			Name: name, Namespace: "default",
		})
	}
	getStatus := func(name string) isobootgithubiov1alpha1.BootConfigStatus {
		var bc isobootgithubiov1alpha1.BootConfig
		ExpectWithOffset(1, k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, &bc)).To(Succeed())
		return bc.Status
	}

	makeISOArtifact := func(name string, phase isobootgithubiov1alpha1.BootArtifactPhase) func() {
		a := &isobootgithubiov1alpha1.BootArtifact{
			Name: name, Namespace: "default",
			Spec: isobootgithubiov1alpha1.BootArtifactSpec{URL: "https://example.com/test.iso", SHA256: new(validSHA256)},
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

	isoPathFor := func(artifact string) string {
		return filepath.Join(dataDir, "artifacts", artifact, "test.iso")
	}

	// readyISOArtifact creates a Ready ISO artifact and writes a real test ISO to disk.
	readyISOArtifact := func(name string, files, links map[string]string) func() {
		cleanup := makeISOArtifact(name, isobootgithubiov1alpha1.BootArtifactPhaseReady)
		ExpectWithOffset(1, os.MkdirAll(filepath.Dir(isoPathFor(name)), 0o755)).To(Succeed())
		ExpectWithOffset(1, writeTestISO(isoPathFor(name), files, links)).To(Succeed())
		return cleanup
	}

	makeISOConfig := func(name, artifactRef, kernelPath, initrdPath string) func() {
		bc := &isobootgithubiov1alpha1.BootConfig{
			Name: name, Namespace: "default",
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

	// nfsEntries lists the names in the NFS directory.
	nfsEntries := func() []string {
		entries, err := os.ReadDir(nfsDir)
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		return names
	}

	contents := map[string]string{"casper/vmlinuz": "KERNEL-BYTES", "casper/initrd": "INITRD-BYTES"}

	// A tree shaped like an Ubuntu live-server ISO. "disk" stands in for
	// ".disk": go-diskfs's ISO writer gives a directory with a leading dot an
	// empty name. Reading the real ".disk" with go-diskfs v1.9.4 was verified
	// against the Ubuntu 24.04.5 and 26.04.1 ISOs.
	ubuntuLike := map[string]string{
		"casper/vmlinuz":                        "KERNEL-BYTES",
		"casper/initrd":                         "INITRD-BYTES",
		"casper/ubuntu-server-minimal.squashfs": "SQUASHFS",
		"disk/info":                             "Ubuntu-Server 26.04.1",
		"disk/casper-uuid-generic":              "0123-uuid",
		"dists/resolute/Release":                "Origin: Ubuntu",
		"pool/main/l/linux/linux-image.deb":     "DEB",
		"md5sum.txt":                            "sums",
	}
	ubuntuLinks := map[string]string{
		"ubuntu":         ".",
		"dists/stable":   "resolute",
		"dists/unstable": "resolute",
	}

	It("extracts the whole tree, kernel and initrd when the ISO artifact is Ready", func() {
		defer readyISOArtifact("iso-ok", ubuntuLike, ubuntuLinks)()
		defer makeISOConfig("iso-bc-ok", "iso-ok", "casper/vmlinuz", "casper/initrd")()

		result, err := doReconcile("iso-bc-ok")
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeZero())
		Expect(getStatus("iso-bc-ok").Phase).To(Equal(isobootgithubiov1alpha1.BootConfigPhaseReady))

		// Kernel and initrd are in the boot directory, served by nginx.
		bootDir := filepath.Join(dataDir, "boot", "iso-bc-ok")
		kernel, err := os.ReadFile(filepath.Join(bootDir, "vmlinuz"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(kernel)).To(Equal("KERNEL-BYTES"))
		initrd, err := os.ReadFile(filepath.Join(bootDir, "initrd"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(initrd)).To(Equal("INITRD-BYTES"))

		// The ISO itself is no longer served over HTTP.
		entries, err := os.ReadDir(bootDir)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(HaveLen(2))

		// Every file and directory, with its Rock Ridge name.
		tree := filepath.Join(nfsDir, "iso-bc-ok")
		for p, want := range ubuntuLike {
			got, err := os.ReadFile(filepath.Join(tree, p))
			Expect(err).NotTo(HaveOccurred(), p)
			Expect(string(got)).To(Equal(want), p)
			info, err := os.Lstat(filepath.Join(tree, p))
			Expect(err).NotTo(HaveOccurred())
			Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o644)), p)
		}
		for _, d := range []string{".", "disk", "casper", "dists", "dists/resolute", "pool/main/l/linux"} {
			info, err := os.Lstat(filepath.Join(tree, d))
			Expect(err).NotTo(HaveOccurred(), d)
			Expect(info.IsDir()).To(BeTrue(), d)
			Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o755)), d)
		}

		// Only the tree and its hidden marker are left in the NFS directory.
		Expect(nfsEntries()).To(ConsistOf("iso-bc-ok", ".source_iso-bc-ok"))
		_, err = os.Stat(filepath.Join(tree, ".source_iso-bc-ok"))
		Expect(os.IsNotExist(err)).To(BeTrue(), "marker must not be inside the exported tree")
	})

	It("recreates in-tree symlinks and skips ones that leave the tree", func() {
		links := map[string]string{
			"ubuntu":             ".",
			"dists/stable":       "resolute",
			"dists/up":           "../casper/vmlinuz",
			"escape":             "../../etc",
			"absolute":           "/etc/passwd",
			"dists/sneaky":       "stable/../..",
			"dists/resolute/out": "../../..",
		}
		defer readyISOArtifact("iso-links", ubuntuLike, links)()
		// The kernel path goes through the "ubuntu -> ." symlink.
		defer makeISOConfig("iso-bc-links", "iso-links", "ubuntu/casper/vmlinuz", "casper/initrd")()

		_, err := doReconcile("iso-bc-links")
		Expect(err).NotTo(HaveOccurred())
		Expect(getStatus("iso-bc-links").Phase).To(Equal(isobootgithubiov1alpha1.BootConfigPhaseReady))

		tree := filepath.Join(nfsDir, "iso-bc-links")
		for p, want := range map[string]string{"ubuntu": ".", "dists/stable": "resolute", "dists/up": "../casper/vmlinuz"} {
			got, err := os.Readlink(filepath.Join(tree, p))
			Expect(err).NotTo(HaveOccurred(), p)
			Expect(got).To(Equal(want), p)
		}
		for _, p := range []string{"escape", "absolute", "dists/sneaky", "dists/resolute/out"} {
			_, err := os.Lstat(filepath.Join(tree, p))
			Expect(os.IsNotExist(err)).To(BeTrue(), p)
		}
		got, err := os.ReadFile(filepath.Join(tree, "ubuntu", "dists", "stable", "Release"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(got)).To(Equal("Origin: Ubuntu"))
	})

	It("does not re-extract on repeated reconcile when the ISO is unchanged", func() {
		defer readyISOArtifact("iso-idem", ubuntuLike, ubuntuLinks)()
		defer makeISOConfig("iso-bc-idem", "iso-idem", "casper/vmlinuz", "casper/initrd")()

		_, err := doReconcile("iso-bc-idem")
		Expect(err).NotTo(HaveOccurred())
		Expect(getStatus("iso-bc-idem").Phase).To(Equal(isobootgithubiov1alpha1.BootConfigPhaseReady))
		paths := []string{
			filepath.Join(dataDir, "boot", "iso-bc-idem", "vmlinuz"),
			filepath.Join(nfsDir, "iso-bc-idem"),
			filepath.Join(nfsDir, "iso-bc-idem", "casper", "ubuntu-server-minimal.squashfs"),
			filepath.Join(nfsDir, "iso-bc-idem", "disk"),
		}
		before := make([]os.FileInfo, 0, len(paths))
		for _, p := range paths {
			info, err := os.Stat(p)
			Expect(err).NotTo(HaveOccurred())
			before = append(before, info)
		}

		// A second reconcile must leave everything in place (same inodes), so
		// NFS clients keep valid handles.
		_, err = doReconcile("iso-bc-idem")
		Expect(err).NotTo(HaveOccurred())
		for i, p := range paths {
			after, err := os.Stat(p)
			Expect(err).NotTo(HaveOccurred())
			Expect(os.SameFile(before[i], after)).To(BeTrue(), p)
		}
	})

	It("does not re-extract when only the artifact's hash text changes", func() {
		defer readyISOArtifact("iso-hash", ubuntuLike, ubuntuLinks)()
		defer makeISOConfig("iso-bc-hash", "iso-hash", "casper/vmlinuz", "casper/initrd")()

		_, err := doReconcile("iso-bc-hash")
		Expect(err).NotTo(HaveOccurred())
		Expect(getStatus("iso-bc-hash").Phase).To(Equal(isobootgithubiov1alpha1.BootConfigPhaseReady))
		tree := filepath.Join(nfsDir, "iso-bc-hash")
		before, err := os.Stat(tree)
		Expect(err).NotTo(HaveOccurred())

		// The same file, its hash now written in upper case. The artifact's
		// status is still Ready from before the edit.
		var artifact isobootgithubiov1alpha1.BootArtifact
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "iso-hash", Namespace: "default"}, &artifact)).To(Succeed())
		artifact.Spec.SHA256 = new(strings.ToUpper(validSHA256))
		Expect(k8sClient.Update(ctx, &artifact)).To(Succeed())

		_, err = doReconcile("iso-bc-hash")
		Expect(err).NotTo(HaveOccurred())
		after, err := os.Stat(tree)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.SameFile(before, after)).To(BeTrue(), "the unchanged ISO must not be extracted again")
	})

	It("re-extracts tree, kernel and initrd when the ISO changes", func() {
		defer readyISOArtifact("iso-chg", ubuntuLike, ubuntuLinks)()
		defer makeISOConfig("iso-bc-chg", "iso-chg", "casper/vmlinuz", "casper/initrd")()

		_, err := doReconcile("iso-bc-chg")
		Expect(err).NotTo(HaveOccurred())
		Expect(getStatus("iso-bc-chg").Phase).To(Equal(isobootgithubiov1alpha1.BootConfigPhaseReady))

		// A new ISO build: different kernel, a file removed, a file added.
		Expect(writeTestISO(isoPathFor("iso-chg"), map[string]string{
			"casper/vmlinuz": "KERNEL-V2",
			"casper/initrd":  "INITRD-V2",
			"disk/info":      "Ubuntu-Server 26.04.2",
			"new.txt":        "new",
		}, nil)).To(Succeed())
		later := time.Now().Add(time.Minute)
		Expect(os.Chtimes(isoPathFor("iso-chg"), later, later)).To(Succeed())

		_, err = doReconcile("iso-bc-chg")
		Expect(err).NotTo(HaveOccurred())
		Expect(getStatus("iso-bc-chg").Phase).To(Equal(isobootgithubiov1alpha1.BootConfigPhaseReady))

		tree := filepath.Join(nfsDir, "iso-bc-chg")
		got, err := os.ReadFile(filepath.Join(tree, "disk", "info"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(got)).To(Equal("Ubuntu-Server 26.04.2"))
		_, err = os.Stat(filepath.Join(tree, "new.txt"))
		Expect(err).NotTo(HaveOccurred())
		_, err = os.Lstat(filepath.Join(tree, "md5sum.txt"))
		Expect(os.IsNotExist(err)).To(BeTrue())
		kernel, err := os.ReadFile(filepath.Join(dataDir, "boot", "iso-bc-chg", "vmlinuz"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(kernel)).To(Equal("KERNEL-V2"))
		initrd, err := os.ReadFile(filepath.Join(dataDir, "boot", "iso-bc-chg", "initrd"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(initrd)).To(Equal("INITRD-V2"))
		Expect(nfsEntries()).To(ConsistOf("iso-bc-chg", ".source_iso-bc-chg"))
	})

	It("cleans up stale temporary directories and the old ISO symlink", func() {
		defer readyISOArtifact("iso-stale", contents, nil)()
		defer makeISOConfig("iso-bc-stale", "iso-stale", "casper/vmlinuz", "casper/initrd")()

		Expect(os.MkdirAll(filepath.Join(nfsDir, ".extract_iso-bc-stale_123", "x"), 0o755)).To(Succeed())
		bootDir := filepath.Join(dataDir, "boot", "iso-bc-stale")
		Expect(os.MkdirAll(bootDir, 0o755)).To(Succeed())
		Expect(os.Symlink("../../artifacts/iso-stale/test.iso", filepath.Join(bootDir, "test.iso"))).To(Succeed())

		_, err := doReconcile("iso-bc-stale")
		Expect(err).NotTo(HaveOccurred())
		Expect(getStatus("iso-bc-stale").Phase).To(Equal(isobootgithubiov1alpha1.BootConfigPhaseReady))
		Expect(nfsEntries()).To(ConsistOf("iso-bc-stale", ".source_iso-bc-stale"))
		_, err = os.Lstat(filepath.Join(bootDir, "test.iso"))
		Expect(os.IsNotExist(err)).To(BeTrue())
	})

	It("removes the tree and marker when the BootConfig is deleted", func() {
		defer readyISOArtifact("iso-del", contents, nil)()
		cleanup := makeISOConfig("iso-bc-del", "iso-del", "casper/vmlinuz", "casper/initrd")

		_, err := doReconcile("iso-bc-del")
		Expect(err).NotTo(HaveOccurred())
		Expect(nfsEntries()).To(ConsistOf("iso-bc-del", ".source_iso-bc-del"))

		cleanup()
		Eventually(func() error {
			return k8sClient.Get(ctx, types.NamespacedName{Name: "iso-bc-del", Namespace: "default"},
				&isobootgithubiov1alpha1.BootConfig{})
		}).ShouldNot(Succeed())
		_, err = doReconcile("iso-bc-del")
		Expect(err).NotTo(HaveOccurred())
		Expect(nfsEntries()).To(BeEmpty())
		_, err = os.Stat(filepath.Join(dataDir, "boot", "iso-bc-del"))
		Expect(os.IsNotExist(err)).To(BeTrue())
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

	It("is Error when the controller has no NFS directory", func() {
		reconciler.NFSDir = ""
		defer readyISOArtifact("iso-nonfs", contents, nil)()
		defer makeISOConfig("iso-bc-nonfs", "iso-nonfs", "casper/vmlinuz", "casper/initrd")()
		_, err := doReconcile("iso-bc-nonfs")
		Expect(err).NotTo(HaveOccurred())
		status := getStatus("iso-bc-nonfs")
		Expect(status.Phase).To(Equal(isobootgithubiov1alpha1.BootConfigPhaseError))
		Expect(status.Message).To(ContainSubstring("--nfs-dir"))
	})

	It("is Error when the kernel path is not in the ISO", func() {
		defer readyISOArtifact("iso-badk", contents, nil)()
		defer makeISOConfig("iso-bc-badk", "iso-badk", "casper/nope", "casper/initrd")()
		_, err := doReconcile("iso-bc-badk")
		Expect(err).NotTo(HaveOccurred())
		Expect(getStatus("iso-bc-badk").Phase).To(Equal(isobootgithubiov1alpha1.BootConfigPhaseError))
	})

	It("is Error when the initrd path is not in the ISO", func() {
		defer readyISOArtifact("iso-badi", contents, nil)()
		defer makeISOConfig("iso-bc-badi", "iso-badi", "casper/vmlinuz", "casper/nope")()
		_, err := doReconcile("iso-bc-badi")
		Expect(err).NotTo(HaveOccurred())
		Expect(getStatus("iso-bc-badi").Phase).To(Equal(isobootgithubiov1alpha1.BootConfigPhaseError))
	})

	It("is Error when a path contains traversal", func() {
		defer readyISOArtifact("iso-trav", contents, nil)()
		defer makeISOConfig("iso-bc-trav", "iso-trav", "../etc/passwd", "casper/initrd")()
		_, err := doReconcile("iso-bc-trav")
		Expect(err).NotTo(HaveOccurred())
		Expect(getStatus("iso-bc-trav").Phase).To(Equal(isobootgithubiov1alpha1.BootConfigPhaseError))
	})

	It("does not copy a file from outside the tree through a symlink in it", func() {
		defer readyISOArtifact("iso-travlink", contents, nil)()
		defer makeISOConfig("iso-bc-travlink", "iso-travlink", "casper/vmlinuz", "casper/initrd")()
		_, err := doReconcile("iso-bc-travlink")
		Expect(err).NotTo(HaveOccurred())
		Expect(getStatus("iso-bc-travlink").Phase).To(Equal(isobootgithubiov1alpha1.BootConfigPhaseReady))

		// Extraction never creates a link that leaves the tree, so plant one
		// in the extracted tree directly. The marker still matches, so the
		// tree is not extracted again, and only os.Root stands between the
		// kernel path and the file outside.
		Expect(os.WriteFile(filepath.Join(dataDir, "outside"), []byte("OUTSIDE"), 0o644)).To(Succeed())
		Expect(os.Symlink("../..", filepath.Join(nfsDir, "iso-bc-travlink", "evil"))).To(Succeed())
		_, err = os.Stat(filepath.Join(nfsDir, "iso-bc-travlink", "evil", "outside"))
		Expect(err).NotTo(HaveOccurred(), "the planted link must reach the file")

		var bc isobootgithubiov1alpha1.BootConfig
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "iso-bc-travlink", Namespace: "default"}, &bc)).To(Succeed())
		bc.Spec.ISO.KernelPath = "evil/outside"
		Expect(k8sClient.Update(ctx, &bc)).To(Succeed())

		_, err = doReconcile("iso-bc-travlink")
		Expect(err).NotTo(HaveOccurred())
		Expect(getStatus("iso-bc-travlink").Phase).To(Equal(isobootgithubiov1alpha1.BootConfigPhaseError))
		kernel, err := os.ReadFile(filepath.Join(dataDir, "boot", "iso-bc-travlink", "vmlinuz"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(kernel)).To(Equal("KERNEL-BYTES"))
	})
})

var _ = Describe("ISO tree helpers", func() {
	DescribeTable("symlinkStaysInside",
		func(link, target string, want bool) {
			Expect(symlinkStaysInside(link, target)).To(Equal(want))
		},
		Entry("self at root", "ubuntu", ".", true),
		Entry("sibling", "dists/stable", "resolute", true),
		Entry("up then down", "dists/up", "../casper/vmlinuz", true),
		Entry("up to root", "a/b", "..", true),
		Entry("escape from root", "x", "..", false),
		Entry("deep escape", "a/b", "../../..", false),
		Entry("absolute", "x", "/etc", false),
		Entry("empty", "x", "", false),
		Entry("dotdot after a name", "dists/sneaky", "stable/../..", false),
		Entry("dotdot after a name, lexically inside", "a/b", "c/../d", false),
		Entry("backslash", "x", `..\..`, false),
	)

	DescribeTable("isSafeEntryName",
		func(name string, want bool) {
			Expect(isSafeEntryName(name)).To(Equal(want))
		},
		Entry("plain", "casper", true),
		Entry("hidden", ".disk", true),
		Entry("empty", "", false),
		Entry("dot", ".", false),
		Entry("dotdot", "..", false),
		Entry("slash", "a/b", false),
		Entry("slash traversal", "../x", false),
		Entry("backslash", `a\b`, false),
		Entry("nul", "a\x00b", false),
	)

	It("rejects a destination entry that already exists", func() {
		// Extraction writes with O_EXCL into a fresh directory, so it can
		// never overwrite or follow something already there.
		dir := GinkgoT().TempDir()
		isoPath := filepath.Join(dir, "t.iso")
		Expect(writeTestISO(isoPath, map[string]string{"a": "x"}, nil)).To(Succeed())
		dest := filepath.Join(dir, "dest")
		Expect(os.MkdirAll(dest, 0o755)).To(Succeed())
		Expect(os.Symlink("/etc/passwd", filepath.Join(dest, "a"))).To(Succeed())
		err := extractISOTree(GinkgoLogr, isoPath, dest)
		Expect(err).To(HaveOccurred())
		Expect(strings.Contains(err.Error(), "creating")).To(BeTrue())
	})

	// recordSyncs makes syncFile record the name of every file or directory
	// it flushes, for the rest of the spec.
	recordSyncs := func() *[]string {
		synced := []string{}
		previous := syncFile
		DeferCleanup(func() { syncFile = previous })
		syncFile = func(f *os.File) error {
			synced = append(synced, filepath.Clean(f.Name()))
			return previous(f)
		}
		return &synced
	}

	It("flushes every extracted file and directory to disk", func() {
		synced := recordSyncs()
		dir := GinkgoT().TempDir()
		isoPath := filepath.Join(dir, "t.iso")
		files := map[string]string{"dists/x/Release": "r"}
		for i := range 40 {
			files[fmt.Sprintf("pool/f%02d", i)] = strings.Repeat("x", 1<<10)
		}
		Expect(writeTestISO(isoPath, files, nil)).To(Succeed())
		dest := filepath.Join(dir, "dest")
		Expect(os.Mkdir(dest, 0o755)).To(Succeed())

		Expect(extractISOTree(GinkgoLogr, isoPath, dest)).To(Succeed())

		want := []string{dest, filepath.Join(dest, "pool"), filepath.Join(dest, "dists"), filepath.Join(dest, "dists", "x")}
		for p := range files {
			want = append(want, filepath.Join(dest, p))
		}
		Expect(*synced).To(ContainElements(want))
	})

	It("flushes the renamed tree and the marker to disk", func() {
		synced := recordSyncs()
		dir := GinkgoT().TempDir()
		isoPath := filepath.Join(dir, "t.iso")
		Expect(writeTestISO(isoPath, map[string]string{"a": "x"}, nil)).To(Succeed())
		nfs := filepath.Join(dir, "nfs")

		Expect(ensureISOTree(GinkgoLogr, isoPath, "source", nfs, "t")).To(Succeed())

		// The marker vouches for the tree, so it is written only after the
		// tree's renames are on disk, and is itself made durable.
		Expect(len(*synced)).To(BeNumerically(">=", 3))
		last := (*synced)[len(*synced)-3:]
		Expect(last[0]).To(Equal(nfs), "nfs dir after the tree rename")
		Expect(filepath.Dir(last[1])).To(Equal(nfs), "the marker's temporary file")
		Expect(last[2]).To(Equal(nfs), "nfs dir after the marker rename")
	})
})

// countingFile is a writeSyncer that counts its bytes and Sync calls.
type countingFile struct {
	written int
	syncs   int
}

func (f *countingFile) Write(p []byte) (int, error) {
	f.written += len(p)
	return len(p), nil
}

func (f *countingFile) Sync() error {
	f.syncs++
	return nil
}

var _ = Describe("syncWriter", func() {
	It("writes everything and syncs every syncEvery bytes", func() {
		f := &countingFile{}
		w := &syncWriter{f: f}
		chunk := make([]byte, 1<<20)
		total := 0
		for total < syncEvery+3<<20 {
			n, err := w.Write(chunk)
			Expect(err).NotTo(HaveOccurred())
			total += n
		}
		Expect(f.written).To(Equal(total))
		Expect(f.syncs).To(Equal(1))
		Expect(w.pending).To(Equal(int64(total - syncEvery)))
	})
})
