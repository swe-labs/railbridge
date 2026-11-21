package crypto

import (
	"bytes"
	"testing"
	"time"
)

func TestAESGCM_Roundtrip(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	original := []byte("confidential_webhook_secret_rb_whsec_991823")
	ciphertext, err := EncryptAESGCM(key, original)
	if err != nil {
		t.Fatalf("encryption failed: %v", err)
	}

	if bytes.Equal(original, ciphertext) {
		t.Fatal("ciphertext matches plaintext")
	}

	decrypted, err := DecryptAESGCM(key, ciphertext)
	if err != nil {
		t.Fatalf("decryption failed: %v", err)
	}

	if !bytes.Equal(original, decrypted) {
		t.Fatalf("expected %q, got %q", original, decrypted)
	}
}

func TestVerifyTimestampedSignature(t *testing.T) {
	secret := []byte("rb_whsec_test_secret_12345")
	body := []byte(`{"event":"payment.verified","id":"pay_001"}`)
	now := time.Now().Unix()

	header := BuildTimestampedSignature(secret, now, body)

	// Valid signature should pass
	err := VerifyTimestampedSignature(header, secret, body, 300*time.Second)
	if err != nil {
		t.Fatalf("valid signature failed: %v", err)
	}

	// Tampered body should fail
	tamperedBody := []byte(`{"event":"payment.verified","id":"pay_002"}`)
	if err := VerifyTimestampedSignature(header, secret, tamperedBody, 300*time.Second); err == nil {
		t.Fatal("expected signature verification failure on tampered body")
	}

	// Expired timestamp should fail
	oldTimestamp := time.Now().Add(-600 * time.Second).Unix()
	expiredHeader := BuildTimestampedSignature(secret, oldTimestamp, body)
	if err := VerifyTimestampedSignature(expiredHeader, secret, body, 300*time.Second); err != ErrTimestampExpired {
		t.Fatalf("expected ErrTimestampExpired, got %v", err)
	}
}
