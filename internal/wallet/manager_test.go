package wallet

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"testing"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/gagliardetto/solana-go"
)

func TestNewManager(t *testing.T) {
	kc := NewKeychain()
	password := "test-password"

	mgr := NewManager(kc, password)
	if mgr == nil {
		t.Fatal("NewManager returned nil")
	}
	if mgr.keychain != kc {
		t.Error("keychain not set correctly")
	}
	if mgr.password != password {
		t.Error("password not set correctly")
	}
}

func TestCreateSolanaWallet(t *testing.T) {
	kc := NewKeychain()
	password := "test-password"
	mgr := NewManager(kc, password)

	keyID, address, err := mgr.CreateWallet(ChainSolana)
	if err != nil {
		t.Fatalf("CreateWallet failed: %v", err)
	}

	if keyID == "" {
		t.Error("key ID is empty")
	}
	if address == "" {
		t.Error("address is empty")
	}

	// Verify address is valid Solana address
	pubKey, err := solana.PublicKeyFromBase58(address)
	if err != nil {
		t.Errorf("invalid Solana address: %v", err)
	}

	// Verify key was added to keychain
	keys := kc.ListKeys()
	if len(keys) != 1 {
		t.Errorf("expected 1 key, got %d", len(keys))
	}

	if keys[0].ID != keyID {
		t.Error("key ID mismatch")
	}
	if keys[0].Type != KeyTypeEd25519 {
		t.Errorf("expected key type %s, got %s", KeyTypeEd25519, keys[0].Type)
	}
	if keys[0].Chain != string(ChainSolana) {
		t.Errorf("expected chain solana, got %s", keys[0].Chain)
	}

	// Verify we can retrieve the key
	pubKeyRetrieved, err := mgr.GetPublicKey(keyID)
	if err != nil {
		t.Fatalf("GetPublicKey failed: %v", err)
	}
	if pubKeyRetrieved != address {
		t.Errorf("address mismatch: expected %s, got %s", address, pubKeyRetrieved)
	}

	// Verify the address matches the derived public key
	if pubKey.String() != address {
		t.Errorf("derived address doesn't match: expected %s, got %s", address, pubKey.String())
	}
}

func TestCreateBaseWallet(t *testing.T) {
	kc := NewKeychain()
	password := "test-password"
	mgr := NewManager(kc, password)

	keyID, address, err := mgr.CreateWallet(ChainBase)
	if err != nil {
		t.Fatalf("CreateWallet failed: %v", err)
	}

	if keyID == "" {
		t.Error("key ID is empty")
	}
	if address == "" {
		t.Error("address is empty")
	}

	// Verify address has 0x prefix
	if len(address) < 2 || address[:2] != "0x" {
		t.Error("Base address should start with 0x")
	}

	// Verify key was added to keychain
	keys := kc.ListKeys()
	if len(keys) != 1 {
		t.Errorf("expected 1 key, got %d", len(keys))
	}

	if keys[0].Type != KeyTypeSecp256k1 {
		t.Errorf("expected key type %s, got %s", KeyTypeSecp256k1, keys[0].Type)
	}
	if keys[0].Chain != string(ChainBase) {
		t.Errorf("expected chain base, got %s", keys[0].Chain)
	}
}

func TestCreateWalletUnsupportedChain(t *testing.T) {
	kc := NewKeychain()
	password := "test-password"
	mgr := NewManager(kc, password)

	_, _, err := mgr.CreateWallet(Chain("unsupported"))
	if !errors.Is(err, ErrUnsupportedChain) {
		t.Errorf("expected ErrUnsupportedChain, got %v", err)
	}
}

