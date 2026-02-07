package wallet

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/bytedance/sonic"
	"github.com/google/uuid"
	"golang.org/x/crypto/scrypt"
)

var (
	// ErrKeyNotFound is returned when a key is not found in the keychain.
	ErrKeyNotFound = errors.New("key not found in keychain")
	// ErrInvalidPassword is returned when the decryption password is incorrect.
	ErrInvalidPassword = errors.New("invalid password")
	// ErrInvalidKeyData is returned when the key data is invalid.
	ErrInvalidKeyData = errors.New("invalid key data")
)

// KeyType represents the type of cryptographic key.
type KeyType string

const (
	// KeyTypeEd25519 is used for Solana wallets.
	KeyTypeEd25519 KeyType = "ed25519"
	// KeyTypeSecp256k1 is used for Ethereum/Base wallets.
	KeyTypeSecp256k1 KeyType = "secp256k1"
)

// KeyData represents encrypted key material.
type KeyData struct {
	ID        string  `json:"id"`
	Type      KeyType `json:"type"`
	Chain     string  `json:"chain"`     // "solana" or "base"
	PublicKey string  `json:"publicKey"` // For identification
	Nonce     []byte  `json:"nonce"`
	Salt      []byte  `json:"salt"`
	Cipher    string  `json:"cipher"` // "aes-256-gcm"
	Key       []byte  `json:"key"`    // Encrypted private key
}

// Keychain represents an encrypted storage for wallet keys.
type Keychain struct {
	keys map[string]*KeyData
}

// NewKeychain creates a new empty keychain.
func NewKeychain() *Keychain {
	return &Keychain{
		keys: make(map[string]*KeyData),
	}
}

// scryptParams defines the key derivation parameters.
type scryptParams struct {
	N       int // CPU/memory cost parameter
	R       int // block size parameter
	P       int // parallelization parameter
	KeyLen  int // length of the derived key
	SaltLen int // length of the salt
}

// defaultScryptParams returns secure scrypt parameters.
func defaultScryptParams() scryptParams {
	return scryptParams{
		N:       32768, // 2^15
		R:       8,
		P:       1,
		KeyLen:  32, // 256 bits for AES-256
		SaltLen: 32,
	}
}

// deriveKey derives an encryption key from the password using scrypt.
func deriveKey(password string, salt []byte) ([]byte, error) {
	params := defaultScryptParams()
	return scrypt.Key([]byte(password), salt, params.N, params.R, params.P, params.KeyLen)
}

// encrypt encrypts plaintext using AES-256-GCM.
func encrypt(plaintext []byte, key []byte) ([]byte, []byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)
	return ciphertext, nonce, nil
}

// decrypt decrypts ciphertext using AES-256-GCM.
func decrypt(ciphertext []byte, nonce []byte, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt: %w", err)
	}

	return plaintext, nil
}

// AddKey adds a new key to the keychain with password encryption.
func (k *Keychain) AddKey(
	keyType KeyType,
	chain string,
	publicKey string,
	privateKey []byte,
	password string,
) (*KeyData, error) {
	// Generate salt
	salt := make([]byte, defaultScryptParams().SaltLen)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("failed to generate salt: %w", err)
	}

	// Derive encryption key from password
	encKey, err := deriveKey(password, salt)
	if err != nil {
		return nil, fmt.Errorf("failed to derive key: %w", err)
	}

	// Encrypt the private key
	ciphertext, nonce, err := encrypt(privateKey, encKey)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt private key: %w", err)
	}

	// Create key data
	keyData := &KeyData{
		ID:        uuid.New().String(),
		Type:      keyType,
		Chain:     chain,
		PublicKey: publicKey,
		Nonce:     nonce,
		Salt:      salt,
		Cipher:    "aes-256-gcm",
		Key:       ciphertext,
	}

	k.keys[keyData.ID] = keyData
	return keyData, nil
}

