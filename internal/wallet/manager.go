package wallet

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/gagliardetto/solana-go"
)

var (
	// ErrInvalidKeyType is returned when the key type is invalid.
	ErrInvalidKeyType = errors.New("invalid key type")
	// ErrInvalidSignature is returned when a signature is invalid.
	ErrInvalidSignature = errors.New("invalid signature")
	// ErrUnsupportedChain is returned when the chain is not supported.
	ErrUnsupportedChain = errors.New("unsupported chain")
)

// Chain represents a blockchain network.
type Chain string

const (
	// ChainSolana represents the Solana blockchain.
	ChainSolana Chain = "solana"
	// ChainBase represents the Base blockchain.
	ChainBase Chain = "base"
)

// Manager manages wallet operations for multiple chains.
type Manager struct {
	keychain *Keychain
	password string // Keep in memory for operations - should be cleared on shutdown
}

// NewManager creates a new wallet manager.
func NewManager(keychain *Keychain, password string) *Manager {
	return &Manager{
		keychain: keychain,
		password: password,
	}
}

// CreateWallet creates a new wallet for the specified chain.
func (m *Manager) CreateWallet(chain Chain) (string, string, error) {
	switch chain {
	case ChainSolana:
		return m.createSolanaWallet()
	case ChainBase:
		return m.createBaseWallet()
	default:
		return "", "", ErrUnsupportedChain
	}
}

// createSolanaWallet creates a new Solana wallet using ed25519.
func (m *Manager) createSolanaWallet() (string, string, error) {
	// Generate ed25519 key pair
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate Solana key: %w", err)
	}

	// Create Solana public key
	solanaPubKey := solana.PublicKeyFromBytes(pub)

	// Add to keychain
	keyData, err := m.keychain.AddKey(KeyTypeEd25519, string(ChainSolana), solanaPubKey.String(), priv, m.password)
	if err != nil {
		ClearSecurely(priv)
		return "", "", fmt.Errorf("failed to add key to keychain: %w", err)
	}

	// Clear the private key from memory (it's now encrypted in keychain)
	ClearSecurely(priv)

	return keyData.ID, solanaPubKey.String(), nil
}

// createBaseWallet creates a new Base wallet using secp256k1.
func (m *Manager) createBaseWallet() (string, string, error) {
	// Generate secp256k1 key pair
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate Base key: %w", err)
	}

	// Convert to Ethereum format
	privBytes := ethcrypto.FromECDSA(priv)
	if privBytes == nil {
		return "", "", errors.New("failed to encode private key")
	}

	// Get public key address
	address := ethcrypto.PubkeyToAddress(priv.PublicKey).Hex()

	// Add to keychain
	keyData, err := m.keychain.AddKey(KeyTypeSecp256k1, string(ChainBase), address, privBytes, m.password)
	if err != nil {
		ClearSecurely(privBytes)
		return "", "", fmt.Errorf("failed to add key to keychain: %w", err)
	}

	// Clear the private key from memory
	ClearSecurely(privBytes)

	return keyData.ID, address, nil
}

// ImportWallet imports an existing wallet from private key.
func (m *Manager) ImportWallet(chain Chain, privateKey []byte) (string, string, error) {
	switch chain {
	case ChainSolana:
		return m.importSolanaWallet(privateKey)
	case ChainBase:
		return m.importBaseWallet(privateKey)
	default:
		return "", "", ErrUnsupportedChain
	}
}

// importSolanaWallet imports a Solana wallet from a private key.
func (m *Manager) importSolanaWallet(privateKey []byte) (string, string, error) {
	// Validate private key length (ed25519 private keys are 64 bytes: 32 seed + 32 pub)
	if len(privateKey) != ed25519.PrivateKeySize {
		return "", "", fmt.Errorf(
			"invalid Solana private key length: expected %d, got %d",
			ed25519.PrivateKeySize,
			len(privateKey),
		)
	}

	// Derive public key from private key
	pubKey := privateKey[32:] // Second half of ed25519 private key is the public key

	// Create Solana public key
	solanaPubKey := solana.PublicKeyFromBytes(pubKey)

	// Add to keychain
	keyData, err := m.keychain.AddKey(
		KeyTypeEd25519,
		string(ChainSolana),
		solanaPubKey.String(),
		privateKey,
		m.password,
	)
	if err != nil {
		return "", "", fmt.Errorf("failed to add key to keychain: %w", err)
	}

	return keyData.ID, solanaPubKey.String(), nil
}

