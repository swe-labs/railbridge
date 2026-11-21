package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
)

var (
	ErrInvalidKeySize    = errors.New("crypto: invalid key size, must be 32 bytes for AES-256")
	ErrCiphertextTooShort = errors.New("crypto: ciphertext too short")
	ErrSignatureInvalid  = errors.New("crypto: signature verification failed")
	ErrTimestampExpired  = errors.New("crypto: timestamp is outside acceptable sliding window")
)

// EncryptAESGCM encrypts plaintext using AES-256-GCM with a random 12-byte nonce.
func EncryptAESGCM(key, plaintext []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, ErrInvalidKeySize
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes.NewCipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("cipher.NewGCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	// Nonce is prepended to ciphertext
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// DecryptAESGCM decrypts ciphertext produced by EncryptAESGCM.
func DecryptAESGCM(key, ciphertext []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, ErrInvalidKeySize
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes.NewCipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("cipher.NewGCM: %w", err)
	}
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, ErrCiphertextTooShort
	}
	nonce, actualCiphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, actualCiphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("gcm.Open: %w", err)
	}
	return plaintext, nil
}

// HashSHA256 returns the SHA-256 hash of the input data.
func HashSHA256(data []byte) []byte {
	h := sha256.Sum256(data)
	return h[:]
}

// SignHMAC computes HMAC-SHA256 of message using the provided secret key.
func SignHMAC(secret, message []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(message)
	return hex.EncodeToString(mac.Sum(nil))
}

// BuildTimestampedSignature formats the signature payload string: t=<timestamp>,v1=<sig>.
func BuildTimestampedSignature(secret []byte, timestamp int64, body []byte) string {
	payload := fmt.Sprintf("%d.%s", timestamp, string(body))
	sig := SignHMAC(secret, []byte(payload))
	return fmt.Sprintf("t=%d,v1=%s", timestamp, sig)
}

// VerifyTimestampedSignature verifies an HMAC-SHA256 header against the raw body.
// It enforces a maximum allowed age window (default: 300 seconds) and uses constant-time comparison.
func VerifyTimestampedSignature(header string, secret, body []byte, maxAge time.Duration) error {
	var timestampStr, signatureHex string
	parts := strings.Split(header, ",")
	for _, part := range parts {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) == 2 {
			switch kv[0] {
			case "t":
				timestampStr = kv[1]
			case "v1":
				signatureHex = kv[1]
			}
		}
	}

	if timestampStr == "" || signatureHex == "" {
		return ErrSignatureInvalid
	}

	ts, err := strconv.ParseInt(timestampStr, 10, 64)
	if err != nil {
		return ErrSignatureInvalid
	}

	// Verify timestamp freshness within sliding window
	eventTime := time.Unix(ts, 0)
	diff := math.Abs(time.Since(eventTime).Seconds())
	if diff > maxAge.Seconds() {
		return ErrTimestampExpired
	}

	// Recalculate signature
	payload := fmt.Sprintf("%d.%s", ts, string(body))
	expectedSig := SignHMAC(secret, []byte(payload))

	// Constant-time comparison to prevent timing side-channel attacks
	if subtle.ConstantTimeCompare([]byte(signatureHex), []byte(expectedSig)) != 1 {
		return ErrSignatureInvalid
	}

	return nil
}
