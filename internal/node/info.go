package node

import (
	"errors"
	"strings"
)

// NodeInfo is the public metadata a node exposes.
type NodeInfo struct {
	NodeID          string   `json:"node_id"`          // derived from public key (first 16 chars or full)
	PublicKey       string   `json:"public_key"`       // hex encoded
	Name            string   `json:"name"`             // human readable, from config
	SoftwareVersion string   `json:"software_version"` // from build flag or config
	ExportedPaths   []string `json:"exported_paths"`   // paths this node exports (TODO: dynamic)
	Signature       string   `json:"signature"`        // ed25519 signature of all above fields (excluding signature itself)
}

// GetNodeInfo returns the node info structure, optionally signed.
func GetNodeInfo(nodeName, version string, exportedPaths []string) (*NodeInfo, error) {
	pubKey := GetNodePublicKey()
	if pubKey == "" {
		return nil, ErrNoIdentity
	}

	// Use short node ID = first 16 chars of public key (64 hex chars)
	nodeID := pubKey[:16]

	info := &NodeInfo{
		NodeID:          nodeID,
		PublicKey:       pubKey,
		Name:            nodeName,
		SoftwareVersion: version,
		ExportedPaths:   exportedPaths,
	}

	// Create canonical data to sign: concatenated fields (order matters)
	var builder strings.Builder
	builder.WriteString(info.NodeID)
	builder.WriteString(info.PublicKey)
	builder.WriteString(info.Name)
	builder.WriteString(info.SoftwareVersion)
	for _, p := range info.ExportedPaths {
		builder.WriteString(p)
	}
	dataToSign := []byte(builder.String())

	sig, err := SignNodeInfo(dataToSign)
	if err != nil {
		return nil, err
	}
	info.Signature = sig
	return info, nil
}

var ErrNoIdentity = errors.New("node identity not initialised")
