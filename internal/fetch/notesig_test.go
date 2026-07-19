package fetch

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// buildSignedNote produces a note whose frontmatter carries a valid signature over
// the canonical payload. It is the reference implementation of the scheme that
// red-feather must match: sig = ed25519(noteSigContext|author|name|signedAt|sha256(body)).
func buildSignedNote(t *testing.T, priv ed25519.PrivateKey, author, name, signedAt, body string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(body))
	bodyHash := hex.EncodeToString(sum[:])
	payload := noteSigContext + author + "|" + name + "|" + signedAt + "|" + bodyHash
	sig := hex.EncodeToString(ed25519.Sign(priv, []byte(payload)))
	fm := strings.Join([]string{
		"red_author: " + author,
		"red_author_name: " + name,
		"red_signed_at: " + signedAt,
		"red_hash: " + bodyHash,
		"red_sig: " + sig,
	}, "\n")
	return "---\n" + fm + "\n---\n" + body
}

func TestVerifyNote_Valid(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	author := hex.EncodeToString(pub)
	note := buildSignedNote(t, priv, author, "Alice Example", "1700000000", "# Title\n\nbody text\n")

	v := VerifyNote([]byte(note))
	if v.State != "signed" {
		t.Fatalf("state: want signed, got %q (%s)", v.State, v.Err)
	}
	if v.SignerKey != author {
		t.Fatalf("signer key: want %s, got %s", author, v.SignerKey)
	}
	if v.SignerName != "Alice Example" {
		t.Fatalf("signer name: want %q, got %q", "Alice Example", v.SignerName)
	}
}

func TestVerifyNote_Unsigned(t *testing.T) {
	v := VerifyNote([]byte("---\nred_tags: [\"X\"]\n---\n# No signature\n"))
	if v.State != "unsigned" {
		t.Fatalf("want unsigned, got %q", v.State)
	}
	v2 := VerifyNote([]byte("# Plain note, no frontmatter\n"))
	if v2.State != "unsigned" {
		t.Fatalf("want unsigned for no-frontmatter, got %q", v2.State)
	}
}

func TestVerifyNote_TamperedBody(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	author := hex.EncodeToString(pub)
	note := buildSignedNote(t, priv, author, "Alice", "1700000000", "original body\n")
	// Flip the body after signing — red_hash no longer matches.
	tampered := strings.Replace(note, "original body", "MALICIOUS body", 1)
	if tampered == note {
		t.Fatal("test setup: body not modified")
	}
	v := VerifyNote([]byte(tampered))
	if v.State != "tampered" {
		t.Fatalf("want tampered, got %q (%s)", v.State, v.Err)
	}
}

func TestVerifyNote_ForgedSignature(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	author := hex.EncodeToString(pub)
	note := buildSignedNote(t, priv, author, "Alice", "1700000000", "body\n")
	// Corrupt the signature hex (keep red_hash consistent so we exercise the sig
	// check, not the hash check).
	good := "red_sig: " + hex.EncodeToString(ed25519.Sign(priv, []byte("x")))
	lines := strings.Split(note, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "red_sig: ") {
			lines[i] = good
		}
	}
	v := VerifyNote([]byte(strings.Join(lines, "\n")))
	if v.State != "tampered" {
		t.Fatalf("want tampered for a non-matching signature, got %q (%s)", v.State, v.Err)
	}
}

func TestVerifyNote_BodySplitMatchesFrontmatterBody(t *testing.T) {
	// Guards that VerifyNote hashes exactly FrontmatterBody — a body containing a
	// later '---' must still verify (only the first closing delimiter is the split).
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	author := hex.EncodeToString(pub)
	body := "intro\n\n---\n\nafter a horizontal rule\n"
	note := buildSignedNote(t, priv, author, "Bob", "1700000001", body)
	if got := string(FrontmatterBody([]byte(note))); got != body {
		t.Fatalf("FrontmatterBody mismatch:\nwant %q\ngot  %q", body, got)
	}
	if v := VerifyNote([]byte(note)); v.State != "signed" {
		t.Fatalf("want signed, got %q (%s)", v.State, v.Err)
	}
}
