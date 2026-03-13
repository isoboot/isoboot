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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	isobootgithubiov1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
)

var _ = Describe("PendingProvisionForMAC", func() {
	const ns = "default"

	createMachine := func(name, mac string) *isobootgithubiov1alpha1.Machine {
		m := &isobootgithubiov1alpha1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: ns,
			},
			Spec: isobootgithubiov1alpha1.MachineSpec{MAC: mac},
		}
		Expect(k8sClient.Create(ctx, m)).To(Succeed())
		return m
	}

	createProvision := func(
		name, machineRef string, phase isobootgithubiov1alpha1.ProvisionPhase,
	) *isobootgithubiov1alpha1.Provision {
		p := &isobootgithubiov1alpha1.Provision{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: ns,
			},
			Spec: isobootgithubiov1alpha1.ProvisionSpec{
				MachineRef:         machineRef,
				BootConfigRef:      "bootconfig-1",
				ProvisionAnswerRef: "answer-1",
			},
		}
		Expect(k8sClient.Create(ctx, p)).To(Succeed())
		if phase != "" {
			p.Status.Phase = phase
			Expect(k8sClient.Status().Update(ctx, p)).To(Succeed())
		}
		return p
	}

	It("returns nil when no machine exists for MAC", func() {
		// Wait for the cache to be ready before querying.
		Eventually(func() error {
			_, err := PendingProvisionForMAC(
				ctx, indexedClient, ns, "00-00-00-00-00-01")
			return err
		}).Should(Succeed())

		result, err := PendingProvisionForMAC(
			ctx, indexedClient, ns, "00-00-00-00-00-01")
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(BeNil())
	})

	It("returns nil when machine exists but no provisions", func() {
		m := createMachine("ppm-m1", "aa-00-00-00-00-01")
		defer func() {
			Expect(k8sClient.Delete(ctx, m)).To(Succeed())
		}()

		Eventually(func() *isobootgithubiov1alpha1.Provision {
			r, _ := PendingProvisionForMAC(
				ctx, indexedClient, ns, "aa-00-00-00-00-01")
			return r
		}).Should(BeNil())

		result, err := PendingProvisionForMAC(
			ctx, indexedClient, ns, "aa-00-00-00-00-01")
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(BeNil())
	})

	It("returns nil when provision exists but is not pending", func() {
		m := createMachine("ppm-m2", "aa-00-00-00-00-02")
		p := createProvision("ppm-p2", "ppm-m2",
			isobootgithubiov1alpha1.ProvisionPhaseComplete)
		defer func() {
			Expect(k8sClient.Delete(ctx, p)).To(Succeed())
			Expect(k8sClient.Delete(ctx, m)).To(Succeed())
		}()

		// Wait for the provision to appear in the cache.
		Eventually(func() int {
			var list isobootgithubiov1alpha1.ProvisionList
			_ = indexedClient.List(ctx, &list)
			return len(list.Items)
		}).Should(BeNumerically(">=", 1))

		result, err := PendingProvisionForMAC(
			ctx, indexedClient, ns, "aa-00-00-00-00-02")
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(BeNil())
	})

	It("returns the provision when exactly one pending match", func() {
		m := createMachine("ppm-m3", "aa-00-00-00-00-03")
		p := createProvision("ppm-p3", "ppm-m3",
			isobootgithubiov1alpha1.ProvisionPhasePending)
		defer func() {
			Expect(k8sClient.Delete(ctx, p)).To(Succeed())
			Expect(k8sClient.Delete(ctx, m)).To(Succeed())
		}()

		var result *isobootgithubiov1alpha1.Provision
		Eventually(func() *isobootgithubiov1alpha1.Provision {
			result, _ = PendingProvisionForMAC(
				ctx, indexedClient, ns, "aa-00-00-00-00-03")
			return result
		}).ShouldNot(BeNil())

		Expect(result.Name).To(Equal("ppm-p3"))
	})

	It("returns pending provision and ignores complete", func() {
		m := createMachine("ppm-m4", "aa-00-00-00-00-04")
		p1 := createProvision("ppm-p4a", "ppm-m4",
			isobootgithubiov1alpha1.ProvisionPhasePending)
		p2 := createProvision("ppm-p4b", "ppm-m4",
			isobootgithubiov1alpha1.ProvisionPhaseComplete)
		defer func() {
			Expect(k8sClient.Delete(ctx, p1)).To(Succeed())
			Expect(k8sClient.Delete(ctx, p2)).To(Succeed())
			Expect(k8sClient.Delete(ctx, m)).To(Succeed())
		}()

		var result *isobootgithubiov1alpha1.Provision
		Eventually(func() *isobootgithubiov1alpha1.Provision {
			result, _ = PendingProvisionForMAC(
				ctx, indexedClient, ns, "aa-00-00-00-00-04")
			return result
		}).ShouldNot(BeNil())

		Expect(result.Name).To(Equal("ppm-p4a"))
	})

	It("returns error when multiple pending provisions", func() {
		m := createMachine("ppm-m5", "aa-00-00-00-00-05")
		p1 := createProvision("ppm-p5a", "ppm-m5",
			isobootgithubiov1alpha1.ProvisionPhasePending)
		p2 := createProvision("ppm-p5b", "ppm-m5",
			isobootgithubiov1alpha1.ProvisionPhasePending)
		defer func() {
			Expect(k8sClient.Delete(ctx, p1)).To(Succeed())
			Expect(k8sClient.Delete(ctx, p2)).To(Succeed())
			Expect(k8sClient.Delete(ctx, m)).To(Succeed())
		}()

		Eventually(func() error {
			_, err := PendingProvisionForMAC(
				ctx, indexedClient, ns, "aa-00-00-00-00-05")
			return err
		}).Should(MatchError(ContainSubstring(
			"multiple pending provisions")))
	})
})
