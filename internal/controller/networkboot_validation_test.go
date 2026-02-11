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
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	bootv1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
)

// ── Helpers ──

func pair(binary, hash bootv1alpha1.URL) bootv1alpha1.BinaryHashPair {
	return bootv1alpha1.BinaryHashPair{Binary: binary, Hash: hash}
}

var (
	validPair    = pair("https://example.com/artifact", "https://example.com/artifact.sha256")
	validFWPair  = pair("https://fw.example.com/fw.bin", "https://fw.example.com/fw.bin.sha256")
	validISOPair = pair("https://releases.ubuntu.com/noble.iso", "https://releases.ubuntu.com/noble.iso.sha256")
)

func directBootSpec(kernel, initrd bootv1alpha1.BinaryHashPair) bootv1alpha1.NetworkBootSpec {
	return bootv1alpha1.NetworkBootSpec{
		Kernel: &kernel,
		Initrd: &initrd,
	}
}

func isoBootSpec(isoPair bootv1alpha1.BinaryHashPair, kernel, initrd string) bootv1alpha1.NetworkBootSpec {
	return bootv1alpha1.NetworkBootSpec{
		ISO: &bootv1alpha1.ISOSpec{
			BinaryHashPair: isoPair,
			Kernel:         kernel,
			Initrd:         initrd,
		},
	}
}

func withFirmware(spec bootv1alpha1.NetworkBootSpec, fwPair bootv1alpha1.BinaryHashPair, prefix *string) bootv1alpha1.NetworkBootSpec {
	spec.Firmware = &bootv1alpha1.FirmwareSpec{
		BinaryHashPair: fwPair,
		Prefix:         prefix,
	}
	return spec
}

func createNetworkBoot(name string, spec bootv1alpha1.NetworkBootSpec) error {
	return k8sClient.Create(context.Background(), &bootv1alpha1.NetworkBoot{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       spec,
	})
}

func deleteNetworkBoot(name string) {
	_ = k8sClient.Delete(context.Background(), &bootv1alpha1.NetworkBoot{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
	})
}

func ptr(s string) *string { return &s }

// ── Tests ──

