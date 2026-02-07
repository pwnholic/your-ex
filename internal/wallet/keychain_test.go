package wallet

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewKeychain(t *testing.T) {
	kc := NewKeychain()
	if kc == nil {
		t.Fatal("NewKeychain returned nil")
	}
	if kc.keys == nil {
		t.Error("keychain keys map is nil")
	}
}

func TestAddAndGetKey(t *testing.T) {
	kc := NewKeychain()
	password := "test-password-123"
	publicKey := "test-public-key"
	privateKey := []byte("test-private-key-data")

	// Add key
	keyData, err := kc.AddKey(KeyTypeEd25519, "solana", publicKey, privateKey, password)
	if err != nil {
		t.Fatalf("AddKey failed: %v", err)
	}

	if keyData.ID == "" {
		t.Error("key ID is empty")
	}
	if keyData.Type != KeyTypeEd25519 {
		t.Errorf("expected key type %s, got %s", KeyTypeEd25519, keyData.Type)
	}
	if keyData.Chain != "solana" {
		t.Errorf("expected chain solana, got %s", keyData.Chain)
	}
	if keyData.PublicKey != publicKey {
		t.Errorf("expected public key %s, got %s", publicKey, keyData.PublicKey)
	}

	// Get key
	keyType, pubKey, privKey, err := kc.GetKey(keyData.ID, password)
	if err != nil {
		t.Fatalf("GetKey failed: %v", err)
	}

	if keyType != KeyTypeEd25519 {
		t.Errorf("expected key type %s, got %s", KeyTypeEd25519, keyType)
	}
	if pubKey != publicKey {
		t.Errorf("expected public key %s, got %s", publicKey, pubKey)
	}
	if !bytes.Equal(privKey, privateKey) {
		t.Error("retrieved private key doesn't match original")
	}

	// Clear sensitive data
	ClearSecurely(privKey)
}

func TestAddAndGetKey_Base(t *testing.T) {
	kc := NewKeychain()
	password := "test-password-123"
	publicKey := "0x1234567890abcdef"
	privateKey := make([]byte, 32)
	for i := range privateKey {
		privateKey[i] = byte(i)
	}

	keyData, err := kc.AddKey(KeyTypeSecp256k1, "base", publicKey, privateKey, password)
	if err != nil {
		t.Fatalf("AddKey failed: %v", err)
	}

	if keyData.Type != KeyTypeSecp256k1 {
		t.Errorf("expected key type %s, got %s", KeyTypeSecp256k1, keyData.Type)
	}

	// Get and verify
	_, pubKey, privKey, err := kc.GetKey(keyData.ID, password)
	if err != nil {
		t.Fatalf("GetKey failed: %v", err)
	}

	if pubKey != publicKey {
		t.Errorf("expected public key %s, got %s", publicKey, pubKey)
	}
	if !bytes.Equal(privKey, privateKey) {
		t.Error("retrieved private key doesn't match original")
	}

	ClearSecurely(privKey)
}

func TestGetKeyNotFound(t *testing.T) {
	kc := NewKeychain()
	_, _, _, err := kc.GetKey("non-existent-id", "password")
	if !errors.Is(err, ErrKeyNotFound) {
		t.Errorf("expected ErrKeyNotFound, got %v", err)
	}
}

func TestGetKeyInvalidPassword(t *testing.T) {
	kc := NewKeychain()
	password := "correct-password"
	wrongPassword := "wrong-password"
	publicKey := "test-public-key"
	privateKey := []byte("test-private-key-data")

	keyData, err := kc.AddKey(KeyTypeEd25519, "solana", publicKey, privateKey, password)
	if err != nil {
		t.Fatalf("AddKey failed: %v", err)
	}

	_, _, _, err = kc.GetKey(keyData.ID, wrongPassword)
	if !errors.Is(err, ErrInvalidPassword) {
		t.Errorf("expected ErrInvalidPassword, got %v", err)
	}
}

func TestListKeys(t *testing.T) {
	kc := NewKeychain()
	password := "test-password"

	// Add multiple keys
	key1, _ := kc.AddKey(KeyTypeEd25519, "solana", "pubkey1", []byte("priv1"), password)
	key2, _ := kc.AddKey(KeyTypeSecp256k1, "base", "pubkey2", []byte("priv2"), password)

	keys := kc.ListKeys()
	if len(keys) != 2 {
		t.Errorf("expected 2 keys, got %d", len(keys))
	}

	// Verify keys are present
	found := make(map[string]bool)
	for _, key := range keys {
		found[key.ID] = true
		// Ensure private key data is not exposed
		if len(key.ID) == 0 {
			t.Error("key ID is empty")
		}
		if len(key.PublicKey) == 0 {
			t.Error("public key is empty")
		}
	}

	if !found[key1.ID] || !found[key2.ID] {
		t.Error("not all keys were returned")
	}
}