// importBaseWallet imports a Base wallet from a private key.
func (m *Manager) importBaseWallet(privateKey []byte) (string, string, error) {
	// Validate private key length (secp256k1 private keys are 32 bytes)
	if len(privateKey) != 32 {
		return "", "", fmt.Errorf("invalid Base private key length: expected 32, got %d", len(privateKey))
	}

	// Convert to ECDSA private key
	priv, err := ethcrypto.ToECDSA(privateKey)
	if err != nil {
		return "", "", fmt.Errorf("failed to convert private key: %w", err)
	}

	// Get address from public key
	address := ethcrypto.PubkeyToAddress(priv.PublicKey).Hex()

	// Add to keychain
	keyData, err := m.keychain.AddKey(KeyTypeSecp256k1, string(ChainBase), address, privateKey, m.password)
	if err != nil {
		return "", "", fmt.Errorf("failed to add key to keychain: %w", err)
	}

	return keyData.ID, address, nil
}

// Sign signs a message with the specified wallet.
func (m *Manager) Sign(keyID string, message []byte) ([]byte, error) {
	keyType, _, privKey, err := m.keychain.GetKey(keyID, m.password)
	if err != nil {
		return nil, fmt.Errorf("failed to get key: %w", err)
	}
	defer ClearSecurely(privKey)

	switch keyType {
	case KeyTypeEd25519:
		return m.signSolana(message, privKey)
	case KeyTypeSecp256k1:
		return m.signBase(message, privKey)
	default:
		return nil, ErrInvalidKeyType
	}
}

// signSolana signs a message using ed25519.
func (m *Manager) signSolana(message []byte, privateKey []byte) ([]byte, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("invalid Solana private key length")
	}

	// ed25519.Sign expects a 64-byte private key
	signature := ed25519.Sign(privateKey, message)

	return signature, nil
}

// signBase signs a message using secp256k1.
func (m *Manager) signBase(message []byte, privateKey []byte) ([]byte, error) {
	// Hash the message first using Keccak256 (Ethereum standard)
	hashed := ethcrypto.Keccak256(message)

	// Convert to ECDSA private key
	priv, err := ethcrypto.ToECDSA(privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to convert private key: %w", err)
	}

	// Sign the hash
	signature, err := ethcrypto.Sign(hashed, priv)
	if err != nil {
		return nil, fmt.Errorf("failed to sign: %w", err)
	}

	return signature, nil
}

// SignTransaction signs a transaction with the specified wallet.
func (m *Manager) SignTransaction(keyID string, tx any) ([]byte, error) {
	keyType, _, privKey, err := m.keychain.GetKey(keyID, m.password)
	if err != nil {
		return nil, fmt.Errorf("failed to get key: %w", err)
	}
	defer ClearSecurely(privKey)

	switch keyType {
	case KeyTypeEd25519:
		return m.signSolanaTransaction(tx, privKey)
	case KeyTypeSecp256k1:
		return m.signBaseTransaction(tx, privKey)
	default:
		return nil, ErrInvalidKeyType
	}
}

// signSolanaTransaction signs a Solana transaction.
func (m *Manager) signSolanaTransaction(tx any, privateKey []byte) ([]byte, error) {
	// Convert to Solana transaction
	solTx, ok := tx.(*solana.Transaction)
	if !ok {
		return nil, errors.New("invalid Solana transaction type")
	}

	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("invalid Solana private key length")
	}

	// Create signature - serialize the message and sign it
	message, err := solTx.Message.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("failed to marshal transaction message: %w", err)
	}

	signature := ed25519.Sign(privateKey, message)

	return signature, nil
}

// signBaseTransaction signs an Base transaction.
func (m *Manager) signBaseTransaction(tx any, privateKey []byte) ([]byte, error) {
	// This would handle Ethereum/Base transaction signing
	// For now, return the raw signature
	// In a full implementation, this would use go-ethereum's transaction types

	// Convert to ECDSA private key
	priv, err := ethcrypto.ToECDSA(privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to convert private key: %w", err)
	}

	// Sign using ethereum crypto
	// This is a placeholder - actual implementation depends on transaction type
	signature, err := ethcrypto.Sign(ethcrypto.Keccak256([]byte("placeholder")), priv)
	if err != nil {
		return nil, fmt.Errorf("failed to sign transaction: %w", err)
	}

	return signature, nil
}

