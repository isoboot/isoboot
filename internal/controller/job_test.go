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
			decoded, err := base64.StdEncoding.DecodeString(tasks[0].EncodedURL)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(decoded)).To(Equal("https://example.com/vmlinuz"))
			Expect(tasks[0].OutputPath).To(Equal("/var/lib/isoboot/default/my-source/kernel/vmlinuz"))

			// Verify kernel shasum task
			decoded, err = base64.StdEncoding.DecodeString(tasks[1].EncodedURL)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(decoded)).To(Equal("https://example.com/vmlinuz.sha256"))
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
					EncodedURL: base64.StdEncoding.EncodeToString([]byte("https://example.com/vmlinuz")),
					OutputPath: "/var/lib/isoboot/default/my-source/kernel/vmlinuz",
				},
			}
			script := buildDownloadScript(tasks)
			Expect(script).To(HavePrefix("set -eu\n"))
			Expect(script).To(ContainSubstring("apk add --no-cache wget"))
		})

		It("should contain base64-encoded URLs", func() {
			encoded := base64.StdEncoding.EncodeToString([]byte("https://example.com/vmlinuz"))
			tasks := []downloadTask{
				{
					EncodedURL: encoded,
					OutputPath: "/var/lib/isoboot/default/my-source/kernel/vmlinuz",
				},
			}
			script := buildDownloadScript(tasks)
			Expect(script).To(ContainSubstring(encoded))
		})

		It("should use wget -i to read URL from file", func() {
			tasks := []downloadTask{
				{
					EncodedURL: base64.StdEncoding.EncodeToString([]byte("https://example.com/vmlinuz")),
					OutputPath: "/var/lib/isoboot/default/my-source/kernel/vmlinuz",
				},
			}
			script := buildDownloadScript(tasks)
			Expect(script).To(ContainSubstring("wget -q -i '/tmp/url_0.txt'"))
		})

		It("should generate unique temp files per task", func() {
			tasks := []downloadTask{
				{
					EncodedURL: base64.StdEncoding.EncodeToString([]byte("https://example.com/a")),
					OutputPath: "/data/a",
				},
				{
					EncodedURL: base64.StdEncoding.EncodeToString([]byte("https://example.com/b")),
					OutputPath: "/data/b",
				},
			}
			script := buildDownloadScript(tasks)
			Expect(script).To(ContainSubstring("url_0.txt"))
			Expect(script).To(ContainSubstring("url_1.txt"))
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
			job, err := buildDownloadJob(source, newScheme(), baseDir)
			Expect(err).NotTo(HaveOccurred())
			Expect(job.Name).To(Equal("isoboot-download-test-source"))
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
			job, err := buildDownloadJob(source, newScheme(), baseDir)
			Expect(err).NotTo(HaveOccurred())
			Expect(job.OwnerReferences).To(HaveLen(1))
			Expect(job.OwnerReferences[0].Name).To(Equal("test-source"))
		})

		It("should use alpine image", func() {
			source := newBootSource("test-source", "default", isobootv1alpha1.BootSourceSpec{
				ISO: &isobootv1alpha1.ISOSource{
					URL: isobootv1alpha1.URLSource{
						Binary: "https://example.com/boot.iso",
						Shasum: "https://example.com/boot.iso.sha256",
					},
					Path: isobootv1alpha1.PathSource{Kernel: "/boot/vmlinuz", Initrd: "/boot/initrd.img"},
				},
			})
			job, err := buildDownloadJob(source, newScheme(), baseDir)
			Expect(err).NotTo(HaveOccurred())
			Expect(job.Spec.Template.Spec.Containers[0].Image).To(Equal("alpine:3.21"))
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
			job, err := buildDownloadJob(source, newScheme(), baseDir)
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
			job, err := buildDownloadJob(source, newScheme(), baseDir)
			Expect(err).NotTo(HaveOccurred())
			Expect(*job.Spec.BackoffLimit).To(Equal(int32(2)))
		})

		It("should set expected labels", func() {
			source := newBootSource("test-source", "default", isobootv1alpha1.BootSourceSpec{
				ISO: &isobootv1alpha1.ISOSource{
					URL: isobootv1alpha1.URLSource{
						Binary: "https://example.com/boot.iso",
						Shasum: "https://example.com/boot.iso.sha256",
					},
					Path: isobootv1alpha1.PathSource{Kernel: "/boot/vmlinuz", Initrd: "/boot/initrd.img"},
				},
			})
			job, err := buildDownloadJob(source, newScheme(), baseDir)
			Expect(err).NotTo(HaveOccurred())
			Expect(job.Labels).To(HaveKeyWithValue("isoboot.github.io/bootsource-name", "test-source"))
			Expect(job.Labels).To(HaveKeyWithValue("app.kubernetes.io/managed-by", "isoboot"))
		})

		It("should truncate long names to 63 characters", func() {
			longName := strings.Repeat("a", 80)
			source := newBootSource(longName, "default", isobootv1alpha1.BootSourceSpec{
				ISO: &isobootv1alpha1.ISOSource{
					URL: isobootv1alpha1.URLSource{
						Binary: "https://example.com/boot.iso",
						Shasum: "https://example.com/boot.iso.sha256",
					},
					Path: isobootv1alpha1.PathSource{Kernel: "/boot/vmlinuz", Initrd: "/boot/initrd.img"},
				},
			})
			job, err := buildDownloadJob(source, newScheme(), baseDir)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(job.Name)).To(BeNumerically("<=", 63))
		})
	})
})
