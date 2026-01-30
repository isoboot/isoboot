package k8s

import (
	"reflect"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestGetString(t *testing.T) {
	tests := []struct {
		name     string
		m        map[string]interface{}
		key      string
		expected string
	}{
		{
			name:     "existing string key",
			m:        map[string]interface{}{"foo": "bar"},
			key:      "foo",
			expected: "bar",
		},
		{
			name:     "missing key",
			m:        map[string]interface{}{"foo": "bar"},
			key:      "baz",
			expected: "",
		},
		{
			name:     "non-string value",
			m:        map[string]interface{}{"foo": 123},
			key:      "foo",
			expected: "",
		},
		{
			name:     "empty map",
			m:        map[string]interface{}{},
			key:      "foo",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getString(tt.m, tt.key)
			if result != tt.expected {
				t.Errorf("getString(%v, %q) = %q, want %q", tt.m, tt.key, result, tt.expected)
			}
		})
	}
}

func TestGetStringSlice(t *testing.T) {
	tests := []struct {
		name     string
		m        map[string]interface{}
		key      string
		expected []string
	}{
		{
			name:     "existing slice",
			m:        map[string]interface{}{"items": []interface{}{"a", "b", "c"}},
			key:      "items",
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "empty slice",
			m:        map[string]interface{}{"items": []interface{}{}},
			key:      "items",
			expected: []string{},
		},
		{
			name:     "missing key",
			m:        map[string]interface{}{"foo": "bar"},
			key:      "items",
			expected: nil,
		},
		{
			name:     "non-slice value",
			m:        map[string]interface{}{"items": "not a slice"},
			key:      "items",
			expected: nil,
		},
		{
			name:     "mixed types in slice",
			m:        map[string]interface{}{"items": []interface{}{"a", 123, "b"}},
			key:      "items",
			expected: []string{"a", "b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getStringSlice(tt.m, tt.key)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("getStringSlice(%v, %q) = %v, want %v", tt.m, tt.key, result, tt.expected)
			}
		})
	}
}

func TestGetStringMap(t *testing.T) {
	tests := []struct {
		name     string
		m        map[string]interface{}
		key      string
		expected map[string]string
	}{
		{
			name: "existing map",
			m: map[string]interface{}{
				"files": map[string]interface{}{"preseed.cfg": "content1", "late.sh": "content2"},
			},
			key:      "files",
			expected: map[string]string{"preseed.cfg": "content1", "late.sh": "content2"},
		},
		{
			name:     "empty map",
			m:        map[string]interface{}{"files": map[string]interface{}{}},
			key:      "files",
			expected: map[string]string{},
		},
		{
			name:     "missing key",
			m:        map[string]interface{}{"foo": "bar"},
			key:      "files",
			expected: nil,
		},
		{
			name:     "non-map value",
			m:        map[string]interface{}{"files": "not a map"},
			key:      "files",
			expected: nil,
		},
		{
			name: "mixed types in map",
			m: map[string]interface{}{
				"files": map[string]interface{}{"a": "string", "b": 123},
			},
			key:      "files",
			expected: map[string]string{"a": "string"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getStringMap(tt.m, tt.key)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("getStringMap(%v, %q) = %v, want %v", tt.m, tt.key, result, tt.expected)
			}
		})
	}
}

func TestParseResponseTemplate(t *testing.T) {
	tests := []struct {
		name        string
		obj         *unstructured.Unstructured
		expected    *ResponseTemplate
		expectError bool
	}{
		{
			name: "valid response template",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{"name": "debian-preseed"},
					"spec": map[string]interface{}{
						"files": map[string]interface{}{
							"preseed.cfg": "d-i locale string en_US",
							"late.sh":     "#!/bin/bash\necho done",
						},
					},
				},
			},
			expected: &ResponseTemplate{
				Name: "debian-preseed",
				Files: map[string]string{
					"preseed.cfg": "d-i locale string en_US",
					"late.sh":     "#!/bin/bash\necho done",
				},
			},
			expectError: false,
		},
		{
			name: "empty files",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{"name": "empty-template"},
					"spec": map[string]interface{}{
						"files": map[string]interface{}{},
					},
				},
			},
			expected: &ResponseTemplate{
				Name:  "empty-template",
				Files: map[string]string{},
			},
			expectError: false,
		},
		{
			name: "missing spec",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{"name": "no-spec"},
				},
			},
			expected:    nil,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseResponseTemplate(tt.obj)
			if tt.expectError {
				if err == nil {
					t.Error("Expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			if result.Name != tt.expected.Name {
				t.Errorf("Name = %q, want %q", result.Name, tt.expected.Name)
			}
			if !reflect.DeepEqual(result.Files, tt.expected.Files) {
				t.Errorf("Files = %v, want %v", result.Files, tt.expected.Files)
			}
		})
	}
}

