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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("DownloadPath", func() {
	const (
		baseDir   = "/var/lib/isoboot"
		namespace = "default"
		name      = "my-bootsource"
	)

	It("should return the correct path for a kernel resource", func() {
		p, err := DownloadPath(baseDir, namespace, name, ResourceKernel, "https://example.com/vmlinuz")
		Expect(err).NotTo(HaveOccurred())
		Expect(p).To(Equal("/var/lib/isoboot/default/my-bootsource/kernel/vmlinuz"))
	})

	It("should return the correct path for an initrd resource", func() {
		p, err := DownloadPath(baseDir, namespace, name, ResourceInitrd, "https://example.com/initrd.img")
		Expect(err).NotTo(HaveOccurred())
		Expect(p).To(Equal("/var/lib/isoboot/default/my-bootsource/initrd/initrd.img"))
	})

	It("should return the correct path for a firmware resource", func() {
		p, err := DownloadPath(baseDir, namespace, name, ResourceFirmware, "https://example.com/firmware.bin")
		Expect(err).NotTo(HaveOccurred())
		Expect(p).To(Equal("/var/lib/isoboot/default/my-bootsource/firmware/firmware.bin"))
	})

	It("should return the correct path for an ISO resource", func() {
		p, err := DownloadPath(baseDir, namespace, name, ResourceISO, "https://example.com/boot.iso")
		Expect(err).NotTo(HaveOccurred())
		Expect(p).To(Equal("/var/lib/isoboot/default/my-bootsource/iso/boot.iso"))
	})

	It("should strip query parameters from the URL", func() {
		p, err := DownloadPath(baseDir, namespace, name, ResourceKernel, "https://example.com/vmlinuz?token=abc123")
		Expect(err).NotTo(HaveOccurred())
		Expect(p).To(Equal("/var/lib/isoboot/default/my-bootsource/kernel/vmlinuz"))
	})

	It("should strip fragments from the URL", func() {
		p, err := DownloadPath(baseDir, namespace, name, ResourceKernel, "https://example.com/vmlinuz#section")
		Expect(err).NotTo(HaveOccurred())
		Expect(p).To(Equal("/var/lib/isoboot/default/my-bootsource/kernel/vmlinuz"))
	})

	It("should handle deeply nested URL paths", func() {
		p, err := DownloadPath(baseDir, namespace, name, ResourceKernel, "https://example.com/releases/v1.0/arch/x86_64/vmlinuz")
		Expect(err).NotTo(HaveOccurred())
		Expect(p).To(Equal("/var/lib/isoboot/default/my-bootsource/kernel/vmlinuz"))
	})

	It("should return an error when the URL has no filename", func() {
		_, err := DownloadPath(baseDir, namespace, name, ResourceKernel, "https://example.com/")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("no filename"))
	})

	It("should return an error for a bare domain URL", func() {
		_, err := DownloadPath(baseDir, namespace, name, ResourceKernel, "https://example.com")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("no filename"))
	})

	It("should return an error for an invalid URL", func() {
		_, err := DownloadPath(baseDir, namespace, name, ResourceKernel, "://bad\x00url")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("invalid URL"))
	})

	It("should return an error when URL path resolves to ..", func() {
		_, err := DownloadPath(baseDir, namespace, name, ResourceKernel, "https://example.com/a/..")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("no filename"))
	})

	It("should reject path traversal in namespace", func() {
		_, err := DownloadPath(baseDir, "../escape", name, ResourceKernel, "https://example.com/vmlinuz")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("escapes base directory"))
	})

	It("should reject path traversal in name", func() {
		_, err := DownloadPath(baseDir, namespace, "../../etc", ResourceKernel, "https://example.com/vmlinuz")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("escapes base directory"))
	})

	It("should reject filenames with single quotes (shell injection)", func() {
		_, err := DownloadPath(baseDir, namespace, name, ResourceFirmware, "https://example.com/fw'$(whoami).bin")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unsafe filename"))
	})

	It("should reject filenames with backticks", func() {
		_, err := DownloadPath(baseDir, namespace, name, ResourceKernel, "https://example.com/vm`id`linuz")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unsafe filename"))
	})

	It("should reject filenames with spaces", func() {
		_, err := DownloadPath(baseDir, namespace, name, ResourceKernel, "https://example.com/my file.iso")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unsafe filename"))
	})

	It("should reject filenames with semicolons", func() {
		_, err := DownloadPath(baseDir, namespace, name, ResourceKernel, "https://example.com/file;rm -rf.iso")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unsafe filename"))
	})

	It("should accept filenames with plus signs", func() {
		p, err := DownloadPath(baseDir, namespace, name, ResourceFirmware, "https://example.com/firmware+extra.bin")
		Expect(err).NotTo(HaveOccurred())
		Expect(p).To(ContainSubstring("firmware+extra.bin"))
	})
})
