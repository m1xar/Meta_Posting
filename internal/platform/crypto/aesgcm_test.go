package crypto

import (
	"crypto/aes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAESGCMRoundTripAndRandomNonce(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	cipher, err := NewAESGCM(key)
	require.NoError(t, err)

	first, err := cipher.EncryptString("secret-meta-token", "connection:abc")
	require.NoError(t, err)
	second, err := cipher.EncryptString("secret-meta-token", "connection:abc")
	require.NoError(t, err)
	require.NotEqual(t, first.Nonce, second.Nonce)
	require.NotEqual(t, first.Ciphertext, second.Ciphertext)

	plaintext, err := cipher.DecryptString(first, "connection:abc")
	require.NoError(t, err)
	require.Equal(t, "secret-meta-token", plaintext)
}

func TestAESGCMRejectsTamperingAndWrongAAD(t *testing.T) {
	cipher, err := NewAESGCM([]byte("0123456789abcdef0123456789abcdef"))
	require.NoError(t, err)
	encrypted, err := cipher.EncryptString("secret", "connection:abc")
	require.NoError(t, err)

	encrypted.Ciphertext[0] ^= 0xff
	_, err = cipher.DecryptString(encrypted, "connection:abc")
	require.Error(t, err)

	encrypted, err = cipher.EncryptString("secret", "connection:abc")
	require.NoError(t, err)
	_, err = cipher.DecryptString(encrypted, "connection:different")
	require.Error(t, err)
}

func TestAESGCMCompactEncoding(t *testing.T) {
	cipher, err := NewAESGCMWithVersion([]byte("0123456789abcdef0123456789abcdef"), 7)
	require.NoError(t, err)
	value, err := cipher.EncryptString("secret", "aad")
	require.NoError(t, err)

	decoded, err := DecodeCompact(EncodeCompact(value), aes.BlockSize-4)
	require.NoError(t, err)
	require.Equal(t, value, decoded)
}

func TestAESGCMRequires256BitKey(t *testing.T) {
	_, err := NewAESGCM([]byte("too-short"))
	require.ErrorIs(t, err, ErrInvalidKeySize)
}
