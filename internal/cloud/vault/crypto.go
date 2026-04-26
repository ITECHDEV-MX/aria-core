// Crypto helpers para el vault: AES-256-GCM + HKDF-SHA256 derivation.
//
// Master key strategy:
//   - master-v1 = los 32 bytes raw del ARIA_CORE_VAULT_MASTER_KEY (hex de 64 chars).
//   - client-{uuid}-v1 = HKDF(master, salt=clientID.Bytes(), info="aria-vault-v1", len=32).
//   - secrets sin client_id se cifran con master-v1 directo.
//
// Cada secret tiene su propio nonce (12 bytes random). AAD es: id || name || keyID
// para que el ciphertext esté ligado al registro y no se pueda mover entre rows.
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/crypto/hkdf"
)

const (
	masterKeyID = "master-v1"
	hkdfInfo    = "aria-vault-v1"
	keyLen      = 32 // AES-256
	nonceLen    = 12 // GCM standard
)

// ErrCryptoUnavailable se retorna cuando el master key no está configurado.
// El vault sigue siendo usable en modo degradado (list metadata, no encrypt/decrypt).
var ErrCryptoUnavailable = errors.New("vault: master key not configured (ARIA_CORE_VAULT_MASTER_KEY empty); vault is in read-only-degraded mode")

// Crypto encapsula el master key y la lógica de derivación + AEAD.
type Crypto struct {
	masterKey []byte // 32 bytes; nil = degraded mode
}

// NewCrypto carga el master desde un hex string (64 chars => 32 bytes).
// Si hexKey está vacío retorna un Crypto en modo degradado: encrypt/decrypt
// fallan con ErrCryptoUnavailable, pero el resto del Store sigue funcionando.
func NewCrypto(hexKey string) (*Crypto, error) {
	hexKey = strings.TrimSpace(hexKey)
	if hexKey == "" {
		return &Crypto{masterKey: nil}, nil
	}
	raw, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("vault: decode master key hex: %w", err)
	}
	if len(raw) != keyLen {
		return nil, fmt.Errorf("vault: master key must be %d bytes (got %d)", keyLen, len(raw))
	}
	return &Crypto{masterKey: raw}, nil
}

// Available reporta si el master key está cargado (no-degraded).
func (c *Crypto) Available() bool {
	return c != nil && len(c.masterKey) == keyLen
}

// KeyIDFor retorna el key_id que se debe persistir junto al ciphertext
// según si tiene client_id o no.
func KeyIDFor(clientID *uuid.UUID) string {
	if clientID == nil {
		return masterKeyID
	}
	return "client-" + clientID.String() + "-v1"
}

// deriveKey resuelve el key_id a la clave AES de 32 bytes.
//   - master-v1               → master key directo
//   - client-{uuid}-v1        → HKDF(master, salt=uuid.Bytes(), info=hkdfInfo)
func (c *Crypto) deriveKey(keyID string) ([]byte, error) {
	if !c.Available() {
		return nil, ErrCryptoUnavailable
	}
	if keyID == masterKeyID {
		out := make([]byte, keyLen)
		copy(out, c.masterKey)
		return out, nil
	}
	if strings.HasPrefix(keyID, "client-") && strings.HasSuffix(keyID, "-v1") {
		mid := strings.TrimSuffix(strings.TrimPrefix(keyID, "client-"), "-v1")
		cid, err := uuid.Parse(mid)
		if err != nil {
			return nil, fmt.Errorf("vault: parse client_id from key_id %q: %w", keyID, err)
		}
		salt := cid[:] // 16 bytes
		r := hkdf.New(sha256.New, c.masterKey, salt, []byte(hkdfInfo))
		out := make([]byte, keyLen)
		if _, err := io.ReadFull(r, out); err != nil {
			return nil, fmt.Errorf("vault: hkdf derive: %w", err)
		}
		return out, nil
	}
	return nil, fmt.Errorf("vault: unknown key_id %q", keyID)
}

// EncryptSecret cifra plaintext con AES-256-GCM, retornando (ciphertext, nonce, keyID).
// El AAD es id || name || keyID para evitar mover ciphertext entre rows.
func (c *Crypto) EncryptSecret(plaintext, secretID, name string, clientID *uuid.UUID) (ct, nonce []byte, keyID string, err error) {
	keyID = KeyIDFor(clientID)
	key, err := c.deriveKey(keyID)
	if err != nil {
		return nil, nil, "", err
	}
	defer zero(key)

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, "", fmt.Errorf("vault: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, "", fmt.Errorf("vault: new gcm: %w", err)
	}
	nonce = make([]byte, nonceLen)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, "", fmt.Errorf("vault: rand nonce: %w", err)
	}
	aad := buildAAD(secretID, name, keyID)
	ct = gcm.Seal(nil, nonce, []byte(plaintext), aad)
	return ct, nonce, keyID, nil
}

// DecryptSecret descifra con AES-256-GCM. Espera el mismo (id, name, keyID) usado al cifrar
// para que el AAD coincida — si alguno cambió, falla con auth error.
func (c *Crypto) DecryptSecret(ciphertext, nonce []byte, secretID, name, keyID string) (string, error) {
	key, err := c.deriveKey(keyID)
	if err != nil {
		return "", err
	}
	defer zero(key)

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("vault: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("vault: new gcm: %w", err)
	}
	if len(nonce) != gcm.NonceSize() {
		return "", fmt.Errorf("vault: invalid nonce size %d (want %d)", len(nonce), gcm.NonceSize())
	}
	aad := buildAAD(secretID, name, keyID)
	plaintext, err := gcm.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return "", fmt.Errorf("vault: gcm open: %w", err)
	}
	return string(plaintext), nil
}

func buildAAD(id, name, keyID string) []byte {
	// Concatenación con separador para que collisions sean improbables (id es uuid de 36 chars).
	return []byte(id + "\x00" + name + "\x00" + keyID)
}

// zero borra el contenido de un buffer (defensa básica contra dump de memoria).
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// GenerateMasterKeyHex es una utilidad para bootstrap: produce 32 bytes random hex-encoded.
// Útil para el setup inicial; el operador exporta el output a ARIA_CORE_VAULT_MASTER_KEY.
func GenerateMasterKeyHex() (string, error) {
	raw := make([]byte, keyLen)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}