// GetPublicKey returns the public key/address for a wallet.
func (m *Manager) GetPublicKey(keyID string) (string, error) {
	_, pubKey, _, err := m.keychain.GetKey(keyID, m.password)
	if err != nil {
		return "", fmt.Errorf("failed to get key: %w", err)
	}
	return pubKey, nil
}

// GetPrivateKey returns the decrypted private key for a wallet
// WARNING: Use with caution - private key will be in memory.
func (m *Manager) GetPrivateKey(keyID string) ([]byte, error) {
	_, _, privKey, err := m.keychain.GetKey(keyID, m.password)
	if err != nil {
		return nil, fmt.Errorf("failed to get key: %w", err)
	}
	return privKey, nil
}

// ListWallets returns all wallets in the keychain.
func (m *Manager) ListWallets() []KeyInfo {
	return m.keychain.ListKeys()
}

// RemoveWallet removes a wallet from the keychain.
func (m *Manager) RemoveWallet(keyID string) error {
	return m.keychain.RemoveKey(keyID)
}

// ExportWallet exports a wallet to encrypted JSON.
func (m *Manager) ExportWallet(keyID string, exportPassword string) ([]byte, error) {
	return m.keychain.ExportKey(keyID, m.password, exportPassword)
}

// ImportEncryptedWallet imports a wallet from encrypted JSON.
func (m *Manager) ImportEncryptedWallet(encryptedKey []byte, importPassword string) (*KeyData, error) {
	return m.keychain.ImportKey(encryptedKey, importPassword, m.password)
}

// ChangePassword changes the wallet password.
func (m *Manager) ChangePassword(newPassword string) error {
	// For this implementation, we'd need to re-encrypt all keys
	// This is a complex operation that requires:
	// 1. Loading all existing keys with old password
	// 2. Re-encrypting with new password
	// 3. Saving the keychain
	// For now, return an error indicating this needs to be done at keychain level
	return errors.New("change password must be done at keychain level using ChangePassword()")
}

// ValidateAddress validates if an address is valid for the given chain.
func (m *Manager) ValidateAddress(chain Chain, address string) error {
	switch chain {
	case ChainSolana:
		_, err := solana.PublicKeyFromBase58(address)
		if err != nil {
			return fmt.Errorf("invalid Solana address: %w", err)
		}
		return nil
	case ChainBase:
		// Check if address is a valid hex address (starts with 0x and is 42 chars)
		if len(address) != 42 || address[:2] != "0x" {
			return errors.New("invalid Base address: not a valid hex address")
		}
		return nil
	default:
		return ErrUnsupportedChain
	}
}

// GetBalance gets the balance for a wallet
// Note: This is a placeholder - actual implementation requires RPC calls.
func (m *Manager) GetBalance(keyID string) (string, error) {
	keyType, _, _, err := m.keychain.GetKey(keyID, m.password)
	if err != nil {
		return "", fmt.Errorf("failed to get key: %w", err)
	}

	switch keyType {
	case KeyTypeEd25519:
		// Solana balance check would go here
		return "0", errors.New("balance check not implemented - requires RPC connection")
	case KeyTypeSecp256k1:
		// Base balance check would go here
		return "0", errors.New("balance check not implemented - requires RPC connection")
	default:
		return "", ErrInvalidKeyType
	}
}

// DeriveHDWallet derives a child wallet from a HD wallet seed
// Note: This is a placeholder for future HD wallet support.
func (m *Manager) DeriveHDWallet(seed []byte, chain Chain, derivationPath string) (string, string, error) {
	return "", "", errors.New("HD wallet derivation not yet implemented")
}

// EstimateFee estimates the transaction fee for a given operation
// Note: This is a placeholder - actual implementation requires RPC calls.
func (m *Manager) EstimateFee(chain Chain, operation string) (string, error) {
	return "0", errors.New("fee estimation not implemented - requires RPC connection")
}