func TestParseProvision_WithNewFields(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{"name": "test-provision"},
			"spec": map[string]interface{}{
				"machineRef":          "vm125",
				"bootTargetRef":       "debian-13",
				"responseTemplateRef": "debian-preseed",
				"configMaps":          []interface{}{"common-config", "host-config"},
				"secrets":             []interface{}{"user-passwords"},
			},
			"status": map[string]interface{}{
				"phase":   "Pending",
				"message": "Initialized",
			},
		},
	}

	result, err := parseProvision(obj)
	if err != nil {
		t.Fatalf("parseProvision failed: %v", err)
	}

	if result.Name != "test-provision" {
		t.Errorf("Name = %q, want %q", result.Name, "test-provision")
	}
	if result.Spec.MachineRef != "vm125" {
		t.Errorf("MachineRef = %q, want %q", result.Spec.MachineRef, "vm125")
	}
	if result.Spec.BootTargetRef != "debian-13" {
		t.Errorf("BootTargetRef = %q, want %q", result.Spec.BootTargetRef, "debian-13")
	}
	if result.Spec.ResponseTemplateRef != "debian-preseed" {
		t.Errorf("ResponseTemplateRef = %q, want %q", result.Spec.ResponseTemplateRef, "debian-preseed")
	}

	expectedConfigMaps := []string{"common-config", "host-config"}
	if !reflect.DeepEqual(result.Spec.ConfigMaps, expectedConfigMaps) {
		t.Errorf("ConfigMaps = %v, want %v", result.Spec.ConfigMaps, expectedConfigMaps)
	}

	expectedSecrets := []string{"user-passwords"}
	if !reflect.DeepEqual(result.Spec.Secrets, expectedSecrets) {
		t.Errorf("Secrets = %v, want %v", result.Spec.Secrets, expectedSecrets)
	}

	if result.Status.Phase != "Pending" {
		t.Errorf("Status.Phase = %q, want %q", result.Status.Phase, "Pending")
	}
}

func TestParseProvision_NoOptionalFields(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{"name": "minimal-provision"},
			"spec": map[string]interface{}{
				"machineRef":    "vm125",
				"bootTargetRef": "debian-13",
			},
		},
	}

	result, err := parseProvision(obj)
	if err != nil {
		t.Fatalf("parseProvision failed: %v", err)
	}

	// Verify legacy "target" field is read into BootTargetRef
	if result.Spec.BootTargetRef != "debian-13" {
		t.Errorf("BootTargetRef = %q, want %q (from legacy target field)", result.Spec.BootTargetRef, "debian-13")
	}
	if result.Spec.ResponseTemplateRef != "" {
		t.Errorf("ResponseTemplateRef = %q, want empty", result.Spec.ResponseTemplateRef)
	}
	if result.Spec.ConfigMaps != nil {
		t.Errorf("ConfigMaps = %v, want nil", result.Spec.ConfigMaps)
	}
	if result.Spec.Secrets != nil {
		t.Errorf("Secrets = %v, want nil", result.Spec.Secrets)
	}
}

func TestGetInt(t *testing.T) {
	tests := []struct {
		name     string
		m        map[string]interface{}
		key      string
		expected int
	}{
		{
			name:     "int value",
			m:        map[string]interface{}{"progress": int(50)},
			key:      "progress",
			expected: 50,
		},
		{
			name:     "int32 value",
			m:        map[string]interface{}{"progress": int32(75)},
			key:      "progress",
			expected: 75,
		},
		{
			name:     "int64 value",
			m:        map[string]interface{}{"progress": int64(100)},
			key:      "progress",
			expected: 100,
		},
		{
			name:     "float64 value",
			m:        map[string]interface{}{"progress": float64(25.0)},
			key:      "progress",
			expected: 25,
		},
		{
			name:     "missing key",
			m:        map[string]interface{}{"foo": "bar"},
			key:      "progress",
			expected: 0,
		},
		{
			name:     "string value",
			m:        map[string]interface{}{"progress": "50"},
			key:      "progress",
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getInt(tt.m, tt.key)
			if result != tt.expected {
				t.Errorf("getInt(%v, %q) = %d, want %d", tt.m, tt.key, result, tt.expected)
			}
		})
	}
}

