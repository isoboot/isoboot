//go:build !e2e

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

package e2e

import (
	"context"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/isoboot/isoboot/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// Validation rules tested:
//
// URLSource:
//   1. binary URL is required (non-empty)
//   2. shasum URL is required (non-empty)
//   3. binary URL must use https
//   4. shasum URL must use https
//   5. binary and shasum URLs must be on the same server
//
// ISOSource:
//   6. path.kernel is required (non-empty)
//   7. path.initrd is required (non-empty)
//
// PathSource (path traversal prevention):
//   10. path.kernel must be relative (cannot start with /)
//   11. path.kernel cannot contain .. components
//   12. path.initrd must be relative (cannot start with /)
//   13. path.initrd cannot contain .. components
//   14. path.firmware must be relative (cannot start with /)
//   15. path.firmware cannot contain .. components
//
// BootSourceSpec:
//   16. must specify either (kernel AND initrd) OR iso
//   17. cannot specify both (kernel OR initrd) AND iso

var (
	testEnv   *envtest.Environment
	k8sClient client.Client
	ctx       context.Context
)

func TestBootSourceValidation(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "BootSource Validation Suite")
}

var _ = BeforeSuite(func() {
	ctx = context.Background()

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{filepath.Join("..", "..", "config", "crd", "bases")},
	}

	cfg, err := testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	err = v1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})

// testCase defines a validation test case
type testCase struct {
	name     string
	spec     v1alpha1.BootSourceSpec
	valid    bool
	errorMsg string // expected error message substring (only for invalid cases)
}

// Base building blocks for specs
func urlSource(binary, shasum string) v1alpha1.URLSource {
	return v1alpha1.URLSource{Binary: binary, Shasum: shasum}
}

func httpsURL(path string) string {
	return "https://example.com/" + path
}

func kernelSource() *v1alpha1.KernelSource {
	return &v1alpha1.KernelSource{
		URL: urlSource(httpsURL("vmlinuz"), httpsURL("vmlinuz.sha256")),
	}
}

func initrdSource() *v1alpha1.InitrdSource {
	return &v1alpha1.InitrdSource{
		URL: urlSource(httpsURL("initrd.img"), httpsURL("initrd.img.sha256")),
	}
}

func firmwareSource() *v1alpha1.FirmwareSource {
	return &v1alpha1.FirmwareSource{
		URL: urlSource(httpsURL("firmware.tar"), httpsURL("firmware.tar.sha256")),
	}
}

func isoSource() *v1alpha1.ISOSource {
	return &v1alpha1.ISOSource{
		URL:  urlSource(httpsURL("boot.iso"), httpsURL("boot.iso.sha256")),
		Path: v1alpha1.PathSource{Kernel: "casper/vmlinuz", Initrd: "casper/initrd.gz"},
	}
}

