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

const (
	validSHA256 = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	validSHA512 = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
)

var _ = Describe("BootArtifact Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}
		bootartifact := &isobootgithubiov1alpha1.BootArtifact{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind BootArtifact")
			err := k8sClient.Get(ctx, typeNamespacedName, bootartifact)
			if err != nil && errors.IsNotFound(err) {
				resource := &isobootgithubiov1alpha1.BootArtifact{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: isobootgithubiov1alpha1.BootArtifactSpec{
						URL:    "https://example.com/vmlinuz",
						SHA256: ptr.To(validSHA256),
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &isobootgithubiov1alpha1.BootArtifact{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance BootArtifact")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &BootArtifactReconciler{
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

		newArtifact := func(name string, spec isobootgithubiov1alpha1.BootArtifactSpec) *isobootgithubiov1alpha1.BootArtifact {
			return &isobootgithubiov1alpha1.BootArtifact{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
				Spec:       spec,
			}
		}

		DescribeTable("should accept valid specs",
			func(name string, spec isobootgithubiov1alpha1.BootArtifactSpec) {
				resource := newArtifact(name, spec)
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
			},
			Entry("sha256 only", "valid-sha256", isobootgithubiov1alpha1.BootArtifactSpec{URL: "https://example.com/vmlinuz", SHA256: ptr.To(validSHA256)}),
			Entry("sha512 only", "valid-sha512", isobootgithubiov1alpha1.BootArtifactSpec{URL: "https://example.com/vmlinuz", SHA512: ptr.To(validSHA512)}),
		)

		DescribeTable("should reject invalid specs",
			func(name string, spec isobootgithubiov1alpha1.BootArtifactSpec) {
				resource := newArtifact(name, spec)
				Expect(k8sClient.Create(ctx, resource)).NotTo(Succeed())
			},
			Entry("both hashes set", "both", isobootgithubiov1alpha1.BootArtifactSpec{URL: "https://example.com/f", SHA256: ptr.To(validSHA256), SHA512: ptr.To(validSHA512)}),
			Entry("no hash set", "none", isobootgithubiov1alpha1.BootArtifactSpec{URL: "https://example.com/f"}),
			Entry("short sha256", "short256", isobootgithubiov1alpha1.BootArtifactSpec{URL: "https://example.com/f", SHA256: ptr.To("abcdef")}),
			Entry("short sha512", "short512", isobootgithubiov1alpha1.BootArtifactSpec{URL: "https://example.com/f", SHA512: ptr.To("abcdef")}),
			Entry("non-hex sha256", "nonhex256", isobootgithubiov1alpha1.BootArtifactSpec{URL: "https://example.com/f", SHA256: ptr.To("zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz")}),
			Entry("non-hex sha512", "nonhex512", isobootgithubiov1alpha1.BootArtifactSpec{URL: "https://example.com/f", SHA512: ptr.To("zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz")}),
			Entry("http url", "http", isobootgithubiov1alpha1.BootArtifactSpec{URL: "http://example.com/f", SHA256: ptr.To(validSHA256)}),
			Entry("empty url", "empty", isobootgithubiov1alpha1.BootArtifactSpec{URL: "", SHA256: ptr.To(validSHA256)}),
		)
	})
})