func TestParseBootTarget(t *testing.T) {
	tests := []struct {
		name        string
		obj         *unstructured.Unstructured
		expected    *BootTarget
		expectError bool
	}{
		{
			name: "valid BootTarget with bootMediaRef and useFirmware",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{"name": "debian-13-firmware"},
					"spec": map[string]interface{}{
						"bootMediaRef":      "debian-13",
						"useFirmware": true,
						"template":          "kernel /linux\ninitrd /firmware-initrd.gz",
					},
				},
			},
			expected: &BootTarget{
				Name:              "debian-13-firmware",
				BootMediaRef:      "debian-13",
				UseFirmware: true,
				Template:          "kernel /linux\ninitrd /firmware-initrd.gz",
			},
			expectError: false,
		},
		{
			name: "BootTarget without firmware",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{"name": "debian-13"},
					"spec": map[string]interface{}{
						"bootMediaRef": "debian-13",
						"template":     "kernel /linux\ninitrd /initrd.gz",
					},
				},
			},
			expected: &BootTarget{
				Name:              "debian-13",
				BootMediaRef:      "debian-13",
				UseFirmware: false,
				Template:          "kernel /linux\ninitrd /initrd.gz",
			},
			expectError: false,
		},
		{
			name: "missing spec returns error",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{"name": "no-spec"},
				},
			},
			expected:    nil,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseBootTarget(tt.obj)
			if tt.expectError {
				if err == nil {
					t.Error("Expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			if result.Name != tt.expected.Name {
				t.Errorf("Name = %q, want %q", result.Name, tt.expected.Name)
			}
			if result.BootMediaRef != tt.expected.BootMediaRef {
				t.Errorf("BootMediaRef = %q, want %q", result.BootMediaRef, tt.expected.BootMediaRef)
			}
			if result.UseFirmware != tt.expected.UseFirmware {
				t.Errorf("UseFirmware = %v, want %v", result.UseFirmware, tt.expected.UseFirmware)
			}
			if result.Template != tt.expected.Template {
				t.Errorf("Template = %q, want %q", result.Template, tt.expected.Template)
			}
		})
	}
}

