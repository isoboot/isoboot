package handlers

import (
	"strings"
	"testing"
)

// Test ED25519 key pair (generated for testing with ssh-keygen)
const testED25519PrivateKey = `-----BEGIN OPENSSH PRIVATE KEY-----
b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAAMwAAAAtzc2gtZW
QyNTUxOQAAACB+ajyqUbLe98zqQ86LWl5dFkBCtEVkSZXiFogJJ1eGfQAAAJjUjRwy1I0c
MgAAAAtzc2gtZWQyNTUxOQAAACB+ajyqUbLe98zqQ86LWl5dFkBCtEVkSZXiFogJJ1eGfQ
AAAEBTCGvVNZAxqiWIr5dnF+AkyPsi8FauWqNAV33OTroHOH5qPKpRst73zOpDzotaXl0W
QEK0RWRJleIWiAknV4Z9AAAAEHRlc3RAZXhhbXBsZS5jb20BAgMEBQ==
-----END OPENSSH PRIVATE KEY-----`

const testED25519PublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIH5qPKpRst73zOpDzotaXl0WQEK0RWRJleIWiAknV4Z9"

func TestDeriveSSHPublicKeys_ED25519(t *testing.T) {
	data := map[string]interface{}{
		"ssh_host_ed25519_key": testED25519PrivateKey,
	}

	err := deriveSSHPublicKeys(data)
	if err != nil {
		t.Fatalf("deriveSSHPublicKeys failed: %v", err)
	}

	pubKey, ok := data["ssh_host_ed25519_key_pub"]
	if !ok {
		t.Fatal("expected ssh_host_ed25519_key_pub to be set")
	}

	pubKeyStr, ok := pubKey.(string)
	if !ok {
		t.Fatal("expected ssh_host_ed25519_key_pub to be a string")
	}

	if pubKeyStr != testED25519PublicKey {
		t.Errorf("public key mismatch:\ngot:  %s\nwant: %s", pubKeyStr, testED25519PublicKey)
	}
}

func TestDeriveSSHPublicKeys_EmptyKey(t *testing.T) {
	data := map[string]interface{}{
		"ssh_host_ed25519_key": "",
	}

	err := deriveSSHPublicKeys(data)
	if err != nil {
		t.Fatalf("deriveSSHPublicKeys failed: %v", err)
	}

	// Should not add _pub key for empty private key
	if _, ok := data["ssh_host_ed25519_key_pub"]; ok {
		t.Error("expected ssh_host_ed25519_key_pub to NOT be set for empty key")
	}
}

func TestDeriveSSHPublicKeys_WhitespaceOnlyKey(t *testing.T) {
	data := map[string]interface{}{
		"ssh_host_ed25519_key": "   \n\t  ",
	}

	err := deriveSSHPublicKeys(data)
	if err != nil {
		t.Fatalf("deriveSSHPublicKeys failed: %v", err)
	}

	// Should not add _pub key for whitespace-only key
	if _, ok := data["ssh_host_ed25519_key_pub"]; ok {
		t.Error("expected ssh_host_ed25519_key_pub to NOT be set for whitespace-only key")
	}
}

func TestDeriveSSHPublicKeys_NoKeys(t *testing.T) {
	data := map[string]interface{}{
		"some_other_key": "some_value",
	}

	err := deriveSSHPublicKeys(data)
	if err != nil {
		t.Fatalf("deriveSSHPublicKeys failed: %v", err)
	}

	// Should not add any _pub keys
	for key := range data {
		if strings.HasSuffix(key, "_pub") {
			t.Errorf("unexpected _pub key: %s", key)
		}
	}
}

func TestDeriveSSHPublicKeys_NonStringValue(t *testing.T) {
	data := map[string]interface{}{
		"ssh_host_ed25519_key": 12345, // not a string
	}

	err := deriveSSHPublicKeys(data)
	if err != nil {
		t.Fatalf("deriveSSHPublicKeys failed: %v", err)
	}

	// Should skip non-string values without error
	if _, ok := data["ssh_host_ed25519_key_pub"]; ok {
		t.Error("expected ssh_host_ed25519_key_pub to NOT be set for non-string value")
	}
}

func TestDeriveSSHPublicKeys_InvalidKey(t *testing.T) {
	data := map[string]interface{}{
		"ssh_host_ed25519_key": "not a valid key",
	}

	err := deriveSSHPublicKeys(data)
	if err == nil {
		t.Fatal("expected error for invalid key")
	}

	if !strings.Contains(err.Error(), "ssh_host_ed25519_key") {
		t.Errorf("error should mention key name: %v", err)
	}
}

func TestDeriveSSHPublicKeys_PreservesOtherData(t *testing.T) {
	data := map[string]interface{}{
		"ssh_host_ed25519_key": testED25519PrivateKey,
		"hostname":             "vm-01",
		"password":             "secret",
	}

	err := deriveSSHPublicKeys(data)
	if err != nil {
		t.Fatalf("deriveSSHPublicKeys failed: %v", err)
	}

	// Check original data preserved
	if data["hostname"] != "vm-01" {
		t.Error("expected hostname to be preserved")
	}
	if data["password"] != "secret" {
		t.Error("expected password to be preserved")
	}
}