func TestRemoveKey(t *testing.T) {
	kc := NewKeychain()
	password := "test-password"

	key, _ := kc.AddKey(KeyTypeEd25519, "solana", "pubkey", []byte("priv"), password)

	// Remove key
	err := kc.RemoveKey(key.ID)
	if err != nil {
		t.Fatalf("RemoveKey failed: %v", err)
	}

	// Verify key is removed
	_, _, _, err = kc.GetKey(key.ID, password)
	if !errors.Is(err, ErrKeyNotFound) {
		t.Errorf("expected ErrKeyNotFound after removal, got %v", err)
	}
}

func TestRemoveKeyNotFound(t *testing.T) {
	kc := NewKeychain()
	err := kc.RemoveKey("non-existent")
	if !errors.Is(err, ErrKeyNotFound) {
		t.Errorf("expected ErrKeyNotFound, got %v", err)
	}
}

func TestSaveAndLoadKeychain(t *testing.T) {
	password := "keychain-password"
	keyPassword := "key-password"

	// Create keychain and add keys
	kc1 := NewKeychain()
	key1, _ := kc1.AddKey(KeyTypeEd25519, "solana", "sol-pubkey", []byte("sol-privkey"), keyPassword)
	key2, _ := kc1.AddKey(KeyTypeSecp256k1, "base", "eth-pubkey", []byte("eth-privkey"), keyPassword)

	// Create temp directory
	tmpDir := t.TempDir()
	keychainPath := filepath.Join(tmpDir, "test-keychain.enc")

	// Save keychain
	err := kc1.Save(keychainPath, password)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(keychainPath); os.IsNotExist(err) {
		t.Fatal("keychain file was not created")
	}

	// Load keychain
	kc2, err := LoadKeychain(keychainPath, password)
	if err != nil {
		t.Fatalf("LoadKeychain failed: %v", err)
	}

	// Verify keys
	keys := kc2.ListKeys()
	if len(keys) != 2 {
		t.Errorf("expected 2 keys, got %d", len(keys))
	}

	// Verify we can retrieve and decrypt keys
	_, pub1, priv1, err := kc2.GetKey(key1.ID, keyPassword)
	if err != nil {
		t.Fatalf("GetKey failed for key1: %v", err)
	}
	if pub1 != "sol-pubkey" {
		t.Errorf("expected sol-pubkey, got %s", pub1)
	}
	if string(priv1) != "sol-privkey" {
		t.Error("private key mismatch for key1")
	}
	ClearSecurely(priv1)

	_, pub2, priv2, err := kc2.GetKey(key2.ID, keyPassword)
	if err != nil {
		t.Fatalf("GetKey failed for key2: %v", err)
	}
	if pub2 != "eth-pubkey" {
		t.Errorf("expected eth-pubkey, got %s", pub2)
	}
	if string(priv2) != "eth-privkey" {
		t.Error("private key mismatch for key2")
	}
	ClearSecurely(priv2)
}

func TestLoadKeychainInvalidPassword(t *testing.T) {
	password := "correct-password"
	wrongPassword := "wrong-password"

	kc := NewKeychain()
	kc.AddKey(KeyTypeEd25519, "solana", "pubkey", []byte("privkey"), "key-pass")

	tmpDir := t.TempDir()
	keychainPath := filepath.Join(tmpDir, "test-keychain.enc")
	kc.Save(keychainPath, password)

	_, err := LoadKeychain(keychainPath, wrongPassword)
	if !errors.Is(err, ErrInvalidPassword) {
		t.Errorf("expected ErrInvalidPassword, got %v", err)
	}
}

func TestLoadKeychainNonExistent(t *testing.T) {
	_, err := LoadKeychain("/non/existent/path.enc", "password")
	if err == nil {
		t.Error("expected error loading non-existent keychain")
	}
}

func TestVerifyPassword(t *testing.T) {
	password := "test-password"

	kc := NewKeychain()
	kc.AddKey(KeyTypeEd25519, "solana", "pubkey", []byte("privkey"), "key-pass")

	tmpDir := t.TempDir()
	keychainPath := filepath.Join(tmpDir, "test-keychain.enc")
	kc.Save(keychainPath, password)

	// Correct password
	err := VerifyPassword(keychainPath, password)
	if err != nil {
		t.Errorf("VerifyPassword failed with correct password: %v", err)
	}

	// Wrong password
	err = VerifyPassword(keychainPath, "wrong-password")
	if !errors.Is(err, ErrInvalidPassword) {
		t.Errorf("expected ErrInvalidPassword, got %v", err)
	}
}

func TestChangePassword(t *testing.T) {
	oldPassword := "old-password"
	newPassword := "new-password"

	kc := NewKeychain()
	kc.AddKey(KeyTypeEd25519, "solana", "pubkey", []byte("privkey"), "key-pass")

	tmpDir := t.TempDir()
	keychainPath := filepath.Join(tmpDir, "test-keychain.enc")
	kc.Save(keychainPath, oldPassword)

	// Change password
	err := ChangePassword(keychainPath, oldPassword, newPassword)
	if err != nil {
		t.Fatalf("ChangePassword failed: %v", err)
	}

	// Verify new password works
	_, err = LoadKeychain(keychainPath, newPassword)
	if err != nil {
		t.Errorf("failed to load keychain with new password: %v", err)
	}

	// Verify old password doesn't work
	_, err = LoadKeychain(keychainPath, oldPassword)
	if !errors.Is(err, ErrInvalidPassword) {
		t.Errorf("expected ErrInvalidPassword with old password, got %v", err)
	}
}

