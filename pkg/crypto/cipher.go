package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"io"
)

// deriveKey normalizes key material to 32 bytes using SHA-256.
func deriveKey(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	key := make([]byte, len(sum))
	copy(key, sum[:])
	return key
}

// EncryptString encrypts plaintext using AES-GCM.
func EncryptString(secret string, plaintext string) ([]byte, error) {
	key := deriveKey(secret)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return ciphertext, nil
}

// DecryptToString decrypts AES-GCM data back to plaintext.
func DecryptToString(secret string, payload []byte) (string, error) {
	key := deriveKey(secret)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonceSize := gcm.NonceSize()
	if len(payload) < nonceSize {
		return "", io.ErrUnexpectedEOF
	}
	nonce := payload[:nonceSize]
	ciphertext := payload[nonceSize:]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}
