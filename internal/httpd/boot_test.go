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

package httpd

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	isobootgithubiov1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
)

var _ = Describe("BootDirectiveForMAC", func() {
	const ns = "default"

	sha256 := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

	createBootConfig := func(
		name, kernelRef, initrdRef, kernelArgs string,
	) *isobootgithubiov1alpha1.BootConfig {
		bc := &isobootgithubiov1alpha1.BootConfig{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec: isobootgithubiov1alpha1.BootConfigSpec{
				Netboot: &isobootgithubiov1alpha1.BootConfigNetbootSpec{
					KernelRef: kernelRef,
					InitrdRef: initrdRef,
				},
				KernelArgs: kernelArgs,
			},
		}
		Expect(k8sClient.Create(ctx, bc)).To(Succeed())
		return bc
	}

	createArtifact := func(
		name, artifactURL string,
	) *isobootgithubiov1alpha1.BootArtifact {
		a := &isobootgithubiov1alpha1.BootArtifact{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec: isobootgithubiov1alpha1.BootArtifactSpec{
				URL:    artifactURL,
				SHA256: &sha256,
			},
		}
		Expect(k8sClient.Create(ctx, a)).To(Succeed())
		return a
	}

	It("returns nil when no pending provision exists", func() {
		result, err := BootDirectiveForMAC(
			ctx, indexedClient, ns, "bb-00-00-00-00-01")
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(BeNil())
	})

	It("returns directive for pending provision", func() {
		m := createMachine("bd-m1", "bb-00-00-00-00-02")
		ka := createArtifact("bd-kernel-1",
			"https://example.com/vmlinuz")
		ia := createArtifact("bd-initrd-1",
			"https://example.com/initrd.img")
		bc := createBootConfig("bd-bc1",
			"bd-kernel-1", "bd-initrd-1", "console=ttyS0")
		p := createProvision("bd-p1", "bd-m1", "bd-bc1",
			isobootgithubiov1alpha1.ProvisionPhasePending)
		defer func() {
			Expect(k8sClient.Delete(ctx, p)).To(Succeed())
			Expect(k8sClient.Delete(ctx, bc)).To(Succeed())
			Expect(k8sClient.Delete(ctx, ia)).To(Succeed())
			Expect(k8sClient.Delete(ctx, ka)).To(Succeed())
			Expect(k8sClient.Delete(ctx, m)).To(Succeed())
		}()

		var result *BootDirective
		Eventually(func() *BootDirective {
			result, _ = BootDirectiveForMAC(
				ctx, indexedClient, ns, "bb-00-00-00-00-02")
			return result
		}).ShouldNot(BeNil())

		Expect(result.KernelPath).To(Equal("bd-bc1/kernel/vmlinuz"))
		Expect(result.KernelArgs).To(Equal("console=ttyS0"))
		Expect(result.InitrdPath).To(Equal("bd-bc1/initrd/initrd.img"))
		Expect(result.ProvisionName).To(Equal("bd-p1"))
	})

	It("returns directive with empty kernel args", func() {
		m := createMachine("bd-m3", "bb-00-00-00-00-04")
		ka := createArtifact("bd-kernel-2",
			"https://example.com/vmlinuz")
		ia := createArtifact("bd-initrd-2",
			"https://example.com/initrd.img")
		bc := createBootConfig("bd-bc2",
			"bd-kernel-2", "bd-initrd-2", "")
		p := createProvision("bd-p3", "bd-m3", "bd-bc2",
			isobootgithubiov1alpha1.ProvisionPhasePending)
		defer func() {
			Expect(k8sClient.Delete(ctx, p)).To(Succeed())
			Expect(k8sClient.Delete(ctx, bc)).To(Succeed())
			Expect(k8sClient.Delete(ctx, ia)).To(Succeed())
			Expect(k8sClient.Delete(ctx, ka)).To(Succeed())
			Expect(k8sClient.Delete(ctx, m)).To(Succeed())
		}()

		var result *BootDirective
		Eventually(func() *BootDirective {
			result, _ = BootDirectiveForMAC(
				ctx, indexedClient, ns, "bb-00-00-00-00-04")
			return result
		}).ShouldNot(BeNil())

		Expect(result.KernelArgs).To(BeEmpty())
	})

	It("returns ISO-mode directive with kernel args", func() {
		m := createMachine("bd-m4", "bb-00-00-00-00-05")
		ia := createArtifact("bd-iso-1", "https://example.com/ubuntu.iso")
		bc := &isobootgithubiov1alpha1.BootConfig{
			ObjectMeta: metav1.ObjectMeta{Name: "bd-bc3", Namespace: ns},
			Spec: isobootgithubiov1alpha1.BootConfigSpec{
				ISO: &isobootgithubiov1alpha1.BootConfigISOSpec{
					ArtifactRef: "bd-iso-1",
					KernelPath:  "casper/vmlinuz",
					InitrdPath:  "casper/initrd",
				},
				KernelArgs: "autoinstall ds=nocloud-net",
			},
		}
		Expect(k8sClient.Create(ctx, bc)).To(Succeed())
		p := createProvision("bd-p4", "bd-m4", "bd-bc3",
			isobootgithubiov1alpha1.ProvisionPhasePending)
		defer func() {
			Expect(k8sClient.Delete(ctx, p)).To(Succeed())
			Expect(k8sClient.Delete(ctx, bc)).To(Succeed())
			Expect(k8sClient.Delete(ctx, ia)).To(Succeed())
			Expect(k8sClient.Delete(ctx, m)).To(Succeed())
		}()

		var result *BootDirective
		Eventually(func() *BootDirective {
			result, _ = BootDirectiveForMAC(
				ctx, indexedClient, ns, "bb-00-00-00-00-05")
			return result
		}).ShouldNot(BeNil())

		Expect(result.KernelPath).To(Equal("bd-bc3/vmlinuz"))
		Expect(result.InitrdPath).To(Equal("bd-bc3/initrd"))
		Expect(result.ISOPath).To(Equal("bd-bc3/image.iso"))
		Expect(result.KernelArgs).To(Equal("autoinstall ds=nocloud-net"))
		Expect(result.ProvisionName).To(Equal("bd-p4"))
	})

	It("returns error when boot config not found", func() {
		m := createMachine("bd-m2", "bb-00-00-00-00-03")
		p := createProvision("bd-p2", "bd-m2", "nonexistent-bc",
			isobootgithubiov1alpha1.ProvisionPhasePending)
		defer func() {
			Expect(k8sClient.Delete(ctx, p)).To(Succeed())
			Expect(k8sClient.Delete(ctx, m)).To(Succeed())
		}()

		Eventually(func() error {
			_, err := BootDirectiveForMAC(
				ctx, indexedClient, ns, "bb-00-00-00-00-03")
			return err
		}).Should(MatchError(ContainSubstring("getting boot config")))
	})
})

