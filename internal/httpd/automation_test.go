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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	isobootgithubiov1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
)

var _ = Describe("RenderAutomationFile", func() {
	const ns = "default"

	createProvisionAutomation := func(
		name string, files map[string]string,
	) *isobootgithubiov1alpha1.ProvisionAutomation {
		pa := &isobootgithubiov1alpha1.ProvisionAutomation{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec: isobootgithubiov1alpha1.ProvisionAutomationSpec{
				Files: files,
			},
		}
		ExpectWithOffset(1, k8sClient.Create(ctx, pa)).To(Succeed())
		return pa
	}

	createProvisionWithRefs := func(
		name, machineRef, bootConfigRef, automationRef string,
		configMaps, secrets []string,
	) *isobootgithubiov1alpha1.Provision {
		p := &isobootgithubiov1alpha1.Provision{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec: isobootgithubiov1alpha1.ProvisionSpec{
				MachineRef:             machineRef,
				BootConfigRef:          bootConfigRef,
				ProvisionAutomationRef: automationRef,
				ConfigMaps:             configMaps,
				Secrets:                secrets,
			},
		}
		ExpectWithOffset(1, k8sClient.Create(ctx, p)).To(Succeed())
		return p
	}

	createConfigMap := func(name string, data map[string]string) *corev1.ConfigMap {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Data:       data,
		}
		ExpectWithOffset(1, k8sClient.Create(ctx, cm)).To(Succeed())
		return cm
	}

	createSecret := func(name string, data map[string][]byte) *corev1.Secret {
		s := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Data:       data,
		}
		ExpectWithOffset(1, k8sClient.Create(ctx, s)).To(Succeed())
		return s
	}

	It("returns error when provision not found", func() {
		_, err := RenderAutomationFile(
			ctx, k8sClient, ns, "nonexistent", "kickstart.cfg")
		Expect(err).To(MatchError(ContainSubstring("getting provision")))
	})

	It("returns error when automation not found", func() {
		p := createProvisionWithRefs(
			"ra-p1", "ra-m1", "ra-bc1", "nonexistent-pa", nil, nil)
		defer func() {
			Expect(k8sClient.Delete(ctx, p)).To(Succeed())
		}()

		_, err := RenderAutomationFile(
			ctx, k8sClient, ns, "ra-p1", "kickstart.cfg")
		Expect(err).To(MatchError(
			ContainSubstring("getting provision automation")))
	})

	It("returns error when file not found in automation", func() {
		pa := createProvisionAutomation("ra-pa1", map[string]string{
			"kickstart.cfg": "content",
		})
		p := createProvisionWithRefs(
			"ra-p2", "ra-m2", "ra-bc2", "ra-pa1", nil, nil)
		defer func() {
			Expect(k8sClient.Delete(ctx, p)).To(Succeed())
			Expect(k8sClient.Delete(ctx, pa)).To(Succeed())
		}()

		_, err := RenderAutomationFile(
			ctx, k8sClient, ns, "ra-p2", "missing.cfg")
		Expect(err).To(MatchError(ContainSubstring("missing.cfg")))
		Expect(IsAutomationNotFound(err)).To(BeTrue())
	})

	It("renders a static template without data", func() {
		pa := createProvisionAutomation("ra-pa2", map[string]string{
			"kickstart.cfg": "lang en_US.UTF-8\nkeyboard us\n",
		})
		p := createProvisionWithRefs(
			"ra-p3", "ra-m3", "ra-bc3", "ra-pa2", nil, nil)
		defer func() {
			Expect(k8sClient.Delete(ctx, p)).To(Succeed())
			Expect(k8sClient.Delete(ctx, pa)).To(Succeed())
		}()

		result, err := RenderAutomationFile(
			ctx, k8sClient, ns, "ra-p3", "kickstart.cfg")
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal("lang en_US.UTF-8\nkeyboard us\n"))
	})

	It("renders a template with configmap data", func() {
		cm := createConfigMap("ra-cm1", map[string]string{
			"hostname": "my-server",
			"timezone": "UTC",
		})
		pa := createProvisionAutomation("ra-pa3", map[string]string{
			"kickstart.cfg": "network --hostname={{ index .ConfigMaps \"hostname\" }}\ntimezone {{ index .ConfigMaps \"timezone\" }}\n",
		})
		p := createProvisionWithRefs(
			"ra-p4", "ra-m4", "ra-bc4", "ra-pa3",
			[]string{"ra-cm1"}, nil)
		defer func() {
			Expect(k8sClient.Delete(ctx, p)).To(Succeed())
			Expect(k8sClient.Delete(ctx, pa)).To(Succeed())
			Expect(k8sClient.Delete(ctx, cm)).To(Succeed())
		}()

		result, err := RenderAutomationFile(
			ctx, k8sClient, ns, "ra-p4", "kickstart.cfg")
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(
			"network --hostname=my-server\ntimezone UTC\n"))
	})

	It("renders a template with secret data", func() {
		s := createSecret("ra-s1", map[string][]byte{
			"password": []byte("s3cret"),
		})
		pa := createProvisionAutomation("ra-pa4", map[string]string{
			"kickstart.cfg": "rootpw {{ index .Secrets \"password\" }}\n",
		})
		p := createProvisionWithRefs(
			"ra-p5", "ra-m5", "ra-bc5", "ra-pa4",
			nil, []string{"ra-s1"})
		defer func() {
			Expect(k8sClient.Delete(ctx, p)).To(Succeed())
			Expect(k8sClient.Delete(ctx, pa)).To(Succeed())
			Expect(k8sClient.Delete(ctx, s)).To(Succeed())
		}()

		result, err := RenderAutomationFile(
			ctx, k8sClient, ns, "ra-p5", "kickstart.cfg")
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal("rootpw s3cret\n"))
	})

	It("later configmaps override earlier ones", func() {
		cm1 := createConfigMap("ra-cm-general", map[string]string{
			"hostname": "default-host",
			"timezone": "UTC",
		})
		cm2 := createConfigMap("ra-cm-specific", map[string]string{
			"hostname": "my-server",
		})
		pa := createProvisionAutomation("ra-pa5", map[string]string{
			"kickstart.cfg": "{{ index .ConfigMaps \"hostname\" }} {{ index .ConfigMaps \"timezone\" }}",
		})
		p := createProvisionWithRefs(
			"ra-p6", "ra-m6", "ra-bc6", "ra-pa5",
			[]string{"ra-cm-general", "ra-cm-specific"}, nil)
		defer func() {
			Expect(k8sClient.Delete(ctx, p)).To(Succeed())
			Expect(k8sClient.Delete(ctx, pa)).To(Succeed())
			Expect(k8sClient.Delete(ctx, cm2)).To(Succeed())
			Expect(k8sClient.Delete(ctx, cm1)).To(Succeed())
		}()

		result, err := RenderAutomationFile(
			ctx, k8sClient, ns, "ra-p6", "kickstart.cfg")
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal("my-server UTC"))
	})

	It("later secrets override earlier ones", func() {
		s1 := createSecret("ra-s-general", map[string][]byte{
			"password": []byte("general-pw"),
			"token":    []byte("general-token"),
		})
		s2 := createSecret("ra-s-specific", map[string][]byte{
			"password": []byte("specific-pw"),
		})
		pa := createProvisionAutomation("ra-pa6", map[string]string{
			"kickstart.cfg": "{{ index .Secrets \"password\" }} {{ index .Secrets \"token\" }}",
		})
		p := createProvisionWithRefs(
			"ra-p7", "ra-m7", "ra-bc7", "ra-pa6",
			nil, []string{"ra-s-general", "ra-s-specific"})
		defer func() {
			Expect(k8sClient.Delete(ctx, p)).To(Succeed())
			Expect(k8sClient.Delete(ctx, pa)).To(Succeed())
			Expect(k8sClient.Delete(ctx, s2)).To(Succeed())
			Expect(k8sClient.Delete(ctx, s1)).To(Succeed())
		}()

		result, err := RenderAutomationFile(
			ctx, k8sClient, ns, "ra-p7", "kickstart.cfg")
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal("specific-pw general-token"))
	})
})