// GetKey retrieves and decrypts a key from the keychain.
func (k *Keychain) GetKey(id string, password string) (KeyType, string, []byte, error) {
	keyData, exists := k.keys[id]
	if !exists {
		return "", "", nil, ErrKeyNotFound
	}

	// Derive decryption key from password
	decKey, err := deriveKey(password, keyData.Salt)
	if err != nil {
		return "", "", nil, fmt.Errorf("failed to derive key: %w", err)
	}

	// Decrypt the private key
	privateKey, err := decrypt(keyData.Key, keyData.Nonce, decKey)
	if err != nil {
		// Check if it's a decryption error (likely wrong password)
		if err.Error() == "failed to decrypt: cipher: message authentication failed" {
			return "", "", nil, ErrInvalidPassword
		}
		return "", "", nil, err
	}

	return keyData.Type, keyData.PublicKey, privateKey, nil
}

// ListKeys returns all key IDs and their public information (without private data).
func (k *Keychain) ListKeys() []KeyInfo {
	var keys []KeyInfo
	for _, keyData := range k.keys {
		keys = append(keys, KeyInfo{
			ID:        keyData.ID,
			Type:      keyData.Type,
			Chain:     keyData.Chain,
			PublicKey: keyData.PublicKey,
		})
	}
	return keys
}

// RemoveKey removes a key from the keychain.
func (k *Keychain) RemoveKey(id string) error {
	if _, exists := k.keys[id]; !exists {
		return ErrKeyNotFound
	}
	delete(k.keys, id)
	return nil
}

// KeyInfo contains public information about a key.
type KeyInfo struct {
	ID        string  `json:"id"`
	Type      KeyType `json:"type"`
	Chain     string  `json:"chain"`
	PublicKey string  `json:"publicKey"`
}

// Save saves the keychain to an encrypted file.
func (k *Keychain) Save(path string, password string) error {
	// Generate salt for file encryption
	salt := make([]byte, defaultScryptParams().SaltLen)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return fmt.Errorf("failed to generate salt: %w", err)
	}

	// Derive encryption key
	encKey, err := deriveKey(password, salt)
	if err != nil {
		return fmt.Errorf("failed to derive key: %w", err)
	}

	// Serialize keychain data - convert to slice to avoid custom JSON marshaling
	keyList := make([]*keyDataSerializable, 0, len(k.keys))
	for _, kd := range k.keys {
		keyList = append(keyList, &keyDataSerializable{
			ID:        kd.ID,
			Type:      kd.Type,
			Chain:     kd.Chain,
			PublicKey: kd.PublicKey,
			Nonce:     kd.Nonce,
			Salt:      kd.Salt,
			Cipher:    kd.Cipher,
			Key:       kd.Key,
		})
	}

	data, err := sonic.Marshal(keyList)
	if err != nil {
		return fmt.Errorf("failed to marshal keychain: %w", err)
	}

	// Encrypt the data
	ciphertext, nonce, err := encrypt(data, encKey)
	if err != nil {
		return fmt.Errorf("failed to encrypt keychain: %w", err)
	}

	// Create file structure
	fileData := struct {
		Version int    `json:"version"`
		Salt    []byte `json:"salt"`
		Nonce   []byte `json:"nonce"`
		Cipher  string `json:"cipher"`
		Data    []byte `json:"data"`
	}{
		Version: 1,
		Salt:    salt,
		Nonce:   nonce,
		Cipher:  "aes-256-gcm",
		Data:    ciphertext,
	}

	fileJSON, err := sonic.Marshal(fileData)
	if err != nil {
		return fmt.Errorf("failed to marshal file data: %w", err)
	}

	// Write to file
	if err := os.WriteFile(path, fileJSON, 0600); err != nil {
		return fmt.Errorf("failed to write keychain file: %w", err)
	}

	return nil
}

// keyDataSerializable is used for serialization without custom JSON marshaling.
type keyDataSerializable struct {
	ID        string  `json:"id"`
	Type      KeyType `json:"type"`
	Chain     string  `json:"chain"`
	PublicKey string  `json:"publicKey"`
	Nonce     []byte  `json:"nonce"`
	Salt      []byte  `json:"salt"`
	Cipher    string  `json:"cipher"`
	Key       []byte  `json:"key"`
}

