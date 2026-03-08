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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	isobootgithubiov1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
)

var _ = Describe("BootConfig Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-bootconfig"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}
		bootconfig := &isobootgithubiov1alpha1.BootConfig{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind BootConfig")
			err := k8sClient.Get(ctx, typeNamespacedName, bootconfig)
			if err != nil && errors.IsNotFound(err) {
				resource := &isobootgithubiov1alpha1.BootConfig{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: isobootgithubiov1alpha1.BootConfigSpec{
						KernelRef: ptr.To("test-kernel"),
						InitrdRef: ptr.To("test-initrd"),
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &isobootgithubiov1alpha1.BootConfig{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance BootConfig")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &BootConfigReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})

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
				KernelRef: ptr.To("my-kernel"),
				InitrdRef: ptr.To("my-initrd"),
			}),
			Entry("mode A: with firmware", "valid-mode-a-fw", isobootgithubiov1alpha1.BootConfigSpec{
				KernelRef:   ptr.To("my-kernel"),
				InitrdRef:   ptr.To("my-initrd"),
				FirmwareRef: ptr.To("my-firmware"),
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
				KernelRef: ptr.To("my-kernel"),
				InitrdRef: ptr.To("my-initrd"),
				ISO: &isobootgithubiov1alpha1.BootConfigISOSpec{
					ArtifactRef: "my-iso",
					KernelPath:  "casper/vmlinuz",
					InitrdPath:  "casper/initrd",
				},
			}),
			Entry("kernelRef without initrdRef", "kernel-only", isobootgithubiov1alpha1.BootConfigSpec{
				KernelRef: ptr.To("my-kernel"),
			}),
			Entry("initrdRef without kernelRef", "initrd-only", isobootgithubiov1alpha1.BootConfigSpec{
				InitrdRef: ptr.To("my-initrd"),
			}),
			Entry("firmwareRef with iso mode", "fw-with-iso", isobootgithubiov1alpha1.BootConfigSpec{
				FirmwareRef: ptr.To("my-firmware"),
				ISO: &isobootgithubiov1alpha1.BootConfigISOSpec{
					ArtifactRef: "my-iso",
					KernelPath:  "casper/vmlinuz",
					InitrdPath:  "casper/initrd",
				},
			}),
		)
	})
})
