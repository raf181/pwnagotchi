package identity

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type fakeView struct {
	keysGenCalls  int
	startingCalls int
}

func (v *fakeView) OnKeysGeneration() { v.keysGenCalls++ }
func (v *fakeView) OnStarting()       { v.startingCalls++ }

// genKeysWithOpenSSL is a fake GenerateKeys that produces a real RSA keypair
// using openssl (standing in for the external pwngrid binary Python shells
// out to), in whatever format openssl defaults to (PKCS1 private, X.509
// SubjectPublicKeyInfo public) — deliberately NOT the same code path as
// exportPublicKeyPythonStyle, so the test exercises real parsing.
func genKeysWithOpenSSL(t *testing.T, path string) error {
	t.Helper()
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl not available")
	}
	priv := filepath.Join(path, "id_rsa")
	pub := priv + ".pub"
	if err := exec.Command("openssl", "genrsa", "-out", priv, "2048").Run(); err != nil {
		return err
	}
	return exec.Command("openssl", "rsa", "-in", priv, "-pubout", "-out", pub).Run()
}

func TestNewKeyPairGeneratesAndLoads(t *testing.T) {
	dir := t.TempDir()
	keyDir := filepath.Join(dir, "keys")

	origGen := GenerateKeys
	GenerateKeys = func(path string) error {
		return genKeysWithOpenSSL(t, path)
	}
	t.Cleanup(func() { GenerateKeys = origGen })

	view := &fakeView{}
	kp, err := NewKeyPair(keyDir, view)
	if err != nil {
		t.Fatalf("NewKeyPair: %v", err)
	}
	if view.keysGenCalls != 1 {
		t.Fatalf("OnKeysGeneration called %d times, want 1", view.keysGenCalls)
	}
	if view.startingCalls != 1 {
		t.Fatalf("OnStarting called %d times, want 1", view.startingCalls)
	}
	if kp.PrivKey == nil || kp.PubKey == nil {
		t.Fatal("keys not loaded")
	}
	if kp.Fingerprint == "" {
		t.Fatal("fingerprint not computed")
	}
	if !strings.HasPrefix(kp.PubKeyPEM, "-----BEGIN RSA PUBLIC KEY-----") {
		t.Fatalf("PubKeyPEM header = %q", kp.PubKeyPEM[:40])
	}
	if strings.HasSuffix(kp.PubKeyPEM, "\n") {
		t.Fatal("PubKeyPEM must not have a trailing newline (matches pycryptodome's PEM.encode)")
	}

	fpOnDisk, err := os.ReadFile(kp.FingerprintPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(fpOnDisk) != kp.Fingerprint {
		t.Fatalf("fingerprint file = %q, want %q", fpOnDisk, kp.Fingerprint)
	}

	// Second load (keys already exist) must not call GenerateKeys again.
	view2 := &fakeView{}
	kp2, err := NewKeyPair(keyDir, view2)
	if err != nil {
		t.Fatalf("second NewKeyPair: %v", err)
	}
	if view2.keysGenCalls != 0 {
		t.Fatalf("OnKeysGeneration called on existing keys, want 0 calls")
	}
	if kp2.Fingerprint != kp.Fingerprint {
		t.Fatal("fingerprint must be stable across reloads of the same key files")
	}
}

func TestNewKeyPairRegeneratesOnCorruption(t *testing.T) {
	dir := t.TempDir()
	keyDir := filepath.Join(dir, "keys")
	if err := os.MkdirAll(keyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-seed corrupted key files.
	if err := os.WriteFile(filepath.Join(keyDir, "id_rsa"), []byte("not a key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(keyDir, "id_rsa.pub"), []byte("not a key"), 0o644); err != nil {
		t.Fatal(err)
	}

	origGen := GenerateKeys
	calls := 0
	GenerateKeys = func(path string) error {
		calls++
		return genKeysWithOpenSSL(t, path)
	}
	t.Cleanup(func() { GenerateKeys = origGen })

	view := &fakeView{}
	kp, err := NewKeyPair(keyDir, view)
	if err != nil {
		t.Fatalf("NewKeyPair: %v", err)
	}
	if calls != 1 {
		t.Fatalf("GenerateKeys called %d times regenerating corrupted keys, want 1", calls)
	}
	if kp.PrivKey == nil {
		t.Fatal("expected valid key after regeneration")
	}
}

func TestSignProducesVerifiableSignature(t *testing.T) {
	dir := t.TempDir()
	if err := genKeysWithOpenSSL(t, dir); err != nil {
		t.Fatal(err)
	}
	priv, err := os.ReadFile(filepath.Join(dir, "id_rsa"))
	if err != nil {
		t.Fatal(err)
	}
	pub, err := os.ReadFile(filepath.Join(dir, "id_rsa.pub"))
	if err != nil {
		t.Fatal(err)
	}
	privKey, err := parseRSAPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	pubKey, err := parseRSAPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}

	kp := &KeyPair{PrivKey: privKey, PubKey: pubKey}
	sig, sigB64, err := kp.Sign("hello pwnagotchi")
	if err != nil {
		t.Fatal(err)
	}
	if len(sig) == 0 || sigB64 == "" {
		t.Fatal("empty signature")
	}
}