// Load loads an encrypted keychain from a file.
func LoadKeychain(path string, password string) (*Keychain, error) {
	// Read file
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read keychain file: %w", err)
	}

	// Parse file structure
	var fileData struct {
		Version int    `json:"version"`
		Salt    []byte `json:"salt"`
		Nonce   []byte `json:"nonce"`
		Cipher  string `json:"cipher"`
		Data    []byte `json:"data"`
	}
	if err := sonic.Unmarshal(data, &fileData); err != nil {
		return nil, fmt.Errorf("failed to parse keychain file: %w", err)
	}

	// Verify version
	if fileData.Version != 1 {
		return nil, fmt.Errorf("unsupported keychain version: %d", fileData.Version)
	}

	// Derive decryption key
	decKey, err := deriveKey(password, fileData.Salt)
	if err != nil {
		return nil, fmt.Errorf("failed to derive key: %w", err)
	}

	// Decrypt the data
	decrypted, err := decrypt(fileData.Data, fileData.Nonce, decKey)
	if err != nil {
		return nil, ErrInvalidPassword
	}

	// Parse keychain data - deserialize from list
	var keyList []*keyDataSerializable
	if err := sonic.Unmarshal(decrypted, &keyList); err != nil {
		return nil, fmt.Errorf("failed to parse keychain data: %w", err)
	}

	// Convert to key map
	keys := make(map[string]*KeyData)
	for _, ks := range keyList {
		keys[ks.ID] = &KeyData{
			ID:        ks.ID,
			Type:      ks.Type,
			Chain:     ks.Chain,
			PublicKey: ks.PublicKey,
			Nonce:     ks.Nonce,
			Salt:      ks.Salt,
			Cipher:    ks.Cipher,
			Key:       ks.Key,
		}
	}

	return &Keychain{keys: keys}, nil
}

// VerifyPassword checks if the provided password can decrypt the keychain.
func VerifyPassword(path string, password string) error {
	_, err := LoadKeychain(path, password)
	return err
}

// ChangePassword changes the encryption password for a keychain file.
func ChangePassword(path string, oldPassword string, newPassword string) error {
	// Load with old password
	kc, err := LoadKeychain(path, oldPassword)
	if err != nil {
		return fmt.Errorf("failed to load keychain: %w", err)
	}

	// Save with new password
	if err := kc.Save(path, newPassword); err != nil {
		return fmt.Errorf("failed to save keychain: %w", err)
	}

	return nil
}

// ExportKey exports a single key in encrypted JSON format.
func (k *Keychain) ExportKey(id string, password string, exportPassword string) ([]byte, error) {
	_, pubKey, privKey, err := k.GetKey(id, password)
	if err != nil {
		return nil, fmt.Errorf("failed to get key: %w", err)
	}

	keyData := k.keys[id]

	// Generate salt for export encryption
	salt := make([]byte, defaultScryptParams().SaltLen)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("failed to generate salt: %w", err)
	}

	// Derive encryption key
	encKey, err := deriveKey(exportPassword, salt)
	if err != nil {
		return nil, fmt.Errorf("failed to derive key: %w", err)
	}

	// Prepare export data
	exportData := struct {
		Type      KeyType `json:"type"`
		Chain     string  `json:"chain"`
		PublicKey string  `json:"publicKey"`
		Private   []byte  `json:"private"`
	}{
		Type:      keyData.Type,
		Chain:     keyData.Chain,
		PublicKey: pubKey,
		Private:   privKey,
	}

	data, err := sonic.Marshal(exportData)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal export data: %w", err)
	}

	// Encrypt
	ciphertext, nonce, err := encrypt(data, encKey)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt export: %w", err)
	}

	// Create export structure
	export := struct {
		Version int    `json:"version"`
		Salt    []byte `json:"salt"`
		Nonce   []byte `json:"nonce"`
		Cipher  string `json:"cipher"`
		Data    []byte `json:"data"`
	}{
		Version: 1,
		Salt:    salt,
		Nonce:   nonce,
		Cipher:  "aes-256-gcm",
		Data:    ciphertext,
	}

	return sonic.Marshal(export)
}