func TestImportSolanaWallet(t *testing.T) {
	kc := NewKeychain()
	password := "test-password"
	mgr := NewManager(kc, password)

	// Generate a valid ed25519 key pair
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	solanaPubKey := solana.PublicKeyFromBytes(pub)
	expectedAddress := solanaPubKey.String()

	keyID, address, err := mgr.ImportWallet(ChainSolana, priv)
	if err != nil {
		t.Fatalf("ImportWallet failed: %v", err)
	}

	if address != expectedAddress {
		t.Errorf("address mismatch: expected %s, got %s", expectedAddress, address)
	}

	// Verify we can retrieve and use the key
	retrievedPubKey, err := mgr.GetPublicKey(keyID)
	if err != nil {
		t.Fatalf("GetPublicKey failed: %v", err)
	}

	if retrievedPubKey != expectedAddress {
		t.Errorf("retrieved address mismatch: expected %s, got %s", expectedAddress, retrievedPubKey)
	}

	ClearSecurely(priv)
}

func TestImportSolanaWalletInvalidLength(t *testing.T) {
	kc := NewKeychain()
	password := "test-password"
	mgr := NewManager(kc, password)

	invalidKey := []byte("too-short")

	_, _, err := mgr.ImportWallet(ChainSolana, invalidKey)
	if err == nil {
		t.Error("expected error for invalid key length")
	}
}

func TestImportBaseWallet(t *testing.T) {
	kc := NewKeychain()
	password := "test-password"
	mgr := NewManager(kc, password)

	// Generate a valid secp256k1 key pair
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	privBytes := ethcrypto.FromECDSA(priv)

	keyID, address, err := mgr.ImportWallet(ChainBase, privBytes)
	if err != nil {
		t.Fatalf("ImportWallet failed: %v", err)
	}

	// Verify address is valid (starts with 0x and is 42 chars)
	if len(address) != 42 || address[:2] != "0x" {
		t.Errorf("invalid address format: %s", address)
	}

	// Verify we can retrieve the key
	retrievedPubKey, err := mgr.GetPublicKey(keyID)
	if err != nil {
		t.Fatalf("GetPublicKey failed: %v", err)
	}

	if retrievedPubKey != address {
		t.Errorf("retrieved address mismatch: expected %s, got %s", address, retrievedPubKey)
	}

	ClearSecurely(privBytes)
}

func TestImportBaseWalletInvalidLength(t *testing.T) {
	kc := NewKeychain()
	password := "test-password"
	mgr := NewManager(kc, password)

	invalidKey := []byte("not-32-bytes!!!")

	_, _, err := mgr.ImportWallet(ChainBase, invalidKey)
	if err == nil {
		t.Error("expected error for invalid key length")
	}
}

func TestSignSolana(t *testing.T) {
	kc := NewKeychain()
	password := "test-password"
	mgr := NewManager(kc, password)

	// Create wallet
	keyID, _, err := mgr.CreateWallet(ChainSolana)
	if err != nil {
		t.Fatalf("CreateWallet failed: %v", err)
	}

	message := []byte("test message to sign")

	// Sign message
	signature, err := mgr.Sign(keyID, message)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	// ed25519 signatures are 64 bytes
	if len(signature) != ed25519.SignatureSize {
		t.Errorf("expected signature size %d, got %d", ed25519.SignatureSize, len(signature))
	}

	// Verify signature
	_, pubKey, privKey, err := kc.GetKey(keyID, password)
	if err != nil {
		t.Fatalf("GetKey failed: %v", err)
	}
	defer ClearSecurely(privKey)

	// Convert string pubKey back to bytes for verification
	// The pubKey from GetKey is the Solana base58 string, we need the actual bytes
	solanaPubKey, err := solana.PublicKeyFromBase58(pubKey)
	if err != nil {
		t.Fatalf("failed to parse public key: %v", err)
	}

	// Verify signature using ed25519
	if !ed25519.Verify(solanaPubKey.Bytes(), message, signature) {
		t.Error("signature verification failed")
	}
}

func TestSignBase(t *testing.T) {
	kc := NewKeychain()
	password := "test-password"
	mgr := NewManager(kc, password)

	// Create wallet
	keyID, _, err := mgr.CreateWallet(ChainBase)
	if err != nil {
		t.Fatalf("CreateWallet failed: %v", err)
	}

	message := []byte("test message to sign")

	// Sign message
	signature, err := mgr.Sign(keyID, message)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	// Ethereum signatures are 65 bytes (r + s + v)
	if len(signature) != 65 {
		t.Errorf("expected signature size 65, got %d", len(signature))
	}

	// Note: Full signature verification would require reconstructing the hash
	// and using ecdsa.Verify. For this test, we just check the signature is generated.
}

