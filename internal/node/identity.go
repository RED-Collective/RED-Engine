package node

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

var (
	once    sync.Once
	nodeKey ed25519.PrivateKey
	nodePub ed25519.PublicKey
	keyPath string
)

// InitNodeIdentity loads or generates the node's Ed25519 key pair.
// Keys are stored in ~/.red-engine/node.key (private, 0600) and node.pub (0644).
func InitNodeIdentity() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot get home dir: %w", err)
	}
	keyDir := filepath.Join(home, ".red-engine")
	if err := os.MkdirAll(keyDir, 0700); err != nil {
		return fmt.Errorf("cannot create key dir: %w", err)
	}
	privPath := filepath.Join(keyDir, "node.key")
	pubPath := filepath.Join(keyDir, "node.pub")

	once.Do(func() {
		// Try to read existing private key
		privData, err := os.ReadFile(privPath)
		if err == nil && len(privData) == ed25519.PrivateKeySize {
			nodeKey = ed25519.PrivateKey(privData)
			nodePub = nodeKey.Public().(ed25519.PublicKey)
			keyPath = privPath
			return
		}

		// Generate new key pair
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			panic(fmt.Errorf("failed to generate node key: %w", err))
		}
		nodeKey = priv
		nodePub = pub

		// Save private key (600)
		if err := os.WriteFile(privPath, nodeKey, 0600); err != nil {
			panic(fmt.Errorf("failed to save node private key: %w", err))
		}
		// Save public key (644)
		if err := os.WriteFile(pubPath, []byte(hex.EncodeToString(nodePub)), 0644); err != nil {
			panic(fmt.Errorf("failed to save node public key: %w", err))
		}
		keyPath = privPath
	})

	return nil
}

// GetNodePublicKey returns the hex-encoded public key of this node.
func GetNodePublicKey() string {
	return hex.EncodeToString(nodePub)
}

// SignNodeInfo signs the given data with the node's private key.
// data should be a JSON string or a concatenation of fields.
func SignNodeInfo(data []byte) (string, error) {
	if nodeKey == nil {
		return "", errors.New("node identity not initialised")
	}
	sig := ed25519.Sign(nodeKey, data)
	return hex.EncodeToString(sig), nil
}
