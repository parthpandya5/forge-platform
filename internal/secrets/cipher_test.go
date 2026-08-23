package secrets

import (
	"bytes"
	"testing"
)

func TestCipherRoundTripAndAssociatedData(t *testing.T) {
	cipher, err := New("", "test")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ciphertext, nonce, err := cipher.Encrypt([]byte("secret"), []byte("service:KEY"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	if bytes.Contains(ciphertext, []byte("secret")) {
		t.Fatal("ciphertext contains plaintext")
	}
	plaintext, err := cipher.Decrypt(ciphertext, nonce, []byte("service:KEY"))
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}
	if string(plaintext) != "secret" {
		t.Fatalf("plaintext = %q", plaintext)
	}
	if _, err := cipher.Decrypt(ciphertext, nonce, []byte("other:KEY")); err == nil {
		t.Fatal("Decrypt() with wrong associated data error = nil")
	}
}

func TestCipherRequiresKeyOutsideDevelopment(t *testing.T) {
	if _, err := New("", "production"); err == nil {
		t.Fatal("New() error = nil")
	}
}