func TestSignInvalidKeyID(t *testing.T) {
	kc := NewKeychain()
	password := "test-password"
	mgr := NewManager(kc, password)

	_, err := mgr.Sign("invalid-key-id", []byte("message"))
	if err == nil {
		t.Error("expected error for invalid key ID")
	}
	// The error is wrapped, so just check it's not nil
}

func TestGetPublicKey(t *testing.T) {
	kc := NewKeychain()
	password := "test-password"
	mgr := NewManager(kc, password)

	keyID, expectedAddress, err := mgr.CreateWallet(ChainSolana)
	if err != nil {
		t.Fatalf("CreateWallet failed: %v", err)
	}

	pubKey, err := mgr.GetPublicKey(keyID)
	if err != nil {
		t.Fatalf("GetPublicKey failed: %v", err)
	}

	if pubKey != expectedAddress {
		t.Errorf("address mismatch: expected %s, got %s", expectedAddress, pubKey)
	}
}

func TestGetPrivateKey(t *testing.T) {
	kc := NewKeychain()
	password := "test-password"
	mgr := NewManager(kc, password)

	keyID, _, err := mgr.CreateWallet(ChainSolana)
	if err != nil {
		t.Fatalf("CreateWallet failed: %v", err)
	}

	privKey, err := mgr.GetPrivateKey(keyID)
	if err != nil {
		t.Fatalf("GetPrivateKey failed: %v", err)
	}

	if len(privKey) != ed25519.PrivateKeySize {
		t.Errorf("expected private key size %d, got %d", ed25519.PrivateKeySize, len(privKey))
	}

	ClearSecurely(privKey)
}

func TestListWallets(t *testing.T) {
	kc := NewKeychain()
	password := "test-password"
	mgr := NewManager(kc, password)

	// Create multiple wallets
	keyID1, _, _ := mgr.CreateWallet(ChainSolana)
	keyID2, _, _ := mgr.CreateWallet(ChainBase)

	wallets := mgr.ListWallets()
	if len(wallets) != 2 {
		t.Errorf("expected 2 wallets, got %d", len(wallets))
	}

	// Verify wallet IDs
	found := make(map[string]bool)
	for _, w := range wallets {
		found[w.ID] = true
	}

	if !found[keyID1] || !found[keyID2] {
		t.Error("not all wallet IDs were returned")
	}
}

func TestRemoveWallet(t *testing.T) {
	kc := NewKeychain()
	password := "test-password"
	mgr := NewManager(kc, password)

	keyID, _, _ := mgr.CreateWallet(ChainSolana)

	err := mgr.RemoveWallet(keyID)
	if err != nil {
		t.Fatalf("RemoveWallet failed: %v", err)
	}

	// Verify wallet was removed
	wallets := mgr.ListWallets()
	if len(wallets) != 0 {
		t.Errorf("expected 0 wallets, got %d", len(wallets))
	}
}

func TestValidateAddressSolana(t *testing.T) {
	kc := NewKeychain()
	password := "test-password"
	mgr := NewManager(kc, password)

	// Create a wallet to get a valid address
	_, validAddress, _ := mgr.CreateWallet(ChainSolana)

	// Test valid address
	err := mgr.ValidateAddress(ChainSolana, validAddress)
	if err != nil {
		t.Errorf("failed to validate valid Solana address: %v", err)
	}

	// Test invalid address
	err = mgr.ValidateAddress(ChainSolana, "invalid-address")
	if err == nil {
		t.Error("expected error for invalid Solana address")
	}
}

func TestValidateAddressBase(t *testing.T) {
	kc := NewKeychain()
	password := "test-password"
	mgr := NewManager(kc, password)

	// Create a wallet to get a valid address
	_, validAddress, _ := mgr.CreateWallet(ChainBase)

	// Test valid address
	err := mgr.ValidateAddress(ChainBase, validAddress)
	if err != nil {
		t.Errorf("failed to validate valid Base address: %v", err)
	}

	// Test invalid address
	err = mgr.ValidateAddress(ChainBase, "not-a-valid-address")
	if err == nil {
		t.Error("expected error for invalid Base address")
	}
}

