package vault

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestCrypto_Available_EmptyKey(t *testing.T) {
	c, err := NewCrypto("")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if c.Available() {
		t.Error("empty hex key should leave crypto in degraded mode")
	}
}

func TestCrypto_NewCrypto_InvalidHex(t *testing.T) {
	if _, err := NewCrypto("not-hex"); err == nil {
		t.Error("expected error for invalid hex")
	}
}

func TestCrypto_NewCrypto_WrongLength(t *testing.T) {
	if _, err := NewCrypto("aabbccdd"); err == nil {
		t.Error("expected error for wrong-length key")
	}
}

func TestCrypto_EncryptDecrypt_RoundTrip_MasterKey(t *testing.T) {
	hexKey, err := GenerateMasterKeyHex()
	if err != nil {
		t.Fatalf("gen hex: %v", err)
	}
	c, err := NewCrypto(hexKey)
	if err != nil {
		t.Fatalf("new crypto: %v", err)
	}
	plain := "supersecret-db-password-123"
	id := uuid.NewString()
	name := "DB_PROD"
	ct, nonce, keyID, err := c.EncryptSecret(plain, id, name, nil)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if keyID != "master-v1" {
		t.Errorf("expected master-v1 key_id, got %s", keyID)
	}
	if string(ct) == plain {
		t.Error("ciphertext equals plaintext")
	}
	pt, err := c.DecryptSecret(ct, nonce, id, name, keyID)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if pt != plain {
		t.Errorf("round-trip mismatch: got %q want %q", pt, plain)
	}
}

func TestCrypto_EncryptDecrypt_RoundTrip_ClientKey(t *testing.T) {
	hexKey, _ := GenerateMasterKeyHex()
	c, _ := NewCrypto(hexKey)
	plain := "client-specific-token"
	id := uuid.NewString()
	cid := uuid.New()
	ct, nonce, keyID, err := c.EncryptSecret(plain, id, "API_KEY", &cid)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if !strings.HasPrefix(keyID, "client-") || !strings.HasSuffix(keyID, "-v1") {
		t.Errorf("expected client-{uuid}-v1 key_id, got %s", keyID)
	}
	pt, err := c.DecryptSecret(ct, nonce, id, "API_KEY", keyID)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if pt != plain {
		t.Errorf("round-trip mismatch: got %q want %q", pt, plain)
	}
}

func TestCrypto_DegradedMode_FailsEncryptDecrypt(t *testing.T) {
	c, _ := NewCrypto("")
	if _, _, _, err := c.EncryptSecret("x", uuid.NewString(), "n", nil); err == nil {
		t.Error("expected ErrCryptoUnavailable on encrypt in degraded mode")
	}
	if _, err := c.DecryptSecret([]byte{1, 2, 3}, []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}, uuid.NewString(), "n", "master-v1"); err == nil {
		t.Error("expected error on decrypt in degraded mode")
	}
}

func TestCrypto_AAD_DifferentNameFailsDecrypt(t *testing.T) {
	hexKey, _ := GenerateMasterKeyHex()
	c, _ := NewCrypto(hexKey)
	id := uuid.NewString()
	ct, nonce, keyID, err := c.EncryptSecret("plain", id, "ORIGINAL_NAME", nil)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	// Misma id, mismo keyID, pero name distinto → AAD distinto → debe fallar.
	if _, err := c.DecryptSecret(ct, nonce, id, "TAMPERED_NAME", keyID); err == nil {
		t.Error("expected decrypt failure when name changed (AAD mismatch)")
	}
}

func TestCrypto_HKDF_Deterministic(t *testing.T) {
	hexKey, _ := GenerateMasterKeyHex()
	c, _ := NewCrypto(hexKey)
	cid := uuid.New()
	keyID := KeyIDFor(&cid)

	k1, err := c.deriveKey(keyID)
	if err != nil {
		t.Fatalf("derive 1: %v", err)
	}
	k2, err := c.deriveKey(keyID)
	if err != nil {
		t.Fatalf("derive 2: %v", err)
	}
	if string(k1) != string(k2) {
		t.Error("HKDF derivation should be deterministic for same (master, client_id)")
	}
}

func TestCrypto_HKDF_DifferentClientsDifferentKeys(t *testing.T) {
	hexKey, _ := GenerateMasterKeyHex()
	c, _ := NewCrypto(hexKey)
	a := uuid.New()
	b := uuid.New()
	ka, _ := c.deriveKey(KeyIDFor(&a))
	kb, _ := c.deriveKey(KeyIDFor(&b))
	if string(ka) == string(kb) {
		t.Error("different client_ids should derive different keys")
	}
}

func TestKeyIDFor_NilClient(t *testing.T) {
	if KeyIDFor(nil) != "master-v1" {
		t.Errorf("KeyIDFor(nil) should be master-v1")
	}
}

func TestGenerateMasterKeyHex_IsValid(t *testing.T) {
	hex, err := GenerateMasterKeyHex()
	if err != nil {
		t.Fatal(err)
	}
	if len(hex) != 64 {
		t.Errorf("expected 64-char hex, got %d", len(hex))
	}
	if _, err := NewCrypto(hex); err != nil {
		t.Errorf("generated key should round-trip: %v", err)
	}
}
