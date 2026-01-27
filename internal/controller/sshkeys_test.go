package controller

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

	err := DeriveSSHPublicKeys(data)
	if err != nil {
		t.Fatalf("DeriveSSHPublicKeys failed: %v", err)
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

	err := DeriveSSHPublicKeys(data)
	if err != nil {
		t.Fatalf("DeriveSSHPublicKeys failed: %v", err)
	}

	// Should not add _pub key for empty private key
	if _, ok := data["ssh_host_ed25519_key_pub"]; ok {
		t.Error("expected ssh_host_ed25519_key_pub to NOT be set for empty key")
	}
}

func TestDeriveSSHPublicKeys_NoKeys(t *testing.T) {
	data := map[string]interface{}{
		"some_other_key": "some_value",
	}

	err := DeriveSSHPublicKeys(data)
	if err != nil {
		t.Fatalf("DeriveSSHPublicKeys failed: %v", err)
	}

	// Should not add any _pub keys
	for key := range data {
		if strings.HasSuffix(key, "_pub") {
			t.Errorf("unexpected _pub key: %s", key)
		}
	}
}

func TestDeriveSSHPublicKeys_MultipleKeys(t *testing.T) {
	data := map[string]interface{}{
		"ssh_host_ed25519_key": testED25519PrivateKey,
		"some_config":          "value",
	}

	err := DeriveSSHPublicKeys(data)
	if err != nil {
		t.Fatalf("DeriveSSHPublicKeys failed: %v", err)
	}

	// Check ED25519 pub key exists
	if _, ok := data["ssh_host_ed25519_key_pub"]; !ok {
		t.Error("expected ssh_host_ed25519_key_pub to be set")
	}

	// Check original data preserved
	if data["some_config"] != "value" {
		t.Error("expected some_config to be preserved")
	}
}

func TestDeriveSSHPublicKeys_InvalidKey(t *testing.T) {
	data := map[string]interface{}{
		"ssh_host_ed25519_key": "not a valid key",
	}

	err := DeriveSSHPublicKeys(data)
	if err == nil {
		t.Fatal("expected error for invalid key")
	}

	if !strings.Contains(err.Error(), "ssh_host_ed25519_key") {
		t.Errorf("error should mention key name: %v", err)
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
}