func TestDerivePublicKey_ED25519(t *testing.T) {
	pubKey, err := derivePublicKey(testED25519PrivateKey)
	if err != nil {
		t.Fatalf("derivePublicKey failed: %v", err)
	}

	if !strings.HasPrefix(pubKey, "ssh-ed25519 ") {
		t.Errorf("expected ssh-ed25519 prefix, got: %s", pubKey)
	}

	if pubKey != testED25519PublicKey {
		t.Errorf("public key mismatch:\ngot:  %s\nwant: %s", pubKey, testED25519PublicKey)
	}
}

func TestDerivePublicKey_InvalidKey(t *testing.T) {
	_, err := derivePublicKey("not a valid key")
	if err == nil {
		t.Fatal("expected error for invalid key")
	}

	if !strings.Contains(err.Error(), "parse private key") {
		t.Errorf("error should mention parsing: %v", err)
	}
}

func TestDerivePublicKey_EmptyKey(t *testing.T) {
	_, err := derivePublicKey("")
	if err == nil {
		t.Fatal("expected error for empty key")
	}
}

func TestRenderAnswerTemplate_Simple(t *testing.T) {
	tmpl := "Hello, {{ .Name }}!"
	data := map[string]interface{}{
		"Name": "World",
	}

	result, err := renderAnswerTemplate(tmpl, data)
	if err != nil {
		t.Fatalf("renderAnswerTemplate failed: %v", err)
	}

	if result != "Hello, World!" {
		t.Errorf("expected 'Hello, World!', got %q", result)
	}
}

func TestRenderAnswerTemplate_B64enc(t *testing.T) {
	tmpl := "{{ .Password | b64enc }}"
	data := map[string]interface{}{
		"Password": "secret",
	}

	result, err := renderAnswerTemplate(tmpl, data)
	if err != nil {
		t.Fatalf("renderAnswerTemplate failed: %v", err)
	}

	// "secret" base64 encoded is "c2VjcmV0"
	if result != "c2VjcmV0" {
		t.Errorf("expected 'c2VjcmV0', got %q", result)
	}
}

func TestRenderAnswerTemplate_HasKey_Present(t *testing.T) {
	tmpl := "{{ if hasKey . \"MachineId\" }}ID={{ .MachineId }}{{ else }}no-id{{ end }}"
	data := map[string]interface{}{
		"MachineId": "abc123",
	}

	result, err := renderAnswerTemplate(tmpl, data)
	if err != nil {
		t.Fatalf("renderAnswerTemplate failed: %v", err)
	}

	if result != "ID=abc123" {
		t.Errorf("expected 'ID=abc123', got %q", result)
	}
}

func TestRenderAnswerTemplate_HasKey_Absent(t *testing.T) {
	tmpl := "{{ if hasKey . \"MachineId\" }}ID={{ .MachineId }}{{ else }}no-id{{ end }}"
	data := map[string]interface{}{
		"Hostname": "vm-01",
	}

	result, err := renderAnswerTemplate(tmpl, data)
	if err != nil {
		t.Fatalf("renderAnswerTemplate failed: %v", err)
	}

	if result != "no-id" {
		t.Errorf("expected 'no-id', got %q", result)
	}
}

func TestRenderAnswerTemplate_MissingKey_Error(t *testing.T) {
	tmpl := "{{ .MissingKey }}"
	data := map[string]interface{}{
		"Hostname": "vm-01",
	}

	_, err := renderAnswerTemplate(tmpl, data)
	if err == nil {
		t.Fatal("expected error for missing key")
	}

	if !strings.Contains(err.Error(), "MissingKey") {
		t.Errorf("error should mention missing key: %v", err)
	}
}

func TestRenderAnswerTemplate_SyntaxError(t *testing.T) {
	tmpl := "{{ .Name"  // missing closing braces
	data := map[string]interface{}{
		"Name": "test",
	}

	_, err := renderAnswerTemplate(tmpl, data)
	if err == nil {
		t.Fatal("expected error for syntax error")
	}

	if !strings.Contains(err.Error(), "parse template") {
		t.Errorf("error should mention parsing: %v", err)
	}
}

func TestRenderAnswerTemplate_MultilineOutput(t *testing.T) {
	tmpl := `Host: {{ .Host }}
Port: {{ .Port }}
Hostname: {{ .Hostname }}`
	data := map[string]interface{}{
		"Host":     "192.168.1.1",
		"Port":     "8080",
		"Hostname": "vm-01",
	}

	result, err := renderAnswerTemplate(tmpl, data)
	if err != nil {
		t.Fatalf("renderAnswerTemplate failed: %v", err)
	}

	expected := `Host: 192.168.1.1
Port: 8080
Hostname: vm-01`
	if result != expected {
		t.Errorf("mismatch:\ngot:\n%s\nwant:\n%s", result, expected)
	}
}

func TestRenderAnswerTemplate_EmptyTemplate(t *testing.T) {
	tmpl := ""
	data := map[string]interface{}{}

	result, err := renderAnswerTemplate(tmpl, data)
	if err != nil {
		t.Fatalf("renderAnswerTemplate failed: %v", err)
	}

	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}

func TestRenderAnswerTemplate_EmptyData(t *testing.T) {
	tmpl := "static content"
	data := map[string]interface{}{}

	result, err := renderAnswerTemplate(tmpl, data)
	if err != nil {
		t.Fatalf("renderAnswerTemplate failed: %v", err)
	}

	if result != "static content" {
		t.Errorf("expected 'static content', got %q", result)
	}
}
