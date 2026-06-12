package fetch

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// noteSigContext domain-separates the in-frontmatter note signature so it can never
// be replayed against the other node-key protocols (the announce handshake signs
// "nonce|new_url"; sync-verify signs "red-sync-verify-v1|nonce"; peer-resolve signs
// "red-peer-resolve-v1|…"). red-feather MUST sign the identical payload.
//
//	payload = "red-note-v1|" + red_author + "|" + red_author_name + "|" + red_signed_at + "|" + sha256(body)
//
// where body is FrontmatterBody(content). The signature, signer key, name, hash and
// timestamp all live in the note's YAML header, so a note is self-verifying and
// needs no signer.db sidecar.
const noteSigContext = "red-note-v1|"

// NoteVerification is the trust-independent result of checking a note's embedded
// signature. The caller layers the contributor keyring on top: State=="signed"
// becomes "verified" when SignerKey is recognized, else "unverified".
type NoteVerification struct {
	SignerKey  string // red_author (hex ed25519 pubkey), set when signed
	SignerName string // red_author_name, self-asserted display label (not trust)
	State      string // "unsigned" | "tampered" | "signed"
	Err        string // human-readable reason when not verified
}

// VerifyNote checks a note's embedded signature using only its frontmatter — no
// external signature store. It returns "unsigned" when there is no signature,
// "tampered" when the body hash or signature does not match, and "signed" (a valid
// signature by SignerKey) otherwise; trust is decided by the caller's keyring.
func VerifyNote(content []byte) NoteVerification {
	author := FrontmatterValue(content, "red_author")
	sig := FrontmatterValue(content, "red_sig")
	name := FrontmatterValue(content, "red_author_name")
	signedAt := FrontmatterValue(content, "red_signed_at")
	claimedHash := strings.ToLower(FrontmatterValue(content, "red_hash"))

	if author == "" || sig == "" {
		return NoteVerification{State: "unsigned", Err: "File has no signature"}
	}

	sum := sha256.Sum256(FrontmatterBody(content))
	bodyHash := hex.EncodeToString(sum[:])

	// red_hash is the signer's claimed body hash. A mismatch means the body changed
	// after signing — report that precisely before touching the signature.
	if claimedHash != "" && claimedHash != bodyHash {
		return NoteVerification{
			SignerKey: author, SignerName: name, State: "tampered",
			Err: "Hash mismatch: file content was modified after signing",
		}
	}

	pub, err1 := hex.DecodeString(author)
	sigBytes, err2 := hex.DecodeString(sig)
	if err1 != nil || err2 != nil || len(pub) != ed25519.PublicKeySize {
		return NoteVerification{
			SignerKey: author, SignerName: name, State: "tampered",
			Err: "Unverified: signature or key is malformed",
		}
	}

	payload := []byte(noteSigContext + author + "|" + name + "|" + signedAt + "|" + bodyHash)
	if !ed25519.Verify(ed25519.PublicKey(pub), payload, sigBytes) {
		return NoteVerification{
			SignerKey: author, SignerName: name, State: "tampered",
			Err: "Signature does not validate against the embedded key",
		}
	}
	return NoteVerification{SignerKey: author, SignerName: name, State: "signed"}
}
