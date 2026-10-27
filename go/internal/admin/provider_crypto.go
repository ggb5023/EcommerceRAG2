package admin

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"strings"
	"syscall"
)

const ProviderMasterKeyPath = "/etc/ecommerce-rag/provider-master.key"

var errProviderKey = errors.New("provider master key is invalid")

// LoadProviderMasterKey reads the 32-byte AES-256 master key from outside the
// repository. It is called at startup; errors never include the key material.
func LoadProviderMasterKey(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() != 32 {
		return nil, errProviderKey
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) {
		return nil, errProviderKey
	}
	stream, err := os.Open(path)
	if err != nil {
		return nil, errProviderKey
	}
	defer stream.Close()
	key := make([]byte, 32)
	if _, err = io.ReadFull(stream, key); err != nil {
		return nil, errProviderKey
	}
	return key, nil
}

func providerLast4(plaintext string) string {
	if len(plaintext) <= 4 {
		return plaintext
	}
	return plaintext[len(plaintext)-4:]
}

// EncryptProviderKey stores only AES-GCM ciphertext and the display-only last4.
func EncryptProviderKey(master []byte, plaintext string) (ciphertext, last4 string, err error) {
	if plaintext == "" {
		return "", "", nil
	}
	if len(master) != 32 {
		return "", "", errProviderKey
	}
	block, err := aes.NewCipher(master)
	if err != nil {
		return "", "", errProviderKey
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", "", errProviderKey
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", "", errProviderKey
	}
	sealed := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	return "v1:" + base64.RawStdEncoding.EncodeToString(nonce) + ":" + base64.RawStdEncoding.EncodeToString(sealed), providerLast4(plaintext), nil
}

func DecryptProviderKey(master []byte, ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}
	if len(master) != 32 || !strings.HasPrefix(ciphertext, "v1:") {
		return "", errProviderKey
	}
	parts := strings.Split(ciphertext, ":")
	if len(parts) != 3 {
		return "", errProviderKey
	}
	nonce, err := base64.RawStdEncoding.DecodeString(parts[1])
	if err != nil {
		return "", errProviderKey
	}
	sealed, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return "", errProviderKey
	}
	if len(sealed) < 16 {
		return "", errProviderKey
	}
	block, err := aes.NewCipher(master)
	if err != nil {
		return "", errProviderKey
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", errProviderKey
	}
	if len(nonce) != gcm.NonceSize() {
		return "", errProviderKey
	}
	opened, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", errProviderKey
	}
	return string(opened), nil
}
