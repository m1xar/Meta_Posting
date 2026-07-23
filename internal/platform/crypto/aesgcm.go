package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

const (
	AES256KeySize     = 32
	DefaultKeyVersion = int16(1)
)

var (
	ErrInvalidKeySize    = errors.New("AES-256-GCM key must be exactly 32 bytes")
	ErrInvalidNonceSize  = errors.New("invalid AES-GCM nonce size")
	ErrInvalidCiphertext = errors.New("ciphertext is too short")
)

// EncryptedValue is safe to persist. Ciphertext contains the GCM
// authentication tag, Nonce must be stored beside it, and KeyVersion allows
// future online key rotation.
type EncryptedValue struct {
	Ciphertext []byte
	Nonce      []byte
	KeyVersion int16
}

type TokenCipher interface {
	Encrypt(plaintext, associatedData []byte) (EncryptedValue, error)
	Decrypt(value EncryptedValue, associatedData []byte) ([]byte, error)
}

type AESGCM struct {
	aead       cipher.AEAD
	random     io.Reader
	keyVersion int16
}

func NewAESGCM(key []byte) (*AESGCM, error) {
	return NewAESGCMWithVersion(key, DefaultKeyVersion)
}

func NewAESGCMWithVersion(key []byte, keyVersion int16) (*AESGCM, error) {
	if len(key) != AES256KeySize {
		return nil, ErrInvalidKeySize
	}
	if keyVersion <= 0 {
		return nil, errors.New("key version must be positive")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}
	return &AESGCM{
		aead:       aead,
		random:     rand.Reader,
		keyVersion: keyVersion,
	}, nil
}

func (c *AESGCM) Encrypt(plaintext, associatedData []byte) (EncryptedValue, error) {
	if c == nil || c.aead == nil {
		return EncryptedValue{}, errors.New("token cipher is not initialized")
	}

	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(c.random, nonce); err != nil {
		return EncryptedValue{}, fmt.Errorf("generate nonce: %w", err)
	}

	ciphertext := c.aead.Seal(nil, nonce, plaintext, associatedData)
	return EncryptedValue{
		Ciphertext: ciphertext,
		Nonce:      nonce,
		KeyVersion: c.keyVersion,
	}, nil
}

func (c *AESGCM) Decrypt(value EncryptedValue, associatedData []byte) ([]byte, error) {
	if c == nil || c.aead == nil {
		return nil, errors.New("token cipher is not initialized")
	}
	if value.KeyVersion != c.keyVersion {
		return nil, fmt.Errorf("unsupported key version %d", value.KeyVersion)
	}
	if len(value.Nonce) != c.aead.NonceSize() {
		return nil, ErrInvalidNonceSize
	}
	if len(value.Ciphertext) < c.aead.Overhead() {
		return nil, ErrInvalidCiphertext
	}

	plaintext, err := c.aead.Open(nil, value.Nonce, value.Ciphertext, associatedData)
	if err != nil {
		return nil, fmt.Errorf("authenticate and decrypt token: %w", err)
	}
	return plaintext, nil
}

func (c *AESGCM) EncryptString(plaintext, associatedData string) (EncryptedValue, error) {
	return c.Encrypt([]byte(plaintext), []byte(associatedData))
}

func (c *AESGCM) DecryptString(value EncryptedValue, associatedData string) (string, error) {
	plaintext, err := c.Decrypt(value, []byte(associatedData))
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// EncodeCompact is useful for transport and backups. The database model uses
// separate bytea columns and does not need this representation.
func EncodeCompact(value EncryptedValue) string {
	raw := make([]byte, 0, 2+len(value.Nonce)+len(value.Ciphertext))
	raw = append(raw, byte(value.KeyVersion>>8), byte(value.KeyVersion))
	raw = append(raw, value.Nonce...)
	raw = append(raw, value.Ciphertext...)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func DecodeCompact(encoded string, nonceSize int) (EncryptedValue, error) {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return EncryptedValue{}, fmt.Errorf("decode encrypted token: %w", err)
	}
	if nonceSize <= 0 || len(raw) < 2+nonceSize+16 {
		return EncryptedValue{}, ErrInvalidCiphertext
	}
	return EncryptedValue{
		KeyVersion: int16(raw[0])<<8 | int16(raw[1]),
		Nonce:      append([]byte(nil), raw[2:2+nonceSize]...),
		Ciphertext: append([]byte(nil), raw[2+nonceSize:]...),
	}, nil
}