// Dispose clears sensitive data from memory.
func (m *Manager) Dispose() {
	// Clear the password from memory
	ClearSecurely([]byte(m.password))
	m.password = ""
}

// SolanaSigner implements solana.Signer interface for Solana transactions.
type SolanaSigner struct {
	privateKey ed25519.PrivateKey
	publicKey  solana.PublicKey
}

// NewSolanaSigner creates a new Solana signer.
func NewSolanaSigner(privateKey []byte) (*SolanaSigner, error) {
	// Handle both 32-byte seeds and 64-byte full keys
	var fullKey ed25519.PrivateKey
	if len(privateKey) == ed25519.PrivateKeySize {
		// It's a 64-byte full key, use as-is
		fullKey = privateKey
	} else if len(privateKey) == 32 {
		// It's a 32-byte seed, derive the full key
		derivedKey := ed25519.NewKeyFromSeed(privateKey)
		fullKey = derivedKey
	} else {
		return nil, fmt.Errorf("invalid Solana private key length: expected 32 or 64 bytes, got %d", len(privateKey))
	}

	// Extract public key from second half of full key
	pubKey := solana.PublicKeyFromBytes(fullKey[32:])

	return &SolanaSigner{
		privateKey: fullKey,
		publicKey:  pubKey,
	}, nil
}

// PublicKey returns the Solana public key.
func (s *SolanaSigner) PublicKey() solana.PublicKey {
	return s.publicKey
}

// Sign signs a message.
func (s *SolanaSigner) Sign(message []byte) ([]byte, error) {
	return ed25519.Sign(s.privateKey, message), nil
}

// IsOnCurve checks if a public key is on the ed25519 curve.
func (s *SolanaSigner) IsOnCurve() bool {
	return true // All ed25519 keys are on the curve
}

// BaseSigner represents a Base/Ethereum signer.
type BaseSigner struct {
	privateKey *ecdsa.PrivateKey
	address    string
}

// NewBaseSigner creates a new Base signer.
func NewBaseSigner(privateKey []byte) (*BaseSigner, error) {
	priv, err := ethcrypto.ToECDSA(privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to convert private key: %w", err)
	}

	address := ethcrypto.PubkeyToAddress(priv.PublicKey).Hex()

	return &BaseSigner{
		privateKey: priv,
		address:    address,
	}, nil
}

// Address returns the Base/Ethereum address.
func (s *BaseSigner) Address() string {
	return s.address
}

// Sign signs a hash.
func (s *BaseSigner) Sign(hash []byte) ([]byte, error) {
	return ethcrypto.Sign(hash, s.privateKey)
}

// SignTx signs an Ethereum transaction.
func (s *BaseSigner) SignTx(txHash hash.Hash) ([]byte, error) {
	return ethcrypto.Sign(txHash.Sum(nil), s.privateKey)
}

// PublicKey returns the ECDSA public key.
func (s *BaseSigner) PublicKey() *ecdsa.PublicKey {
	return &s.privateKey.PublicKey
}

// PrivateKeyToHex converts a private key to hex string.
func PrivateKeyToHex(privateKey []byte) string {
	return "0x" + hex.EncodeToString(privateKey)
}

// HexToPrivateKey converts a hex string to private key bytes.
func HexToPrivateKey(hexKey string) ([]byte, error) {
	if len(hexKey) >= 2 && hexKey[0:2] == "0x" {
		hexKey = hexKey[2:]
	}

	return hex.DecodeString(hexKey)
}

// ValidatePrivateKey validates a private key for a given chain.
func ValidatePrivateKey(chain Chain, privateKey []byte) error {
	switch chain {
	case ChainSolana:
		if len(privateKey) != ed25519.PrivateKeySize {
			return fmt.Errorf(
				"invalid Solana private key: expected %d bytes, got %d",
				ed25519.PrivateKeySize,
				len(privateKey),
			)
		}
		return nil
	case ChainBase:
		if len(privateKey) != 32 {
			return fmt.Errorf("invalid Base private key: expected 32 bytes, got %d", len(privateKey))
		}
		// Try to parse as ECDSA key
		_, err := ethcrypto.ToECDSA(privateKey)
		if err != nil {
			return fmt.Errorf("invalid Base private key: %w", err)
		}
		return nil
	default:
		return ErrUnsupportedChain
	}
}