var _ = Describe("BootSource Validation", func() {

	tests := []testCase{
		// === VALID CASES ===
		{
			name:  "valid: kernel + initrd",
			spec:  v1alpha1.BootSourceSpec{Kernel: kernelSource(), Initrd: initrdSource()},
			valid: true,
		},
		{
			name:  "valid: kernel + initrd + firmware",
			spec:  v1alpha1.BootSourceSpec{Kernel: kernelSource(), Initrd: initrdSource(), Firmware: firmwareSource()},
			valid: true,
		},
		{
			name:  "valid: iso only",
			spec:  v1alpha1.BootSourceSpec{ISO: isoSource()},
			valid: true,
		},
		{
			name:  "valid: iso + firmware",
			spec:  v1alpha1.BootSourceSpec{ISO: isoSource(), Firmware: firmwareSource()},
			valid: true,
		},

		// === URLSource: binary URL required [Rule 1] ===
		{
			name: "invalid: empty kernel binary URL",
			spec: v1alpha1.BootSourceSpec{
				Kernel: &v1alpha1.KernelSource{URL: urlSource("", httpsURL("vmlinuz.sha256"))},
				Initrd: initrdSource(),
			},
			valid:    false,
			errorMsg: "binary URL is required",
		},
		{
			name: "invalid: empty initrd binary URL",
			spec: v1alpha1.BootSourceSpec{
				Kernel: kernelSource(),
				Initrd: &v1alpha1.InitrdSource{URL: urlSource("", httpsURL("initrd.sha256"))},
			},
			valid:    false,
			errorMsg: "binary URL is required",
		},

		// === URLSource: shasum URL required [Rule 2] ===
		{
			name: "invalid: empty kernel shasum URL",
			spec: v1alpha1.BootSourceSpec{
				Kernel: &v1alpha1.KernelSource{URL: urlSource(httpsURL("vmlinuz"), "")},
				Initrd: initrdSource(),
			},
			valid:    false,
			errorMsg: "shasum URL is required",
		},

		// === URLSource: binary must be https [Rule 3] ===
		{
			name: "invalid: http binary URL",
			spec: v1alpha1.BootSourceSpec{
				Kernel: &v1alpha1.KernelSource{URL: urlSource("http://example.com/vmlinuz", httpsURL("vmlinuz.sha256"))},
				Initrd: initrdSource(),
			},
			valid:    false,
			errorMsg: "binary URL must use https",
		},

		// === URLSource: shasum must be https [Rule 4] ===
		{
			name: "invalid: http shasum URL",
			spec: v1alpha1.BootSourceSpec{
				Kernel: &v1alpha1.KernelSource{URL: urlSource(httpsURL("vmlinuz"), "http://example.com/vmlinuz.sha256")},
				Initrd: initrdSource(),
			},
			valid:    false,
			errorMsg: "shasum URL must use https",
		},

		// === URLSource: same server [Rule 5] ===
		{
			name: "invalid: binary and shasum on different servers",
			spec: v1alpha1.BootSourceSpec{
				Kernel: &v1alpha1.KernelSource{
					URL: urlSource(
						"https://server1.example.com/vmlinuz",
						"https://server2.example.com/vmlinuz.sha256"),
				},
				Initrd: initrdSource(),
			},
			valid:    false,
			errorMsg: "binary and shasum URLs must be on the same server",
		},

		// === ISOSource: path.kernel required [Rule 6] ===
		{
			name: "invalid: iso with empty path.kernel",
			spec: v1alpha1.BootSourceSpec{
				ISO: &v1alpha1.ISOSource{
					URL:  urlSource(httpsURL("boot.iso"), httpsURL("boot.iso.sha256")),
					Path: v1alpha1.PathSource{Kernel: "", Initrd: "casper/initrd.gz"},
				},
			},
			valid:    false,
			errorMsg: "iso requires path.kernel to be specified",
		},

		// === ISOSource: path.initrd required [Rule 7] ===
		{
			name: "invalid: iso with empty path.initrd",
			spec: v1alpha1.BootSourceSpec{
				ISO: &v1alpha1.ISOSource{
					URL:  urlSource(httpsURL("boot.iso"), httpsURL("boot.iso.sha256")),
					Path: v1alpha1.PathSource{Kernel: "casper/vmlinuz", Initrd: ""},
				},
			},
			valid:    false,
			errorMsg: "iso requires path.initrd to be specified",
		},

		// === PathSource: kernel absolute path [Rule 10] ===
		{
			name: "invalid: iso kernel path starts with /",
			spec: v1alpha1.BootSourceSpec{
				ISO: &v1alpha1.ISOSource{
					URL:  urlSource(httpsURL("boot.iso"), httpsURL("boot.iso.sha256")),
					Path: v1alpha1.PathSource{Kernel: "/etc/passwd", Initrd: "casper/initrd.gz"},
				},
			},
			valid:    false,
			errorMsg: "kernel path must be relative",
		},

		// === PathSource: kernel path traversal [Rule 11] ===
		{
			name: "invalid: iso kernel path with ../ prefix",
			spec: v1alpha1.BootSourceSpec{
				ISO: &v1alpha1.ISOSource{
					URL:  urlSource(httpsURL("boot.iso"), httpsURL("boot.iso.sha256")),
					Path: v1alpha1.PathSource{Kernel: "../etc/passwd", Initrd: "casper/initrd.gz"},
				},
			},
			valid:    false,
			errorMsg: "kernel path cannot contain .. components",
		},
		{
			name: "invalid: iso kernel path with /../ in middle",
			spec: v1alpha1.BootSourceSpec{
				ISO: &v1alpha1.ISOSource{
					URL:  urlSource(httpsURL("boot.iso"), httpsURL("boot.iso.sha256")),
					Path: v1alpha1.PathSource{Kernel: "casper/../../../etc/passwd", Initrd: "casper/initrd.gz"},
				},
			},
			valid:    false,
			errorMsg: "kernel path cannot contain .. components",
		},
		{
			name: "invalid: iso kernel path with /.. suffix",
			spec: v1alpha1.BootSourceSpec{
				ISO: &v1alpha1.ISOSource{
					URL:  urlSource(httpsURL("boot.iso"), httpsURL("boot.iso.sha256")),
					Path: v1alpha1.PathSource{Kernel: "casper/vmlinuz/..", Initrd: "casper/initrd.gz"},
				},
			},
			valid:    false,
			errorMsg: "kernel path cannot contain .. components",
		},
		{
			name: "invalid: iso kernel path is just ..",
			spec: v1alpha1.BootSourceSpec{
				ISO: &v1alpha1.ISOSource{
					URL:  urlSource(httpsURL("boot.iso"), httpsURL("boot.iso.sha256")),
					Path: v1alpha1.PathSource{Kernel: "..", Initrd: "casper/initrd.gz"},
				},
			},
			valid:    false,
			errorMsg: "kernel path cannot contain .. components",
		},

		// === PathSource: initrd absolute path [Rule 12] ===
		{
			name: "invalid: iso initrd path starts with /",
			spec: v1alpha1.BootSourceSpec{
				ISO: &v1alpha1.ISOSource{
					URL:  urlSource(httpsURL("boot.iso"), httpsURL("boot.iso.sha256")),
					Path: v1alpha1.PathSource{Kernel: "casper/vmlinuz", Initrd: "/etc/shadow"},
				},
			},
			valid:    false,
			errorMsg: "initrd path must be relative",
		},

		// === PathSource: initrd path traversal [Rule 13] ===
		{
			name: "invalid: iso initrd path with ../ prefix",
			spec: v1alpha1.BootSourceSpec{
				ISO: &v1alpha1.ISOSource{
					URL:  urlSource(httpsURL("boot.iso"), httpsURL("boot.iso.sha256")),
					Path: v1alpha1.PathSource{Kernel: "casper/vmlinuz", Initrd: "../../../etc/shadow"},
				},
			},
			valid:    false,
			errorMsg: "initrd path cannot contain .. components",
		},
		{
			name: "invalid: iso initrd path with /../ in middle",
			spec: v1alpha1.BootSourceSpec{
				ISO: &v1alpha1.ISOSource{
					URL:  urlSource(httpsURL("boot.iso"), httpsURL("boot.iso.sha256")),
					Path: v1alpha1.PathSource{Kernel: "casper/vmlinuz", Initrd: "casper/../../../etc/shadow"},
				},
			},
			valid:    false,
			errorMsg: "initrd path cannot contain .. components",
		},
		{
			name: "invalid: iso initrd path with /.. suffix",
			spec: v1alpha1.BootSourceSpec{
				ISO: &v1alpha1.ISOSource{
					URL:  urlSource(httpsURL("boot.iso"), httpsURL("boot.iso.sha256")),
					Path: v1alpha1.PathSource{Kernel: "casper/vmlinuz", Initrd: "casper/initrd.gz/.."},
				},
			},
			valid:    false,
			errorMsg: "initrd path cannot contain .. components",
		},
		{
			name: "invalid: iso initrd path is just ..",
			spec: v1alpha1.BootSourceSpec{
				ISO: &v1alpha1.ISOSource{
					URL:  urlSource(httpsURL("boot.iso"), httpsURL("boot.iso.sha256")),
					Path: v1alpha1.PathSource{Kernel: "casper/vmlinuz", Initrd: ".."},
				},
			},
			valid:    false,
			errorMsg: "initrd path cannot contain .. components",
		},

		// === PathSource: firmware absolute path [Rule 14] ===
		{
			name: "invalid: iso firmware path starts with /",
			spec: v1alpha1.BootSourceSpec{
				ISO: &v1alpha1.ISOSource{
					URL:  urlSource(httpsURL("boot.iso"), httpsURL("boot.iso.sha256")),
					Path: v1alpha1.PathSource{Kernel: "casper/vmlinuz", Initrd: "casper/initrd.gz", Firmware: "/var/run/secrets/token"},
				},
			},
			valid:    false,
			errorMsg: "firmware path must be relative",
		},

		// === PathSource: firmware path traversal [Rule 15] ===
		{
			name: "invalid: iso firmware path with ../ prefix",
			spec: v1alpha1.BootSourceSpec{
				ISO: &v1alpha1.ISOSource{
					URL:  urlSource(httpsURL("boot.iso"), httpsURL("boot.iso.sha256")),
					Path: v1alpha1.PathSource{Kernel: "casper/vmlinuz", Initrd: "casper/initrd.gz", Firmware: "../../../var/run/secrets/token"},
				},
			},
			valid:    false,
			errorMsg: "firmware path cannot contain .. components",
		},
		{
			name: "invalid: iso firmware path with /../ in middle",
			spec: v1alpha1.BootSourceSpec{
				ISO: &v1alpha1.ISOSource{
					URL:  urlSource(httpsURL("boot.iso"), httpsURL("boot.iso.sha256")),
					Path: v1alpha1.PathSource{Kernel: "casper/vmlinuz", Initrd: "casper/initrd.gz", Firmware: "firmware/../../../var/run/secrets/token"},
				},
			},
			valid:    false,
			errorMsg: "firmware path cannot contain .. components",
		},
		{
			name: "invalid: iso firmware path with /.. suffix",
			spec: v1alpha1.BootSourceSpec{
				ISO: &v1alpha1.ISOSource{
					URL:  urlSource(httpsURL("boot.iso"), httpsURL("boot.iso.sha256")),
					Path: v1alpha1.PathSource{Kernel: "casper/vmlinuz", Initrd: "casper/initrd.gz", Firmware: "firmware.cpio.gz/.."},
				},
			},
			valid:    false,
			errorMsg: "firmware path cannot contain .. components",
		},
		{
			name: "invalid: iso firmware path is just ..",
			spec: v1alpha1.BootSourceSpec{
				ISO: &v1alpha1.ISOSource{
					URL:  urlSource(httpsURL("boot.iso"), httpsURL("boot.iso.sha256")),
					Path: v1alpha1.PathSource{Kernel: "casper/vmlinuz", Initrd: "casper/initrd.gz", Firmware: ".."},
				},
			},
			valid:    false,
			errorMsg: "firmware path cannot contain .. components",
		},

		// === BootSourceSpec: (kernel && initrd) || iso [Rule 16] ===
		{
			name:     "invalid: empty spec",
			spec:     v1alpha1.BootSourceSpec{},
			valid:    false,
			errorMsg: "must specify either (kernel and initrd) or iso",
		},
		{
			name:     "invalid: kernel only (no initrd)",
			spec:     v1alpha1.BootSourceSpec{Kernel: kernelSource()},
			valid:    false,
			errorMsg: "must specify either (kernel and initrd) or iso",
		},
		{
			name:     "invalid: initrd only (no kernel)",
			spec:     v1alpha1.BootSourceSpec{Initrd: initrdSource()},
			valid:    false,
			errorMsg: "must specify either (kernel and initrd) or iso",
		},
		{
			name:     "invalid: firmware only",
			spec:     v1alpha1.BootSourceSpec{Firmware: firmwareSource()},
			valid:    false,
			errorMsg: "must specify either (kernel and initrd) or iso",
		},

		// === BootSourceSpec: XOR constraint [Rule 17] ===
		{
			name:     "invalid: kernel + initrd + iso",
			spec:     v1alpha1.BootSourceSpec{Kernel: kernelSource(), Initrd: initrdSource(), ISO: isoSource()},
			valid:    false,
			errorMsg: "cannot specify both (kernel or initrd) and iso",
		},
		{
			name:     "invalid: kernel + iso (no initrd)",
			spec:     v1alpha1.BootSourceSpec{Kernel: kernelSource(), ISO: isoSource()},
			valid:    false,
			errorMsg: "cannot specify both (kernel or initrd) and iso",
		},
		{
			name:     "invalid: initrd + iso (no kernel)",
			spec:     v1alpha1.BootSourceSpec{Initrd: initrdSource(), ISO: isoSource()},
			valid:    false,
			errorMsg: "cannot specify both (kernel or initrd) and iso",
		},
	}

	for _, tc := range tests {
		It(tc.name, func() {
			bs := &v1alpha1.BootSource{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "test-",
					Namespace:    "default",
				},
				Spec: tc.spec,
			}

			err := k8sClient.Create(ctx, bs)

			if tc.valid {
				Expect(err).NotTo(HaveOccurred(), "expected valid spec to be accepted")
				Expect(k8sClient.Delete(ctx, bs)).To(Succeed())
			} else {
				Expect(err).To(HaveOccurred(), "expected invalid spec to be rejected")
				Expect(err.Error()).To(ContainSubstring(tc.errorMsg),
					"expected error to contain %q, got: %v", tc.errorMsg, err)
			}
		})
	}
})