var _ = Describe("NetworkBoot Validation", func() {

	Describe("valid resources", func() {
		AfterEach(func() { deleteNetworkBoot("valid-test") })

		DescribeTable("should accept",
			func(spec bootv1alpha1.NetworkBootSpec) {
				Expect(createNetworkBoot("valid-test", spec)).To(Succeed())
			},
			Entry("direct boot with kernel and initrd",
				directBootSpec(validPair, validPair)),
			Entry("ISO boot",
				isoBootSpec(validISOPair, "/casper/vmlinuz", "/casper/initrd")),
			Entry("direct boot with firmware",
				withFirmware(directBootSpec(validPair, validPair), validFWPair, nil)),
			Entry("ISO boot with firmware",
				withFirmware(isoBootSpec(validISOPair, "/casper/vmlinuz", "/casper/initrd"), validFWPair, nil)),
			Entry("firmware with custom prefix",
				withFirmware(directBootSpec(validPair, validPair), validFWPair, ptr("/fw"))),
			Entry("URLs with ports",
				directBootSpec(
					pair("https://example.com:8443/vmlinuz", "https://example.com:8443/vmlinuz.sha256"),
					pair("https://example.com:8443/initrd", "https://example.com:8443/initrd.sha256"))),
			Entry("deeply nested ISO paths",
				isoBootSpec(validISOPair, "/casper/hwe/vmlinuz.efi", "/casper/hwe/initrd.lz")),
		)
	})

	Describe("boot mode XOR", func() {
		DescribeTable("should reject",
			func(spec bootv1alpha1.NetworkBootSpec) {
				Expect(createNetworkBoot("xor-test", spec)).NotTo(Succeed())
			},
			Entry("both kernel/initrd and iso set", func() bootv1alpha1.NetworkBootSpec {
				spec := directBootSpec(validPair, validPair)
				spec.ISO = &bootv1alpha1.ISOSpec{
					BinaryHashPair: validISOPair,
					Kernel:         "/casper/vmlinuz",
					Initrd:         "/casper/initrd",
				}
				return spec
			}()),
			Entry("neither kernel/initrd nor iso set",
				bootv1alpha1.NetworkBootSpec{}),
			Entry("kernel without initrd",
				bootv1alpha1.NetworkBootSpec{Kernel: &bootv1alpha1.BinaryHashPair{
					Binary: "https://example.com/vmlinuz", Hash: "https://example.com/vmlinuz.sha256"}}),
			Entry("initrd without kernel",
				bootv1alpha1.NetworkBootSpec{Initrd: &bootv1alpha1.BinaryHashPair{
					Binary: "https://example.com/initrd", Hash: "https://example.com/initrd.sha256"}}),
			Entry("firmware only without boot mode",
				withFirmware(bootv1alpha1.NetworkBootSpec{}, validFWPair, nil)),
		)
	})

	Describe("URL validation", func() {
		DescribeTable("should reject invalid URLs",
			func(binary, hash bootv1alpha1.URL) {
				spec := directBootSpec(pair(binary, hash), validPair)
				Expect(createNetworkBoot("url-test", spec)).NotTo(Succeed())
			},
			Entry("http instead of https",
				bootv1alpha1.URL("http://example.com/vmlinuz"),
				bootv1alpha1.URL("http://example.com/vmlinuz.sha256")),
			Entry("no path after host",
				bootv1alpha1.URL("https://example.com"),
				bootv1alpha1.URL("https://example.com")),
			Entry("no host",
				bootv1alpha1.URL("https:///vmlinuz"),
				bootv1alpha1.URL("https:///vmlinuz.sha256")),
			Entry("trailing slash only as path",
				bootv1alpha1.URL("https://example.com/"),
				bootv1alpha1.URL("https://example.com/")),
			Entry("userinfo with @ in URL",
				bootv1alpha1.URL("https://user:pass@example.com/vmlinuz"),
				bootv1alpha1.URL("https://user:pass@example.com/vmlinuz.sha256")),
			Entry("empty string",
				bootv1alpha1.URL(""),
				bootv1alpha1.URL("")),
			Entry("exceeding max length",
				bootv1alpha1.URL("https://example.com/"+strings.Repeat("a", 2048)),
				bootv1alpha1.URL("https://example.com/"+strings.Repeat("a", 2048))),
		)

		It("should reject invalid URL in ISO position", func() {
			spec := isoBootSpec(
				pair("http://example.com/noble.iso", "http://example.com/noble.iso.sha256"),
				"/casper/vmlinuz", "/casper/initrd")
			Expect(createNetworkBoot("url-iso-test", spec)).NotTo(Succeed())
		})
	})

	Describe("hostname matching", func() {
		DescribeTable("should reject mismatched hostnames",
			func(spec bootv1alpha1.NetworkBootSpec) {
				Expect(createNetworkBoot("host-test", spec)).NotTo(Succeed())
			},
			Entry("kernel binary vs hash",
				directBootSpec(
					pair("https://host-a.com/vmlinuz", "https://host-b.com/vmlinuz.sha256"),
					validPair)),
			Entry("initrd binary vs hash",
				directBootSpec(
					validPair,
					pair("https://host-a.com/initrd", "https://host-b.com/initrd.sha256"))),
			Entry("ISO binary vs hash",
				isoBootSpec(
					pair("https://host-a.com/noble.iso", "https://host-b.com/noble.iso.sha256"),
					"/casper/vmlinuz", "/casper/initrd")),
			Entry("firmware binary vs hash",
				withFirmware(directBootSpec(validPair, validPair),
					pair("https://host-a.com/fw.bin", "https://host-b.com/fw.sha256"), nil)),
			Entry("hostname differs by port",
				directBootSpec(
					pair("https://example.com:8443/vmlinuz", "https://example.com:9443/vmlinuz.sha256"),
					validPair)),
		)
	})

	Describe("ISO path validation", func() {
		DescribeTable("should reject invalid ISO paths",
			func(kernel, initrd string) {
				Expect(createNetworkBoot("iso-test",
					isoBootSpec(validISOPair, kernel, initrd))).NotTo(Succeed())
			},
			Entry("kernel without leading slash", "casper/vmlinuz", "/casper/initrd"),
			Entry("initrd without leading slash", "/casper/vmlinuz", "casper/initrd"),
			Entry("kernel path traversal mid-path", "/casper/../etc/passwd", "/casper/initrd"),
			Entry("initrd path traversal mid-path", "/casper/vmlinuz", "/casper/../etc/passwd"),
			Entry("kernel path traversal at end", "/casper/..", "/casper/initrd"),
			Entry("initrd path traversal at end", "/casper/vmlinuz", "/casper/.."),
			Entry("kernel is bare /..", "/..", "/casper/initrd"),
			Entry("initrd is bare /..", "/casper/vmlinuz", "/.."),
			Entry("kernel exceeding max length", "/"+strings.Repeat("a", 1024), "/casper/initrd"),
		)
	})

	Describe("firmware prefix validation", func() {
		specWithPrefix := func(prefix string) bootv1alpha1.NetworkBootSpec {
			return withFirmware(directBootSpec(validPair, validPair), validFWPair, ptr(prefix))
		}

		DescribeTable("should reject invalid prefixes",
			func(prefix string) {
				Expect(createNetworkBoot("prefix-test", specWithPrefix(prefix))).NotTo(Succeed())
			},
			Entry("bare slash", "/"),
			Entry("trailing slash", "/firmware/"),
			Entry("no leading slash", "firmware"),
			Entry("path traversal mid-path", "/firm/../ware"),
			Entry("path traversal at end", "/firmware/.."),
			Entry("bare /..", "/.."),
			Entry("double slash start", "//firmware"),
			Entry("exceeding max length", "/"+strings.Repeat("a", 256)),
		)

		DescribeTable("should accept valid prefixes",
			func(prefix string) {
				Expect(createNetworkBoot("prefix-test", specWithPrefix(prefix))).To(Succeed())
				deleteNetworkBoot("prefix-test")
			},
			Entry("minimum length", "/a"),
			Entry("typical prefix", "/with-firmware"),
			Entry("nested path", "/boot/firmware"),
		)

		It("should default prefix to /with-firmware when not set", func() {
			spec := withFirmware(directBootSpec(validPair, validPair), validFWPair, nil)
			name := fmt.Sprintf("prefix-default-%d", GinkgoRandomSeed())
			Expect(createNetworkBoot(name, spec)).To(Succeed())

			created := &bootv1alpha1.NetworkBoot{}
			Expect(k8sClient.Get(context.Background(),
				client.ObjectKey{Name: name, Namespace: "default"}, created)).To(Succeed())
			Expect(created.Spec.Firmware.Prefix).NotTo(BeNil())
			Expect(*created.Spec.Firmware.Prefix).To(Equal("/with-firmware"))
			deleteNetworkBoot(name)
		})
	})
})
