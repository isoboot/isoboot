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

func TestParseDeploy_WithNewFields(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{"name": "test-deploy"},
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

	result, err := parseDeploy(obj)
	if err != nil {
		t.Fatalf("parseDeploy failed: %v", err)
	}

	if result.Name != "test-deploy" {
		t.Errorf("Name = %q, want %q", result.Name, "test-deploy")
	}
	if result.Spec.MachineRef != "vm125" {
		t.Errorf("MachineRef = %q, want %q", result.Spec.MachineRef, "vm125")
	}
	if result.Spec.BootTargetRef != "debian-13" {
		t.Errorf("Target = %q, want %q", result.Spec.BootTargetRef, "debian-13")
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

func TestParseDeploy_NoOptionalFields(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{"name": "minimal-deploy"},
			"spec": map[string]interface{}{
				"machineRef": "vm125",
				"target":     "debian-13",
			},
		},
	}

	result, err := parseDeploy(obj)
	if err != nil {
		t.Fatalf("parseDeploy failed: %v", err)
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