func TestValidatePrivateKeySolana(t *testing.T) {
	validKey := make([]byte, ed25519.PrivateKeySize)
	err := ValidatePrivateKey(ChainSolana, validKey)
	if err != nil {
		t.Errorf("failed to validate valid Solana key: %v", err)
	}

	invalidKey := []byte("too-short")
	err = ValidatePrivateKey(ChainSolana, invalidKey)
	if err == nil {
		t.Error("expected error for invalid Solana key length")
	}
}

func TestValidatePrivateKeyBase(t *testing.T) {
	// Generate a valid secp256k1 key
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}
	validKey := ethcrypto.FromECDSA(priv)

	err = ValidatePrivateKey(ChainBase, validKey)
	if err != nil {
		t.Errorf("failed to validate valid Base key: %v", err)
	}

	invalidKey := []byte("not-32-bytes-long-at-all!!")
	err = ValidatePrivateKey(ChainBase, invalidKey)
	if err == nil {
		t.Error("expected error for invalid Base key length")
	}
}

func TestPrivateKeyToHexAndHexToPrivateKey(t *testing.T) {
	originalKey := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}

	hex := PrivateKeyToHex(originalKey)
	if hex[:2] != "0x" {
		t.Error("hex string should start with 0x")
	}

	decoded, err := HexToPrivateKey(hex)
	if err != nil {
		t.Fatalf("HexToPrivateKey failed: %v", err)
	}

	if !bytes.Equal(decoded, originalKey) {
		t.Error("decoded key doesn't match original")
	}
}

func TestSolanaSigner(t *testing.T) {
	priv, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	signer, err := NewSolanaSigner(priv)
	if err != nil {
		t.Fatalf("NewSolanaSigner failed: %v", err)
	}

	message := []byte("test message")
	signature, err := signer.Sign(message)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	if len(signature) != ed25519.SignatureSize {
		t.Errorf("expected signature size %d, got %d", ed25519.SignatureSize, len(signature))
	}

	if !signer.IsOnCurve() {
		t.Error("expected IsOnCurve to return true")
	}

	pubKey := signer.PublicKey()
	if len(pubKey.Bytes()) != solana.PublicKeyLength {
		t.Errorf("expected public key length %d, got %d", solana.PublicKeyLength, len(pubKey.Bytes()))
	}

	ClearSecurely(priv)
}

func TestSolanaSignerInvalidKey(t *testing.T) {
	invalidKey := []byte("too-short")

	_, err := NewSolanaSigner(invalidKey)
	if err == nil {
		t.Error("expected error for invalid key length")
	}
}

func TestBaseSigner(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	privBytes := ethcrypto.FromECDSA(priv)
	signer, err := NewBaseSigner(privBytes)
	if err != nil {
		t.Fatalf("NewBaseSigner failed: %v", err)
	}

	if signer.Address() == "" {
		t.Error("address is empty")
	}

	// Verify address format (starts with 0x and is 42 chars)
	if len(signer.Address()) != 42 || signer.Address()[:2] != "0x" {
		t.Errorf("invalid address format: %s", signer.Address())
	}

	hash := ethcrypto.Keccak256([]byte("test"))
	signature, err := signer.Sign(hash)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	if len(signature) != 65 {
		t.Errorf("expected signature length 65, got %d", len(signature))
	}

	pubKey := signer.PublicKey()
	if pubKey == nil {
		t.Error("public key is nil")
	}

	ClearSecurely(privBytes)
}

func TestBaseSignerInvalidKey(t *testing.T) {
	invalidKey := []byte("not-32-bytes")

	_, err := NewBaseSigner(invalidKey)
	if err == nil {
		t.Error("expected error for invalid key")
	}
}

func TestManagerDispose(t *testing.T) {
	kc := NewKeychain()
	password := "sensitive-password"
	mgr := NewManager(kc, password)

	mgr.Dispose()
	if mgr.password != "" {
		t.Error("password not cleared after dispose")
	}
}