var _ = Describe("RenderKernelArgs", func() {
	data := KernelArgsData{
		ProvisionAutomationBaseURL: "http://10.0.0.1:8080/dynamic/automation/my-provision",
	}

	It("passes through plain string unchanged", func() {
		result, err := RenderKernelArgs("console=ttyS0 ip=dhcp", data)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal("console=ttyS0 ip=dhcp"))
	})

	It("renders ProvisionAutomationBaseURL", func() {
		result, err := RenderKernelArgs(
			"ip=dhcp inst.ks={{.ProvisionAutomationBaseURL}}/ks.cfg", data)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(
			"ip=dhcp inst.ks=http://10.0.0.1:8080/dynamic/automation/my-provision/ks.cfg"))
	})

	It("renders ProxyURL", func() {
		result, err := RenderKernelArgs(
			"ip=dhcp inst.proxy={{.ProxyURL}} inst.ks={{.ProvisionAutomationBaseURL}}/ks.cfg",
			KernelArgsData{
				ProvisionAutomationBaseURL: "http://10.0.0.1:8080/dynamic/automation/my-provision",
				ProxyURL:                   "http://10.0.0.1:3128",
			})
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(
			"ip=dhcp inst.proxy=http://10.0.0.1:3128 inst.ks=http://10.0.0.1:8080/dynamic/automation/my-provision/ks.cfg"))
	})

	It("omits ProxyURL block when empty", func() {
		result, err := RenderKernelArgs(
			"ip=dhcp {{if .ProxyURL}}inst.proxy={{.ProxyURL}} {{end}}inst.repo=https://example.com",
			KernelArgsData{
				ProvisionAutomationBaseURL: "http://10.0.0.1:8080/dynamic/automation/my-provision",
			})
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal("ip=dhcp inst.repo=https://example.com"))
	})

	It("returns empty string for empty input", func() {
		result, err := RenderKernelArgs("", data)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(BeEmpty())
	})

	It("returns error for invalid template syntax", func() {
		_, err := RenderKernelArgs("{{.Foo", data)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("parsing kernel args template"))
	})

	It("returns error for unknown variable", func() {
		_, err := RenderKernelArgs("{{.UnknownVar}}", data)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("executing kernel args template"))
	})

	It("renders UpdatePhaseURL and ProvisionName", func() {
		result, err := RenderKernelArgs(
			"ip=dhcp inst.ks={{.ProvisionAutomationBaseURL}}/ks.cfg inst.status={{.UpdatePhaseURL}} inst.provname={{.ProvisionName}}",
			KernelArgsData{
				ProvisionAutomationBaseURL: "http://10.0.0.1:8080/dynamic/automation/my-provision",
				UpdatePhaseURL:             "http://10.0.0.1:8080/dynamic/status",
				ProvisionName:              "my-provision",
			})
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(ContainSubstring("inst.status=http://10.0.0.1:8080/dynamic/status"))
		Expect(result).To(ContainSubstring("inst.provname=my-provision"))
	})

	It("renders ISOURL for ISO-mode autoinstall", func() {
		result, err := RenderKernelArgs(
			"ip=dhcp url={{.ISOURL}} autoinstall ds=nocloud-net;s={{.ProvisionAutomationBaseURL}}/",
			KernelArgsData{
				ProvisionAutomationBaseURL: "http://10.0.0.1:8080/dynamic/automation/my-provision",
				ISOURL:                     "http://10.0.0.1:8080/static/ubuntu-26.04/image.iso",
			})
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(
			"ip=dhcp url=http://10.0.0.1:8080/static/ubuntu-26.04/image.iso autoinstall ds=nocloud-net;s=http://10.0.0.1:8080/dynamic/automation/my-provision/"))
	})
})

var _ = Describe("IsDuplicateError", func() {
	It("returns false for nil", func() {
		Expect(IsDuplicateError(nil)).To(BeFalse())
	})

	It("returns true for wrapped ErrMultipleMachines", func() {
		Expect(IsDuplicateError(fmt.Errorf(
			"%w with MAC aa-bb-cc-dd-ee-ff",
			ErrMultipleMachines))).To(BeTrue())
	})

	It("returns true for wrapped ErrMultipleProvisions", func() {
		Expect(IsDuplicateError(fmt.Errorf(
			"%w for MAC aa",
			ErrMultipleProvisions))).To(BeTrue())
	})

	It("returns false for other errors", func() {
		Expect(IsDuplicateError(fmt.Errorf(
			"listing machines: connection refused"))).To(BeFalse())
	})
})