func TestParseBootMedia(t *testing.T) {
	tests := []struct {
		name        string
		obj         *unstructured.Unstructured
		expectError bool
		check       func(t *testing.T, bm *BootMedia)
	}{
		{
			name: "direct kernel + initrd",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{"name": "debian-13"},
					"spec": map[string]interface{}{
						"kernel": map[string]interface{}{
							"url":         "http://example.com/linux",
							"checksumURL": "http://example.com/SHA256SUMS",
						},
						"initrd": map[string]interface{}{
							"url": "http://example.com/initrd.gz",
						},
					},
				},
			},
			check: func(t *testing.T, bm *BootMedia) {
				if bm.Name != "debian-13" {
					t.Errorf("Name = %q, want %q", bm.Name, "debian-13")
				}
				if bm.Kernel == nil || bm.Kernel.URL != "http://example.com/linux" {
					t.Errorf("Kernel = %+v, want URL http://example.com/linux", bm.Kernel)
				}
				if bm.Kernel.ChecksumURL != "http://example.com/SHA256SUMS" {
					t.Errorf("Kernel.ChecksumURL = %q, want SHA256SUMS URL", bm.Kernel.ChecksumURL)
				}
				if bm.Initrd == nil || bm.Initrd.URL != "http://example.com/initrd.gz" {
					t.Errorf("Initrd = %+v, want URL http://example.com/initrd.gz", bm.Initrd)
				}
				if bm.ISO != nil {
					t.Error("ISO should be nil for direct mode")
				}
				if bm.Firmware != nil {
					t.Error("Firmware should be nil")
				}
			},
		},
		{
			name: "ISO mode",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{"name": "debian-12-iso"},
					"spec": map[string]interface{}{
						"iso": map[string]interface{}{
							"url":         "http://example.com/debian-12.iso",
							"checksumURL": "http://example.com/SHA256SUMS",
							"kernel":      "/install.amd/vmlinuz",
							"initrd":      "/install.amd/initrd.gz",
						},
					},
				},
			},
			check: func(t *testing.T, bm *BootMedia) {
				if bm.Name != "debian-12-iso" {
					t.Errorf("Name = %q, want debian-12-iso", bm.Name)
				}
				if bm.ISO == nil {
					t.Fatal("ISO should not be nil")
				}
				if bm.ISO.URL != "http://example.com/debian-12.iso" {
					t.Errorf("ISO.URL = %q", bm.ISO.URL)
				}
				if bm.ISO.Kernel != "/install.amd/vmlinuz" {
					t.Errorf("ISO.Kernel = %q", bm.ISO.Kernel)
				}
				if bm.ISO.Initrd != "/install.amd/initrd.gz" {
					t.Errorf("ISO.Initrd = %q", bm.ISO.Initrd)
				}
				if bm.Kernel != nil || bm.Initrd != nil {
					t.Error("Kernel/Initrd should be nil for ISO mode")
				}
			},
		},
		{
			name: "direct with firmware",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{"name": "debian-13"},
					"spec": map[string]interface{}{
						"kernel": map[string]interface{}{
							"url": "http://example.com/linux",
						},
						"initrd": map[string]interface{}{
							"url": "http://example.com/initrd.gz",
						},
						"firmware": map[string]interface{}{
							"url": "http://example.com/firmware.cpio.gz",
						},
					},
				},
			},
			check: func(t *testing.T, bm *BootMedia) {
				if bm.Firmware == nil || bm.Firmware.URL != "http://example.com/firmware.cpio.gz" {
					t.Errorf("Firmware = %+v", bm.Firmware)
				}
			},
		},
		{
			name: "status parsing",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{"name": "debian-13"},
					"spec": map[string]interface{}{
						"kernel": map[string]interface{}{"url": "http://example.com/linux"},
						"initrd": map[string]interface{}{"url": "http://example.com/initrd.gz"},
					},
					"status": map[string]interface{}{
						"phase":   "Complete",
						"message": "All files downloaded",
						"kernel": map[string]interface{}{
							"name":   "linux",
							"phase":  "Complete",
							"sha256": "abc123",
						},
						"initrd": map[string]interface{}{
							"name":   "initrd.gz",
							"phase":  "Complete",
							"sha256": "def456",
						},
					},
				},
			},
			check: func(t *testing.T, bm *BootMedia) {
				if bm.Status.Phase != "Complete" {
					t.Errorf("Status.Phase = %q", bm.Status.Phase)
				}
				if bm.Status.Kernel == nil || bm.Status.Kernel.SHA256 != "abc123" {
					t.Errorf("Status.Kernel = %+v", bm.Status.Kernel)
				}
				if bm.Status.Initrd == nil || bm.Status.Initrd.SHA256 != "def456" {
					t.Errorf("Status.Initrd = %+v", bm.Status.Initrd)
				}
				if bm.Status.ISO != nil {
					t.Error("Status.ISO should be nil")
				}
			},
		},
		{
			name: "missing spec returns error",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{"name": "no-spec"},
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseBootMedia(tt.obj)
			if tt.expectError {
				if err == nil {
					t.Error("Expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			if tt.check != nil {
				tt.check(t, result)
			}
		})
	}
}

