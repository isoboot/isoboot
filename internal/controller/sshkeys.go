package controller

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

// sshHostKeyPrefixes are the key names we look for in secrets
var sshHostKeyPrefixes = []string{
	"ssh_host_rsa_key",
	"ssh_host_ecdsa_key",
	"ssh_host_ed25519_key",
}

// DeriveSSHPublicKeys looks for SSH host private keys in the data map and
// derives their public keys. For each key like "ssh_host_ed25519_key", it adds
// "ssh_host_ed25519_key_pub" with the OpenSSH-formatted public key.
func DeriveSSHPublicKeys(data map[string]interface{}) error {
	for _, keyName := range sshHostKeyPrefixes {
		privateKeyPEM, ok := data[keyName]
		if !ok {
			continue
		}

		privateKeyStr, ok := privateKeyPEM.(string)
		if !ok {
			continue
		}

		// Skip empty keys
		if strings.TrimSpace(privateKeyStr) == "" {
			continue
		}

		pubKey, err := derivePublicKey(privateKeyStr)
		if err != nil {
			return fmt.Errorf("failed to derive public key for %s: %w", keyName, err)
		}

		data[keyName+"_pub"] = pubKey
	}

	return nil
}

// derivePublicKey parses an OpenSSH private key and returns the public key
// in OpenSSH authorized_keys format (e.g., "ssh-ed25519 AAAA...")
func derivePublicKey(privateKeyPEM string) (string, error) {
	// Parse the private key
	privateKey, err := ssh.ParseRawPrivateKey([]byte(privateKeyPEM))
	if err != nil {
		return "", fmt.Errorf("parse private key: %w", err)
	}

	// Extract the public key based on key type
	var pubKey ssh.PublicKey

	switch key := privateKey.(type) {
	case *rsa.PrivateKey:
		pubKey, err = ssh.NewPublicKey(&key.PublicKey)
	case *ecdsa.PrivateKey:
		pubKey, err = ssh.NewPublicKey(&key.PublicKey)
	case *ed25519.PrivateKey:
		pubKey, err = ssh.NewPublicKey(key.Public())
	default:
		return "", fmt.Errorf("unsupported key type: %T", privateKey)
	}

	if err != nil {
		return "", fmt.Errorf("create public key: %w", err)
	}

	// Format as OpenSSH public key (type + base64 data)
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pubKey))), nil
}
