//go:build ignore

// resolve_probe.go — a standalone client for exercising /-/peer/resolve security
// edge cases against a live node (Phase 5 of the federation test). Std-lib only.
//
//	go run scripts/resolve_probe.go unknown <baseURL> <targetKeyHex>
//	go run scripts/resolve_probe.go badsig  <baseURL> <targetKeyHex> <privKeyFile>
//	go run scripts/resolve_probe.go valid   <baseURL> <targetKeyHex> <privKeyFile>
//
// Exit code 0 means the observed behaviour matched the expectation for the mode.
package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const ctx = "red-peer-resolve-v1|"

type req struct {
	RequestorKey string `json:"requestor_key"`
	TargetKey    string `json:"target_key"`
	Nonce        string `json:"nonce"`
	Signature    string `json:"signature"`
}
type resp struct {
	TargetKey string `json:"target_key"`
	URL       string `json:"url"`
	PublicKey string `json:"public_key"`
	Signature string `json:"signature"`
}

func die(f string, a ...any) { fmt.Fprintf(os.Stderr, f+"\n", a...); os.Exit(2) }

func loadPriv(path string) (ed25519.PrivateKey, string) {
	b, err := os.ReadFile(path)
	if err != nil {
		die("read priv %s: %v", path, err)
	}
	if len(b) != ed25519.PrivateKeySize {
		die("priv %s has size %d, want %d", path, len(b), ed25519.PrivateKeySize)
	}
	pk := ed25519.PrivateKey(b)
	return pk, hex.EncodeToString(pk.Public().(ed25519.PublicKey))
}

func main() {
	if len(os.Args) < 4 {
		die("usage: resolve_probe <unknown|badsig|valid> <baseURL> <targetKey> [privFile]")
	}
	mode, base, target := os.Args[1], strings.TrimSuffix(os.Args[2], "/"), os.Args[3]

	nb := make([]byte, 32)
	rand.Read(nb)
	nonce := hex.EncodeToString(nb)

	var reqKey, sigHex string
	switch mode {
	case "unknown":
		pub, priv, _ := ed25519.GenerateKey(rand.Reader) // never registered
		reqKey = hex.EncodeToString(pub)
		sigHex = hex.EncodeToString(ed25519.Sign(priv, []byte(ctx+nonce+"|"+target)))
	case "badsig":
		if len(os.Args) < 5 {
			die("badsig needs <privFile>")
		}
		_, reqKey = loadPriv(os.Args[4])
		bad := make([]byte, ed25519.SignatureSize) // all-zero, invalid
		sigHex = hex.EncodeToString(bad)
	case "valid":
		if len(os.Args) < 5 {
			die("valid needs <privFile>")
		}
		var priv ed25519.PrivateKey
		priv, reqKey = loadPriv(os.Args[4])
		sigHex = hex.EncodeToString(ed25519.Sign(priv, []byte(ctx+nonce+"|"+target)))
	default:
		die("unknown mode %q", mode)
	}

	body, _ := json.Marshal(req{RequestorKey: reqKey, TargetKey: target, Nonce: nonce, Signature: sigHex})
	client := &http.Client{Timeout: 10 * time.Second}
	r, err := client.Post(base+"/-/peer/resolve", "application/json", bytes.NewReader(body))
	if err != nil {
		die("POST failed: %v", err)
	}
	defer r.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 8192))
	fmt.Printf("mode=%s HTTP=%d body=%s\n", mode, r.StatusCode, strings.TrimSpace(string(raw)))

	switch mode {
	case "unknown":
		if r.StatusCode == http.StatusNotFound {
			fmt.Println("PASS: unknown requestor rejected (404), no url disclosed")
			os.Exit(0)
		}
		die("FAIL: expected 404 for unknown requestor, got %d", r.StatusCode)
	case "badsig":
		if r.StatusCode == http.StatusForbidden {
			fmt.Println("PASS: forged signature rejected (403)")
			os.Exit(0)
		}
		die("FAIL: expected 403 for bad signature, got %d", r.StatusCode)
	case "valid":
		if r.StatusCode != http.StatusOK {
			die("FAIL: expected 200 for valid request, got %d", r.StatusCode)
		}
		var rr resp
		if err := json.Unmarshal(raw, &rr); err != nil {
			die("FAIL: decode: %v", err)
		}
		pub, err := hex.DecodeString(rr.PublicKey)
		if err != nil || len(pub) != ed25519.PublicKeySize {
			die("FAIL: bad directory key in response")
		}
		sig, _ := hex.DecodeString(rr.Signature)
		want := []byte(ctx + nonce + "|" + target + "|" + rr.URL)
		if !ed25519.Verify(ed25519.PublicKey(pub), want, sig) {
			die("FAIL: directory signature did not verify")
		}
		fmt.Printf("PASS: valid lookup → url=%s (signature verified against directory key %s…)\n", rr.URL, rr.PublicKey[:16])
		os.Exit(0)
	}
}