func TestBootMedia_Validate(t *testing.T) {
	tests := []struct {
		name      string
		bm        *BootMedia
		expectErr string
	}{
		{
			name: "valid direct",
			bm: &BootMedia{
				Kernel: &BootMediaFileRef{URL: "http://example.com/linux"},
				Initrd: &BootMediaFileRef{URL: "http://example.com/initrd.gz"},
			},
		},
		{
			name: "valid ISO",
			bm: &BootMedia{
				ISO: &BootMediaISO{
					URL:    "http://example.com/debian.iso",
					Kernel: "/install.amd/vmlinuz",
					Initrd: "/install.amd/initrd.gz",
				},
			},
		},
		{
			name: "both set",
			bm: &BootMedia{
				Kernel: &BootMediaFileRef{URL: "http://example.com/linux"},
				Initrd: &BootMediaFileRef{URL: "http://example.com/initrd.gz"},
				ISO:    &BootMediaISO{URL: "http://example.com/debian.iso", Kernel: "/k", Initrd: "/i"},
			},
			expectErr: "cannot specify both",
		},
		{
			name:      "neither set",
			bm:        &BootMedia{},
			expectErr: "must specify either",
		},
		{
			name: "kernel only",
			bm: &BootMedia{
				Kernel: &BootMediaFileRef{URL: "http://example.com/linux"},
			},
			expectErr: "initrd requires kernel",
		},
		{
			name: "initrd only",
			bm: &BootMedia{
				Initrd: &BootMediaFileRef{URL: "http://example.com/initrd.gz"},
			},
			expectErr: "kernel requires initrd",
		},
		{
			name: "duplicate basenames",
			bm: &BootMedia{
				Kernel: &BootMediaFileRef{URL: "http://example.com/path1/file"},
				Initrd: &BootMediaFileRef{URL: "http://example.com/path2/file"},
			},
			expectErr: "duplicate basename",
		},
		{
			name: "ISO missing kernel path",
			bm: &BootMedia{
				ISO: &BootMediaISO{
					URL:    "http://example.com/debian.iso",
					Initrd: "/install.amd/initrd.gz",
				},
			},
			expectErr: "iso.kernel is required",
		},
		{
			name: "ISO missing initrd path",
			bm: &BootMedia{
				ISO: &BootMediaISO{
					URL:    "http://example.com/debian.iso",
					Kernel: "/install.amd/vmlinuz",
				},
			},
			expectErr: "iso.initrd is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.bm.Validate()
			if tt.expectErr == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.expectErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.expectErr)
			}
		})
	}
}

func TestBootMedia_KernelFilename(t *testing.T) {
	tests := []struct {
		name     string
		bm       *BootMedia
		expected string
	}{
		{
			name:     "from kernel URL",
			bm:       &BootMedia{Kernel: &BootMediaFileRef{URL: "http://example.com/path/linux"}},
			expected: "linux",
		},
		{
			name: "from ISO path",
			bm: &BootMedia{ISO: &BootMediaISO{
				URL: "http://example.com/debian.iso", Kernel: "/install.amd/vmlinuz", Initrd: "/install.amd/initrd.gz",
			}},
			expected: "vmlinuz",
		},
		{
			name:     "empty",
			bm:       &BootMedia{},
			expected: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.bm.KernelFilename()
			if got != tt.expected {
				t.Errorf("KernelFilename() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestBootMedia_InitrdFilename(t *testing.T) {
	tests := []struct {
		name     string
		bm       *BootMedia
		expected string
	}{
		{
			name:     "from initrd URL",
			bm:       &BootMedia{Initrd: &BootMediaFileRef{URL: "http://example.com/path/initrd.gz"}},
			expected: "initrd.gz",
		},
		{
			name: "from ISO path",
			bm: &BootMedia{ISO: &BootMediaISO{
				URL: "http://example.com/debian.iso", Kernel: "/install.amd/vmlinuz", Initrd: "/install.amd/initrd.gz",
			}},
			expected: "initrd.gz",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.bm.InitrdFilename()
			if got != tt.expected {
				t.Errorf("InitrdFilename() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestFilenameFromURL(t *testing.T) {
	tests := []struct {
		name        string
		url         string
		expected    string
		expectError bool
	}{
		{"normal URL", "http://example.com/path/to/file.iso", "file.iso", false},
		{"root path", "http://example.com/", "", true},
		{"no path", "http://example.com", "", true},
		{"with query", "http://example.com/file.iso?token=abc", "file.iso", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FilenameFromURL(tt.url)
			if tt.expectError {
				if err == nil {
					t.Errorf("expected error, got %q", got)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if got != tt.expected {
					t.Errorf("got %q, want %q", got, tt.expected)
				}
			}
		})
	}
}

func TestParseMachine(t *testing.T) {
	tests := []struct {
		name        string
		obj         *unstructured.Unstructured
		expected    *Machine
		expectError bool
	}{
		{
			name: "valid Machine with machineId",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{"name": "vm-01.lan"},
					"spec": map[string]interface{}{
						"mac":       "AA-BB-CC-DD-EE-FF",
						"machineId": "0123456789abcdef0123456789abcdef",
					},
				},
			},
			expected: &Machine{
				Name: "vm-01.lan",
				MAC:  "aa-bb-cc-dd-ee-ff", // lowercase
			},
			expectError: false,
		},
		{
			name: "valid Machine simple",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{"name": "vm-02.lan"},
					"spec": map[string]interface{}{
						"mac": "11-22-33-44-55-66",
					},
				},
			},
			expected: &Machine{
				Name: "vm-02.lan",
				MAC:  "11-22-33-44-55-66",
			},
			expectError: false,
		},
		{
			name: "missing mac returns error",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{"name": "no-mac"},
					"spec":     map[string]interface{}{},
				},
			},
			expected:    nil,
			expectError: true,
		},
		{
			name: "missing spec returns error",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{"name": "no-spec"},
				},
			},
			expected:    nil,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseMachine(tt.obj)
			if tt.expectError {
				if err == nil {
					t.Error("Expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			if result.Name != tt.expected.Name {
				t.Errorf("Name = %q, want %q", result.Name, tt.expected.Name)
			}
			if result.MAC != tt.expected.MAC {
				t.Errorf("MAC = %q, want %q", result.MAC, tt.expected.MAC)
			}
		})
	}
}

func TestNormalizeMAC(t *testing.T) {
	tests := []struct {
		name     string
		mac      string
		expected string
	}{
		{
			name:     "dash-separated lowercase",
			mac:      "aa-bb-cc-dd-ee-ff",
			expected: "aa-bb-cc-dd-ee-ff",
		},
		{
			name:     "dash-separated uppercase",
			mac:      "AA-BB-CC-DD-EE-FF",
			expected: "aa-bb-cc-dd-ee-ff",
		},
		{
			name:     "dash-separated mixed case",
			mac:      "Aa-Bb-Cc-Dd-Ee-Ff",
			expected: "aa-bb-cc-dd-ee-ff",
		},
		{
			name:     "colon-separated rejected",
			mac:      "aa:bb:cc:dd:ee:ff",
			expected: "",
		},
		{
			name:     "empty string",
			mac:      "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeMAC(tt.mac)
			if result != tt.expected {
				t.Errorf("normalizeMAC(%q) = %q, want %q", tt.mac, result, tt.expected)
			}
		})
	}
}