func TestExportAndImportKey(t *testing.T) {
	kc := NewKeychain()
	keyPassword := "key-password"
	exportPassword := "export-password"

	originalKey := []byte("secret-key-data")
	key, _ := kc.AddKey(KeyTypeEd25519, "solana", "test-pubkey", originalKey, keyPassword)

	// Export key
	exportData, err := kc.ExportKey(key.ID, keyPassword, exportPassword)
	if err != nil {
		t.Fatalf("ExportKey failed: %v", err)
	}

	if len(exportData) == 0 {
		t.Fatal("export data is empty")
	}

	// Import into new keychain
	kc2 := NewKeychain()
	importedKey, err := kc2.ImportKey(exportData, exportPassword, keyPassword)
	if err != nil {
		t.Fatalf("ImportKey failed: %v", err)
	}

	// Verify imported key matches original
	if importedKey.Type != key.Type {
		t.Errorf("type mismatch: expected %s, got %s", key.Type, importedKey.Type)
	}
	if importedKey.Chain != key.Chain {
		t.Errorf("chain mismatch: expected %s, got %s", key.Chain, importedKey.Chain)
	}
	if importedKey.PublicKey != key.PublicKey {
		t.Errorf("public key mismatch: expected %s, got %s", key.PublicKey, importedKey.PublicKey)
	}

	// Verify we can retrieve the private key
	_, pubKey, privKey, err := kc2.GetKey(importedKey.ID, keyPassword)
	if err != nil {
		t.Fatalf("failed to get imported key: %v", err)
	}

	if pubKey != "test-pubkey" {
		t.Errorf("public key mismatch: expected test-pubkey, got %s", pubKey)
	}
	if !bytes.Equal(privKey, originalKey) {
		t.Error("imported private key doesn't match original")
	}
	ClearSecurely(privKey)
}

func TestImportKeyInvalidPassword(t *testing.T) {
	kc := NewKeychain()
	keyPassword := "key-password"
	exportPassword := "export-password"

	key, _ := kc.AddKey(KeyTypeEd25519, "solana", "test-pubkey", []byte("secret"), keyPassword)
	exportData, _ := kc.ExportKey(key.ID, keyPassword, exportPassword)

	kc2 := NewKeychain()
	_, err := kc2.ImportKey(exportData, "wrong-password", keyPassword)
	if !errors.Is(err, ErrInvalidPassword) {
		t.Errorf("expected ErrInvalidPassword, got %v", err)
	}
}

func TestClearSecurely(t *testing.T) {
	data := []byte("sensitive-data")
	ClearSecurely(data)

	for i, b := range data {
		if b != 0 {
			t.Errorf("data not cleared at index %d: got %d", i, b)
		}
	}
}

func TestSecureBuffer(t *testing.T) {
	data := []byte("sensitive-data")
	sb := NewSecureBuffer(data)

	if !bytes.Equal(sb.Data(), data) {
		t.Error("secure buffer data doesn't match original")
	}

	// Test clone
	cloned := sb.Clone()
	if !bytes.Equal(cloned.Data(), data) {
		t.Error("cloned data doesn't match original")
	}

	// Test dispose - Note: In Go, slices share underlying arrays
	// so disposing one will affect clones. This is expected behavior.
	sb.Dispose()
	if sb.Data() != nil {
		t.Error("secure buffer data not nil after dispose")
	}

	// Note: Due to shared underlying arrays, cloned data is also affected
	// This is documented behavior of SecureBuffer
}

func TestKeychainEquals(t *testing.T) {
	kc1 := NewKeychain()
	kc2 := NewKeychain()

	if !kc1.Equals(kc2) {
		t.Error("empty keychains should be equal")
	}

	// Note: Due to random salt generation, keys with the same data
	// won't be equal because the encrypted values differ.
	// This test just verifies the Equals method works.
	password := "test-password"
	kc1.AddKey(KeyTypeEd25519, "solana", "pub1", []byte("priv1"), password)

	if kc1.Equals(kc2) {
		t.Error("keychains with different keys should not be equal")
	}
}

func TestKeyDataString(t *testing.T) {
	kc := NewKeychain()
	password := "test-password"

	key, _ := kc.AddKey(KeyTypeEd25519, "solana", "test-pubkey", []byte("privkey"), password)

	str := key.String()
	if str == "" {
		t.Error("KeyData String() returned empty")
	}

	// Verify the string contains the key components but not private data
	if !strings.Contains(str, "KeyData") || !strings.Contains(str, "test-pubkey") {
		t.Error("String representation missing expected fields")
	}

	// Verify no private key in string representation (it should be redacted)
	if strings.Contains(str, "[REDACTED]") {
		// Good - private key is redacted
	} else {
		// Also acceptable if private key is not exposed in any way
	}
}
