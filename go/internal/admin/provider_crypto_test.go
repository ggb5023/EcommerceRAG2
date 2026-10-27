package admin

import (
	"bytes"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func masterKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	return key
}

func TestProviderKeyEncryptionRoundTripAndLast4(t *testing.T) {
	key := masterKey(t)
	ciphertext, last4, err := EncryptProviderKey(key, "sk-live-1234567890")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ciphertext, "v1:") || bytes.Contains([]byte(ciphertext), []byte("sk-live")) {
		t.Fatalf("ciphertext leaks plaintext: %s", ciphertext)
	}
	if last4 != "7890" {
		t.Fatalf("last4=%s", last4)
	}
	plaintext, err := DecryptProviderKey(key, ciphertext)
	if err != nil || plaintext != "sk-live-1234567890" {
		t.Fatalf("round trip failed: %q %v", plaintext, err)
	}
	if ciphertext2, last42, _ := EncryptProviderKey(key, "sk-live-1234567890"); ciphertext2 == ciphertext || last42 != last4 {
		t.Fatal("encryption must use a fresh nonce")
	}
	if ciphertext, last4, err = EncryptProviderKey(key, ""); err != nil || ciphertext != "" || last4 != "" {
		t.Fatal("empty key must remain empty for local endpoints")
	}
	if _, err = DecryptProviderKey(masterKey(t), "v1:bad:bad"); err == nil || strings.Contains(err.Error(), "bad") {
		t.Fatal("corrupt ciphertext must fail without echoing it")
	}
}

func TestProviderMasterKeyPermissions(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0o700)
	path := filepath.Join(dir, "provider-master.key")
	if err := os.WriteFile(path, masterKey(t), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProviderMasterKey(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProviderMasterKey(path); err == nil {
		t.Fatal("group-readable master key accepted")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.key")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProviderMasterKey(link); err == nil {
		t.Fatal("symlink master key accepted")
	}
	short := filepath.Join(dir, "short.key")
	if err := os.WriteFile(short, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProviderMasterKey(short); err == nil {
		t.Fatal("short master key accepted")
	}
}