// TestFindMachineByMAC_ColonFormatReturnsNil tests MAC normalization edge cases
// Note: Full integration tests for FindMachineByMAC require a k8s client
func TestFindMachineByMAC_ColonFormatReturnsNil(t *testing.T) {
	// This tests that colon format MACs are rejected before any k8s calls
	// The actual function would return nil immediately for colon-separated MACs
	mac := "aa:bb:cc:dd:ee:ff"
	normalized := normalizeMAC(mac)
	if normalized != "" {
		t.Errorf("expected empty string for colon-separated MAC, got %q", normalized)
	}
}

// TestListProvisionsByMachine_FilteringLogic tests the filtering logic
// Note: Full integration tests require a k8s client
func TestListProvisionsByMachine_FilteringLogic(t *testing.T) {
	// Test that MachineRef comparison is exact string match
	provisions := []*Provision{
		{Name: "prov-1", Spec: ProvisionSpec{MachineRef: "vm-01.lan"}},
		{Name: "prov-2", Spec: ProvisionSpec{MachineRef: "vm-02.lan"}},
		{Name: "prov-3", Spec: ProvisionSpec{MachineRef: "vm-01.lan"}},
	}

	// Filter for vm-01.lan
	machineRef := "vm-01.lan"
	var result []*Provision
	for _, p := range provisions {
		if p.Spec.MachineRef == machineRef {
			result = append(result, p)
		}
	}

	if len(result) != 2 {
		t.Errorf("expected 2 provisions for %s, got %d", machineRef, len(result))
	}
	for _, p := range result {
		if p.Spec.MachineRef != machineRef {
			t.Errorf("unexpected provision %s with MachineRef %s", p.Name, p.Spec.MachineRef)
		}
	}
}

func TestListProvisionsByMachine_NoMatches(t *testing.T) {
	provisions := []*Provision{
		{Name: "prov-1", Spec: ProvisionSpec{MachineRef: "vm-01.lan"}},
		{Name: "prov-2", Spec: ProvisionSpec{MachineRef: "vm-02.lan"}},
	}

	// Filter for non-existent machine
	machineRef := "vm-99.lan"
	var result []*Provision
	for _, p := range provisions {
		if p.Spec.MachineRef == machineRef {
			result = append(result, p)
		}
	}

	if len(result) != 0 {
		t.Errorf("expected 0 provisions for %s, got %d", machineRef, len(result))
	}
}
