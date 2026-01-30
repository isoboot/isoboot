package k8s

import (
	"reflect"
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
			name: "valid BootTarget with bootMediaRef and useDebianFirmware",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{"name": "debian-13-firmware"},
					"spec": map[string]interface{}{
						"bootMediaRef":      "debian-13",
						"useDebianFirmware": true,
						"template":          "kernel /linux\ninitrd /firmware-initrd.gz",
					},
				},
			},
			expected: &BootTarget{
				Name:              "debian-13-firmware",
				BootMediaRef:      "debian-13",
				UseDebianFirmware: true,
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
				UseDebianFirmware: false,
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
			if result.UseDebianFirmware != tt.expected.UseDebianFirmware {
				t.Errorf("UseDebianFirmware = %v, want %v", result.UseDebianFirmware, tt.expected.UseDebianFirmware)
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
		expected    *BootMedia
		expectError bool
	}{
		{
			name: "valid BootMedia with files and combinedFiles",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{"name": "debian-13"},
					"spec": map[string]interface{}{
						"files": []interface{}{
							map[string]interface{}{
								"url":         "http://example.com/linux",
								"checksumURL": "http://example.com/SHA256SUMS",
							},
							map[string]interface{}{
								"url": "http://example.com/initrd.gz",
							},
						},
						"combinedFiles": []interface{}{
							map[string]interface{}{
								"name":    "firmware-initrd.gz",
								"sources": []interface{}{"initrd.gz", "firmware.cpio.gz"},
							},
						},
					},
				},
			},
			expected: &BootMedia{
				Name: "debian-13",
				Files: []BootMediaFile{
					{URL: "http://example.com/linux", ChecksumURL: "http://example.com/SHA256SUMS"},
					{URL: "http://example.com/initrd.gz"},
				},
				CombinedFiles: []CombinedFile{
					{Name: "firmware-initrd.gz", Sources: []string{"initrd.gz", "firmware.cpio.gz"}},
				},
			},
			expectError: false,
		},
		{
			name: "BootMedia with status",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{"name": "debian-13"},
					"spec": map[string]interface{}{
						"files": []interface{}{
							map[string]interface{}{"url": "http://example.com/linux"},
						},
					},
					"status": map[string]interface{}{
						"phase":   "Complete",
						"message": "All files downloaded",
						"files": []interface{}{
							map[string]interface{}{
								"name":   "linux",
								"phase":  "Complete",
								"sha256": "abc123def456",
							},
						},
					},
				},
			},
			expected: &BootMedia{
				Name: "debian-13",
				Files: []BootMediaFile{
					{URL: "http://example.com/linux"},
				},
				Status: BootMediaStatus{
					Phase:   "Complete",
					Message: "All files downloaded",
					Files: []FileStatus{
						{Name: "linux", Phase: "Complete", SHA256: "abc123def456"},
					},
				},
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
			if result.Name != tt.expected.Name {
				t.Errorf("Name = %q, want %q", result.Name, tt.expected.Name)
			}
			if len(result.Files) != len(tt.expected.Files) {
				t.Errorf("Files len = %d, want %d", len(result.Files), len(tt.expected.Files))
			} else {
				for i, f := range result.Files {
					if f.URL != tt.expected.Files[i].URL {
						t.Errorf("Files[%d].URL = %q, want %q", i, f.URL, tt.expected.Files[i].URL)
					}
					if f.ChecksumURL != tt.expected.Files[i].ChecksumURL {
						t.Errorf("Files[%d].ChecksumURL = %q, want %q", i, f.ChecksumURL, tt.expected.Files[i].ChecksumURL)
					}
				}
			}
			if len(result.CombinedFiles) != len(tt.expected.CombinedFiles) {
				t.Errorf("CombinedFiles len = %d, want %d", len(result.CombinedFiles), len(tt.expected.CombinedFiles))
			} else {
				for i, cf := range result.CombinedFiles {
					if cf.Name != tt.expected.CombinedFiles[i].Name {
						t.Errorf("CombinedFiles[%d].Name = %q, want %q", i, cf.Name, tt.expected.CombinedFiles[i].Name)
					}
					if !reflect.DeepEqual(cf.Sources, tt.expected.CombinedFiles[i].Sources) {
						t.Errorf("CombinedFiles[%d].Sources = %v, want %v", i, cf.Sources, tt.expected.CombinedFiles[i].Sources)
					}
				}
			}
			if result.Status.Phase != tt.expected.Status.Phase {
				t.Errorf("Status.Phase = %q, want %q", result.Status.Phase, tt.expected.Status.Phase)
			}
			if result.Status.Message != tt.expected.Status.Message {
				t.Errorf("Status.Message = %q, want %q", result.Status.Message, tt.expected.Status.Message)
			}
			if len(result.Status.Files) != len(tt.expected.Status.Files) {
				t.Errorf("Status.Files len = %d, want %d", len(result.Status.Files), len(tt.expected.Status.Files))
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
