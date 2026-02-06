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
	"encoding/base64"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	isobootv1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
)

var _ = Describe("Job construction", func() {
	const (
		baseDir   = "/var/lib/isoboot"
		namespace = "default"
		name      = "my-source"
	)

	Describe("collectDownloadTasks", func() {
		It("should return 4 tasks for kernel+initrd", func() {
			spec := isobootv1alpha1.BootSourceSpec{
				Kernel: &isobootv1alpha1.KernelSource{
					URL: isobootv1alpha1.URLSource{
						Binary: "https://example.com/vmlinuz",
						Shasum: "https://example.com/vmlinuz.sha256",
					},
				},
				Initrd: &isobootv1alpha1.InitrdSource{
					URL: isobootv1alpha1.URLSource{
						Binary: "https://example.com/initrd.img",
						Shasum: "https://example.com/initrd.img.sha256",
					},
				},
			}
			tasks, err := collectDownloadTasks(spec, baseDir, namespace, name)
			Expect(err).NotTo(HaveOccurred())
			Expect(tasks).To(HaveLen(4))

			// Verify kernel binary task
			Expect(tasks[0].URL).To(Equal("https://example.com/vmlinuz"))
			Expect(tasks[0].OutputPath).To(Equal("/var/lib/isoboot/default/my-source/kernel/vmlinuz"))

			// Verify kernel shasum task
			Expect(tasks[1].URL).To(Equal("https://example.com/vmlinuz.sha256"))
		})

		It("should return 2 tasks for iso", func() {
			spec := isobootv1alpha1.BootSourceSpec{
				ISO: &isobootv1alpha1.ISOSource{
					URL: isobootv1alpha1.URLSource{
						Binary: "https://example.com/boot.iso",
						Shasum: "https://example.com/boot.iso.sha256",
					},
					Path: isobootv1alpha1.PathSource{
						Kernel: "/boot/vmlinuz",
						Initrd: "/boot/initrd.img",
					},
				},
			}
			tasks, err := collectDownloadTasks(spec, baseDir, namespace, name)
			Expect(err).NotTo(HaveOccurred())
			Expect(tasks).To(HaveLen(2))
			Expect(tasks[0].OutputPath).To(ContainSubstring("/iso/"))
		})

		It("should return 4 tasks for iso+firmware", func() {
			spec := isobootv1alpha1.BootSourceSpec{
				ISO: &isobootv1alpha1.ISOSource{
					URL: isobootv1alpha1.URLSource{
						Binary: "https://example.com/boot.iso",
						Shasum: "https://example.com/boot.iso.sha256",
					},
					Path: isobootv1alpha1.PathSource{
						Kernel: "/boot/vmlinuz",
						Initrd: "/boot/initrd.img",
					},
				},
				Firmware: &isobootv1alpha1.FirmwareSource{
					URL: isobootv1alpha1.URLSource{
						Binary: "https://example.com/firmware.bin",
						Shasum: "https://example.com/firmware.bin.sha256",
					},
				},
			}
			tasks, err := collectDownloadTasks(spec, baseDir, namespace, name)
			Expect(err).NotTo(HaveOccurred())
			Expect(tasks).To(HaveLen(4))
		})

		It("should return an error for an invalid URL", func() {
			spec := isobootv1alpha1.BootSourceSpec{
				Kernel: &isobootv1alpha1.KernelSource{
					URL: isobootv1alpha1.URLSource{
						Binary: "https://example.com/",
						Shasum: "https://example.com/vmlinuz.sha256",
					},
				},
			}
			_, err := collectDownloadTasks(spec, baseDir, namespace, name)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("buildDownloadScript", func() {
		It("should start with set -eu and install wget", func() {
			tasks := []downloadTask{
				{
					URL:        "https://example.com/vmlinuz",
					OutputPath: "/var/lib/isoboot/default/my-source/kernel/vmlinuz",
				},
			}
			script := buildDownloadScript(tasks, nil, nil)
			Expect(script).To(HavePrefix("set -eu\n"))
			Expect(script).To(ContainSubstring("apk add --no-cache wget"))
		})

		It("should contain base64-encoded URLs via b64enc", func() {
			tasks := []downloadTask{
				{
					URL:        "https://example.com/vmlinuz",
					OutputPath: "/var/lib/isoboot/default/my-source/kernel/vmlinuz",
				},
			}
			script := buildDownloadScript(tasks, nil, nil)
			encoded := base64.StdEncoding.EncodeToString([]byte("https://example.com/vmlinuz"))
			Expect(script).To(ContainSubstring(encoded))
		})

		It("should use wget -i to read URL from file", func() {
			tasks := []downloadTask{
				{
					URL:        "https://example.com/vmlinuz",
					OutputPath: "/var/lib/isoboot/default/my-source/kernel/vmlinuz",
				},
			}
			script := buildDownloadScript(tasks, nil, nil)
			Expect(script).To(ContainSubstring("wget -q -i '/tmp/url_0.txt'"))
		})

		It("should generate unique temp files per task", func() {
			tasks := []downloadTask{
				{
					URL:        "https://example.com/a",
					OutputPath: "/data/a",
				},
				{
					URL:        "https://example.com/b",
					OutputPath: "/data/b",
				},
			}
			script := buildDownloadScript(tasks, nil, nil)
			Expect(script).To(ContainSubstring("url_0.txt"))
			Expect(script).To(ContainSubstring("url_1.txt"))
		})

		It("should contain skip-if-exists check", func() {
			tasks := []downloadTask{
				{
					URL:        "https://example.com/vmlinuz",
					OutputPath: "/data/vmlinuz",
				},
			}
			script := buildDownloadScript(tasks, nil, nil)
			Expect(script).To(ContainSubstring("! -f"))
			Expect(script).To(ContainSubstring("SKIP"))
		})

		It("should contain hash verification for binary/shasum pairs", func() {
			tasks := []downloadTask{
				{
					URL:        "https://example.com/dir/vmlinuz",
					OutputPath: "/data/vmlinuz",
				},
				{
					URL:        "https://example.com/dir/SHA256SUMS",
					OutputPath: "/data/SHA256SUMS",
				},
			}
			script := buildDownloadScript(tasks, nil, nil)
			Expect(script).To(ContainSubstring("VERIFY_FAILED=0"))
			Expect(script).To(ContainSubstring("awk"))
			Expect(script).To(ContainSubstring("sha256sum"))
			Expect(script).To(ContainSubstring("PASS"))
			Expect(script).To(ContainSubstring("FAIL"))
		})

		It("should contain file size summary using du -h for binary files", func() {
			tasks := []downloadTask{
				{
					URL:        "https://example.com/dir/vmlinuz",
					OutputPath: "/data/vmlinuz",
				},
				{
					URL:        "https://example.com/dir/SHA256SUMS",
					OutputPath: "/data/SHA256SUMS",
				},
			}
			script := buildDownloadScript(tasks, nil, nil)
			Expect(script).To(ContainSubstring("File sizes"))
			Expect(script).To(ContainSubstring("du -h '/data/vmlinuz'"))
			// Should not show du for shasum files (odd index)
			Expect(script).NotTo(ContainSubstring("du -h '/data/SHA256SUMS'"))
		})

		It("should use awk with exact match on second field", func() {
			tasks := []downloadTask{
				{
					URL:        "https://example.com/images/netboot/amd64/linux",
					OutputPath: "/data/linux",
				},
				{
					URL:        "https://example.com/images/SHA256SUMS",
					OutputPath: "/data/SHA256SUMS",
				},
			}
			script := buildDownloadScript(tasks, nil, nil)
			// awk matches $2 exactly, avoiding substring collisions (e.g. linux vs linux.old)
			Expect(script).To(ContainSubstring(`awk -v path='netboot/amd64/linux'`))
			Expect(script).To(ContainSubstring(`$2==path`))
		})

		It("should accept uppercase hex and normalize to lowercase", func() {
			tasks := []downloadTask{
				{
					URL:        "https://example.com/dir/vmlinuz",
					OutputPath: "/data/vmlinuz",
				},
				{
					URL:        "https://example.com/dir/SHA256SUMS",
					OutputPath: "/data/SHA256SUMS",
				},
			}
			script := buildDownloadScript(tasks, nil, nil)
			// case pattern accepts A-F alongside a-f
			Expect(script).To(ContainSubstring("0-9a-fA-F"))
			// normalize to lowercase before comparison
			Expect(script).To(ContainSubstring("tr 'A-F' 'a-f'"))
		})

		It("should send error and fail messages to stderr", func() {
			tasks := []downloadTask{
				{
					URL:        "https://example.com/dir/vmlinuz",
					OutputPath: "/data/vmlinuz",
				},
				{
					URL:        "https://example.com/dir/SHA256SUMS",
					OutputPath: "/data/SHA256SUMS",
				},
			}
			script := buildDownloadScript(tasks, nil, nil)
			Expect(script).To(ContainSubstring("ERROR: no valid hash found for /data/vmlinuz' >&2"))
			Expect(script).To(ContainSubstring("FAIL: /data/vmlinuz' >&2"))
		})

		It("should include ISO extraction commands when isoExtractInfo is provided", func() {
			tasks := []downloadTask{
				{
					URL:        "https://example.com/boot.iso",
					OutputPath: "/data/iso/boot.iso",
				},
				{
					URL:        "https://example.com/boot.iso.sha256",
					OutputPath: "/data/iso/boot.iso.sha256",
				},
			}
			iso := &isoExtractInfo{
				ISOPath:      "/data/iso/boot.iso",
				KernelSrc:    "/casper/vmlinuz",
				KernelDstDir: "/data/kernel",
				KernelDst:    "/data/kernel/vmlinuz",
				InitrdSrc:    "/casper/initrd",
				InitrdDstDir: "/data/initrd",
				InitrdDst:    "/data/initrd/initrd",
			}
			script := buildDownloadScript(tasks, iso, nil)
			Expect(script).To(ContainSubstring("Extracting from ISO"))
			Expect(script).To(ContainSubstring("mount -o ro,loop '/data/iso/boot.iso'"))
			Expect(script).To(ContainSubstring("mkdir -p '/data/kernel'"))
			// Verify / separator between mount dir and ISO path
			Expect(script).To(ContainSubstring(`"$MOUNT_DIR"/'/casper/vmlinuz'`))
			Expect(script).To(ContainSubstring("'/data/kernel/vmlinuz'"))
			Expect(script).To(ContainSubstring("mkdir -p '/data/initrd'"))
			Expect(script).To(ContainSubstring(`"$MOUNT_DIR"/'/casper/initrd'`))
			Expect(script).To(ContainSubstring("'/data/initrd/initrd'"))
			Expect(script).To(ContainSubstring("umount"))
			// Verify trap-based cleanup
			Expect(script).To(ContainSubstring("trap"))
			Expect(script).To(ContainSubstring("trap - EXIT"))
			// Verify error handling on mount
			Expect(script).To(ContainSubstring("ERROR: failed to mount ISO"))
			Expect(script).To(ContainSubstring("Extracted kernel: /data/kernel/vmlinuz"))
			Expect(script).To(ContainSubstring("Extracted initrd: /data/initrd/initrd"))
			// du -h should include extracted files
			Expect(script).To(ContainSubstring("du -h '/data/kernel/vmlinuz'"))
			Expect(script).To(ContainSubstring("du -h '/data/initrd/initrd'"))
		})

		It("should include firmware concatenation when firmwareBuildInfo is provided", func() {
			tasks := []downloadTask{
				{
					URL:        "https://example.com/vmlinuz",
					OutputPath: "/data/kernel/vmlinuz",
				},
				{
					URL:        "https://example.com/SHA256SUMS",
					OutputPath: "/data/kernel/SHA256SUMS",
				},
				{
					URL:        "https://example.com/initrd.img",
					OutputPath: "/data/initrd/initrd.img",
				},
				{
					URL:        "https://example.com/SHA256SUMS",
					OutputPath: "/data/initrd/SHA256SUMS",
				},
				{
					URL:        "https://example.com/firmware.cpio.gz",
					OutputPath: "/data/firmware/firmware.cpio.gz",
				},
				{
					URL:        "https://example.com/SHA256SUMS",
					OutputPath: "/data/firmware/SHA256SUMS",
				},
			}
			fw := &firmwareBuildInfo{
				FirmwarePath: "/data/firmware/firmware.cpio.gz",
				InitrdPath:   "/data/initrd/initrd.img",
				OutputDir:    "/data/initrd/with-firmware",
				OutputPath:   "/data/initrd/with-firmware/initrd.img",
			}
			script := buildDownloadScript(tasks, nil, fw)
			Expect(script).To(ContainSubstring("Building combined initrd with firmware"))
			Expect(script).To(ContainSubstring("mkdir -p '/data/initrd/with-firmware'"))
			Expect(script).To(ContainSubstring("cat '/data/initrd/initrd.img' '/data/firmware/firmware.cpio.gz' > '/data/initrd/with-firmware/initrd.img'"))
			Expect(script).To(ContainSubstring("Combined initrd: /data/initrd/with-firmware/initrd.img"))
			// Skip-if-exists check
			Expect(script).To(ContainSubstring("if [ -f '/data/initrd/with-firmware/initrd.img' ]"))
			// du -h for combined file
			Expect(script).To(ContainSubstring("du -h '/data/initrd/with-firmware/initrd.img'"))
		})

		It("should concatenate initrd before firmware per Debian NetbootFirmware convention", func() {
			tasks := []downloadTask{
				{URL: "https://example.com/initrd.img", OutputPath: "/data/initrd/initrd.img"},
				{URL: "https://example.com/SHA256SUMS", OutputPath: "/data/initrd/SHA256SUMS"},
			}
			fw := &firmwareBuildInfo{
				FirmwarePath: "/data/firmware/firmware.cpio.gz",
				InitrdPath:   "/data/initrd/initrd.img",
				OutputDir:    "/data/initrd/with-firmware",
				OutputPath:   "/data/initrd/with-firmware/initrd.img",
			}
			script := buildDownloadScript(tasks, nil, fw)
			initrdIdx := strings.Index(script, "cat '/data/initrd/initrd.img'")
			firmwareIdx := strings.Index(script, "'/data/firmware/firmware.cpio.gz'")
			Expect(initrdIdx).To(BeNumerically(">", -1), "initrd not found in cat command")
			Expect(firmwareIdx).To(BeNumerically(">", -1), "firmware not found in cat command")
			Expect(initrdIdx).To(BeNumerically("<", firmwareIdx), "initrd must come before firmware in cat")
		})

		It("should not include firmware concatenation when firmwareBuildInfo is nil", func() {
			tasks := []downloadTask{
				{
					URL:        "https://example.com/vmlinuz",
					OutputPath: "/data/vmlinuz",
				},
				{
					URL:        "https://example.com/SHA256SUMS",
					OutputPath: "/data/SHA256SUMS",
				},
			}
			script := buildDownloadScript(tasks, nil, nil)
			Expect(script).NotTo(ContainSubstring("Building combined initrd with firmware"))
			Expect(script).NotTo(ContainSubstring("with-firmware"))
		})

		It("should include both ISO extraction and firmware concatenation", func() {
			tasks := []downloadTask{
				{
					URL:        "https://example.com/boot.iso",
					OutputPath: "/data/iso/boot.iso",
				},
				{
					URL:        "https://example.com/boot.iso.sha256",
					OutputPath: "/data/iso/boot.iso.sha256",
				},
				{
					URL:        "https://example.com/firmware.cpio.gz",
					OutputPath: "/data/firmware/firmware.cpio.gz",
				},
				{
					URL:        "https://example.com/firmware.sha256",
					OutputPath: "/data/firmware/firmware.sha256",
				},
			}
			iso := &isoExtractInfo{
				ISOPath:      "/data/iso/boot.iso",
				KernelSrc:    "/casper/vmlinuz",
				KernelDstDir: "/data/kernel",
				KernelDst:    "/data/kernel/vmlinuz",
				InitrdSrc:    "/casper/initrd",
				InitrdDstDir: "/data/initrd",
				InitrdDst:    "/data/initrd/initrd",
			}
			fw := &firmwareBuildInfo{
				FirmwarePath: "/data/firmware/firmware.cpio.gz",
				InitrdPath:   "/data/initrd/initrd",
				OutputDir:    "/data/initrd/with-firmware",
				OutputPath:   "/data/initrd/with-firmware/initrd",
			}
			script := buildDownloadScript(tasks, iso, fw)
			// ISO extraction
			Expect(script).To(ContainSubstring("Extracting from ISO"))
			Expect(script).To(ContainSubstring("mount -o ro,loop"))
			// Firmware concatenation
			Expect(script).To(ContainSubstring("Building combined initrd with firmware"))
			Expect(script).To(ContainSubstring("cat '/data/initrd/initrd' '/data/firmware/firmware.cpio.gz' > '/data/initrd/with-firmware/initrd'"))
		})

		It("should not include mount commands when isoExtractInfo is nil", func() {
			tasks := []downloadTask{
				{
					URL:        "https://example.com/vmlinuz",
					OutputPath: "/data/vmlinuz",
				},
				{
					URL:        "https://example.com/SHA256SUMS",
					OutputPath: "/data/SHA256SUMS",
				},
			}
			script := buildDownloadScript(tasks, nil, nil)
			Expect(script).NotTo(ContainSubstring("mount"))
			Expect(script).NotTo(ContainSubstring("Extracting from ISO"))
		})
	})

	Describe("relativeURLPath", func() {
		It("should return filename for same-directory URLs", func() {
			result := relativeURLPath(
				"https://example.com/dir/file",
				"https://example.com/dir/SHA256SUMS",
			)
			Expect(result).To(Equal("file"))
		})

		It("should return nested relative path", func() {
			result := relativeURLPath(
				"https://example.com/a/b/c/file",
				"https://example.com/a/SHA256SUMS",
			)
			Expect(result).To(Equal("b/c/file"))
		})

		It("should fall back to basename when shasum path has no slash", func() {
			result := relativeURLPath(
				"https://example.com/vmlinuz",
				"nopath",
			)
			Expect(result).To(Equal("vmlinuz"))
		})

		It("should handle Debian-style nested paths", func() {
			result := relativeURLPath(
				"https://deb.debian.org/debian/dists/trixie/main/installer-amd64/current/images/netboot/debian-installer/amd64/linux",
				"https://deb.debian.org/debian/dists/trixie/main/installer-amd64/current/images/SHA256SUMS",
			)
			Expect(result).To(Equal("netboot/debian-installer/amd64/linux"))
		})
	})

	Describe("buildDownloadJob", func() {
		newBootSource := func(name, ns string, spec isobootv1alpha1.BootSourceSpec) *isobootv1alpha1.BootSource {
			return &isobootv1alpha1.BootSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: ns,
				},
				Spec: spec,
			}
		}

		newScheme := func() *runtime.Scheme {
			s := runtime.NewScheme()
			Expect(isobootv1alpha1.AddToScheme(s)).To(Succeed())
			return s
		}

		It("should set correct name and namespace", func() {
			source := newBootSource("test-source", "kube-system", isobootv1alpha1.BootSourceSpec{
				Kernel: &isobootv1alpha1.KernelSource{
					URL: isobootv1alpha1.URLSource{
						Binary: "https://example.com/vmlinuz",
						Shasum: "https://example.com/vmlinuz.sha256",
					},
				},
				Initrd: &isobootv1alpha1.InitrdSource{
					URL: isobootv1alpha1.URLSource{
						Binary: "https://example.com/initrd.img",
						Shasum: "https://example.com/initrd.img.sha256",
					},
				},
			})
			job, err := buildDownloadJob(source, newScheme(), baseDir, testDownloadImage)
			Expect(err).NotTo(HaveOccurred())
			Expect(job.Name).To(Equal(downloadJobName("test-source")))
			Expect(job.Namespace).To(Equal("kube-system"))
		})

		It("should set OwnerReference", func() {
			source := newBootSource("test-source", "default", isobootv1alpha1.BootSourceSpec{
				Kernel: &isobootv1alpha1.KernelSource{
					URL: isobootv1alpha1.URLSource{
						Binary: "https://example.com/vmlinuz",
						Shasum: "https://example.com/vmlinuz.sha256",
					},
				},
				Initrd: &isobootv1alpha1.InitrdSource{
					URL: isobootv1alpha1.URLSource{
						Binary: "https://example.com/initrd.img",
						Shasum: "https://example.com/initrd.img.sha256",
					},
				},
			})
			job, err := buildDownloadJob(source, newScheme(), baseDir, testDownloadImage)
			Expect(err).NotTo(HaveOccurred())
			Expect(job.OwnerReferences).To(HaveLen(1))
			Expect(job.OwnerReferences[0].Name).To(Equal("test-source"))
		})

		It("should use the configured download image", func() {
			source := newBootSource("test-source", "default", isobootv1alpha1.BootSourceSpec{
				ISO: &isobootv1alpha1.ISOSource{
					URL: isobootv1alpha1.URLSource{
						Binary: "https://example.com/boot.iso",
						Shasum: "https://example.com/boot.iso.sha256",
					},
					Path: isobootv1alpha1.PathSource{Kernel: "/boot/vmlinuz", Initrd: "/boot/initrd.img"},
				},
			})
			job, err := buildDownloadJob(source, newScheme(), baseDir, testDownloadImage)
			Expect(err).NotTo(HaveOccurred())
			Expect(job.Spec.Template.Spec.Containers[0].Image).To(Equal(testDownloadImage))
		})

		It("should configure hostPath volume", func() {
			source := newBootSource("test-source", "default", isobootv1alpha1.BootSourceSpec{
				ISO: &isobootv1alpha1.ISOSource{
					URL: isobootv1alpha1.URLSource{
						Binary: "https://example.com/boot.iso",
						Shasum: "https://example.com/boot.iso.sha256",
					},
					Path: isobootv1alpha1.PathSource{Kernel: "/boot/vmlinuz", Initrd: "/boot/initrd.img"},
				},
			})
			job, err := buildDownloadJob(source, newScheme(), baseDir, testDownloadImage)
			Expect(err).NotTo(HaveOccurred())

			vols := job.Spec.Template.Spec.Volumes
			Expect(vols).To(HaveLen(1))
			Expect(vols[0].HostPath).NotTo(BeNil())
			Expect(vols[0].HostPath.Path).To(Equal("/var/lib/isoboot/default/test-source"))
		})

		It("should set backoff limit to 2", func() {
			source := newBootSource("test-source", "default", isobootv1alpha1.BootSourceSpec{
				ISO: &isobootv1alpha1.ISOSource{
					URL: isobootv1alpha1.URLSource{
						Binary: "https://example.com/boot.iso",
						Shasum: "https://example.com/boot.iso.sha256",
					},
					Path: isobootv1alpha1.PathSource{Kernel: "/boot/vmlinuz", Initrd: "/boot/initrd.img"},
				},
			})
			job, err := buildDownloadJob(source, newScheme(), baseDir, testDownloadImage)
			Expect(err).NotTo(HaveOccurred())
			Expect(*job.Spec.BackoffLimit).To(Equal(int32(2)))
		})

		It("should set expected labels", func() {
			source := newBootSource("label-source", "default", isobootv1alpha1.BootSourceSpec{
				ISO: &isobootv1alpha1.ISOSource{
					URL: isobootv1alpha1.URLSource{
						Binary: "https://example.com/boot.iso",
						Shasum: "https://example.com/boot.iso.sha256",
					},
					Path: isobootv1alpha1.PathSource{Kernel: "/boot/vmlinuz", Initrd: "/boot/initrd.img"},
				},
			})
			job, err := buildDownloadJob(source, newScheme(), baseDir, testDownloadImage)
			Expect(err).NotTo(HaveOccurred())
			Expect(job.Labels).To(HaveKeyWithValue("isoboot.github.io/bootsource-name", "label-source"))
			Expect(job.Labels).To(HaveKeyWithValue("app.kubernetes.io/managed-by", "isoboot"))
		})

		It("should append -download suffix to name", func() {
			Expect(downloadJobName("my-source")).To(Equal("my-source-download"))
		})

		It("should set SYS_ADMIN capability for ISO sources", func() {
			source := newBootSource("iso-source", "default", isobootv1alpha1.BootSourceSpec{
				ISO: &isobootv1alpha1.ISOSource{
					URL: isobootv1alpha1.URLSource{
						Binary: "https://example.com/boot.iso",
						Shasum: "https://example.com/boot.iso.sha256",
					},
					Path: isobootv1alpha1.PathSource{Kernel: "/boot/vmlinuz", Initrd: "/boot/initrd.img"},
				},
			})
			job, err := buildDownloadJob(source, newScheme(), baseDir, testDownloadImage)
			Expect(err).NotTo(HaveOccurred())
			sc := job.Spec.Template.Spec.Containers[0].SecurityContext
			Expect(sc).NotTo(BeNil())
			Expect(sc.Capabilities).NotTo(BeNil())
			Expect(sc.Capabilities.Add).To(ContainElement(corev1.Capability("SYS_ADMIN")))
		})

		It("should not set security context for non-ISO sources", func() {
			source := newBootSource("kernel-source", "default", isobootv1alpha1.BootSourceSpec{
				Kernel: &isobootv1alpha1.KernelSource{
					URL: isobootv1alpha1.URLSource{
						Binary: "https://example.com/vmlinuz",
						Shasum: "https://example.com/vmlinuz.sha256",
					},
				},
				Initrd: &isobootv1alpha1.InitrdSource{
					URL: isobootv1alpha1.URLSource{
						Binary: "https://example.com/initrd.img",
						Shasum: "https://example.com/initrd.img.sha256",
					},
				},
			})
			job, err := buildDownloadJob(source, newScheme(), baseDir, testDownloadImage)
			Expect(err).NotTo(HaveOccurred())
			Expect(job.Spec.Template.Spec.Containers[0].SecurityContext).To(BeNil())
		})

		It("should include firmware concatenation in script for firmware sources", func() {
			source := newTestBootSourceWithFirmware("fw-source", "default")
			job, err := buildDownloadJob(source, newScheme(), baseDir, testDownloadImage)
			Expect(err).NotTo(HaveOccurred())
			script := job.Spec.Template.Spec.Containers[0].Command[2]
			Expect(script).To(ContainSubstring("Building combined initrd with firmware"))
			Expect(script).To(ContainSubstring("with-firmware/initrd.img"))
			// Firmware doesn't need SYS_ADMIN
			Expect(job.Spec.Template.Spec.Containers[0].SecurityContext).To(BeNil())
		})
	})
})