// ImportKey imports a key from encrypted JSON format.
func (k *Keychain) ImportKey(encryptedKey []byte, importPassword string, walletPassword string) (*KeyData, error) {
	// Parse export structure
	var export struct {
		Version int    `json:"version"`
		Salt    []byte `json:"salt"`
		Nonce   []byte `json:"nonce"`
		Cipher  string `json:"cipher"`
		Data    []byte `json:"data"`
	}
	if err := sonic.Unmarshal(encryptedKey, &export); err != nil {
		return nil, fmt.Errorf("failed to parse export: %w", err)
	}

	if export.Version != 1 {
		return nil, fmt.Errorf("unsupported export version: %d", export.Version)
	}

	// Derive decryption key
	decKey, err := deriveKey(importPassword, export.Salt)
	if err != nil {
		return nil, fmt.Errorf("failed to derive key: %w", err)
	}

	// Decrypt
	decrypted, err := decrypt(export.Data, export.Nonce, decKey)
	if err != nil {
		return nil, ErrInvalidPassword
	}

	// Parse export data
	var exportData struct {
		Type      KeyType `json:"type"`
		Chain     string  `json:"chain"`
		PublicKey string  `json:"publicKey"`
		Private   []byte  `json:"private"`
	}
	if err := sonic.Unmarshal(decrypted, &exportData); err != nil {
		return nil, fmt.Errorf("failed to parse export data: %w", err)
	}

	return k.AddKey(exportData.Type, exportData.Chain, exportData.PublicKey, exportData.Private, walletPassword)
}

// ClearSecurely zeroes out sensitive data in memory.
func ClearSecurely(data []byte) {
	for i := range data {
		data[i] = 0
	}
}

// SecureBuffer wraps sensitive data with automatic clearing.
type SecureBuffer struct {
	data []byte
}

// NewSecureBuffer creates a new secure buffer.
func NewSecureBuffer(data []byte) *SecureBuffer {
	return &SecureBuffer{data: data}
}

// Data returns the underlying data.
func (sb *SecureBuffer) Data() []byte {
	return sb.data
}

// Dispose clears the sensitive data.
func (sb *SecureBuffer) Dispose() {
	ClearSecurely(sb.data)
	sb.data = nil
}

// Clone creates a copy of the secure buffer.
func (sb *SecureBuffer) Clone() *SecureBuffer {
	if sb.data == nil {
		return &SecureBuffer{data: nil}
	}
	data := make([]byte, len(sb.data))
	copy(data, sb.data)
	return NewSecureBuffer(data)
}

// KeychainData is a helper for exporting the entire keychain.
type KeychainData struct {
	Keys []*KeyData `json:"keys"`
}

// MarshalJSON implements custom JSON marshaling that never logs private keys.
func (kd *KeyData) MarshalJSON() ([]byte, error) {
	type Alias KeyData
	return sonic.Marshal(&struct {
		*Alias // Embedded field comes first

		Key string `json:"key"` // Redacted
	}{
		Alias: (*Alias)(kd),
		Key:   "[REDACTED]",
	})
}

// String returns a safe string representation (no private keys).
func (kd *KeyData) String() string {
	return fmt.Sprintf("KeyData{ID: %s, Type: %s, Chain: %s, PublicKey: %s}", kd.ID, kd.Type, kd.Chain, kd.PublicKey)
}

// Equals checks if two keychains are equal.
func (k *Keychain) Equals(other *Keychain) bool {
	if len(k.keys) != len(other.keys) {
		return false
	}
	for id, key := range k.keys {
		otherKey, exists := other.keys[id]
		if !exists {
			return false
		}
		if key.ID != otherKey.ID || key.Type != otherKey.Type || key.Chain != otherKey.Chain ||
			key.PublicKey != otherKey.PublicKey {
			return false
		}
		if !bytes.Equal(key.Salt, otherKey.Salt) || !bytes.Equal(key.Nonce, otherKey.Nonce) ||
			!bytes.Equal(key.Key, otherKey.Key) {
			return false
		}
	}
	return true
}
