//go:build compatibility

package tests

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jayofelony/pwnagotchi/go-port/internal/identity"
)

// TestCompatFingerprintMatchesPython generates a real RSA key with
// pycryptodome (standing in for pwngrid), computes identity.py's
// PubKeyPEM/Fingerprint in Python, and checks the Go port produces the
// IDENTICAL fingerprint from the same on-disk key files — including the
// "RSA PUBLIC KEY" header/footer swap over an X.509 SubjectPublicKeyInfo
// body, and pycryptodome's no-trailing-newline PEM quirk (see
// docs/known-differences.md).
func TestCompatFingerprintMatchesPython(t *testing.T) {
	python := pythonBin(t)
	dir := t.TempDir()

	script := `
from Crypto.PublicKey import RSA
import hashlib

key = RSA.generate(2048)
with open(%q, "wb") as f:
    f.write(key.exportKey('PEM'))
with open(%q, "wb") as f:
    f.write(key.publickey().exportKey('PEM'))

pub_pem = key.publickey().exportKey('PEM').decode('ascii')
if 'RSA PUBLIC KEY' not in pub_pem:
    pub_pem = pub_pem.replace('PUBLIC KEY', 'RSA PUBLIC KEY')
fingerprint = hashlib.sha256(pub_pem.encode('ascii')).hexdigest()
with open(%q, "w") as f:
    f.write(fingerprint)
`
	privPath := filepath.Join(dir, "id_rsa")
	pubPath := filepath.Join(dir, "id_rsa.pub")
	fpPath := filepath.Join(dir, "fingerprint.expected")

	cmd := exec.Command(python, "-c", fmt.Sprintf(script, privPath, pubPath, fpPath))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("python key generation failed: %v\n%s", err, stderr.String())
	}

	wantFP, err := os.ReadFile(fpPath)
	if err != nil {
		t.Fatal(err)
	}

	kp := &identity.KeyPair{
		PrivPath:        privPath,
		PubPath:         pubPath,
		FingerprintPath: filepath.Join(dir, "fingerprint"),
	}
	if err := kp.Load(); err != nil {
		t.Fatalf("KeyPair load: %v", err)
	}

	if kp.Fingerprint != string(wantFP) {
		t.Fatalf("fingerprint mismatch:\n  go:     %s\n  python: %s", kp.Fingerprint, wantFP)
	}
}

// TestCompatPSSSignatureCrossVerify signs with Go and verifies with
// pycryptodome's PKCS1_PSS, and vice versa, confirming the two
// implementations are interoperable (PSS itself is randomized, so exact
// signature bytes are never expected to match across implementations or
// even across repeated calls in the same one).
func TestCompatPSSSignatureCrossVerify(t *testing.T) {
	python := pythonBin(t)
	dir := t.TempDir()

	genScript := `
from Crypto.PublicKey import RSA
key = RSA.generate(2048)
with open(%q, "wb") as f:
    f.write(key.exportKey('PEM'))
`
	privPath := filepath.Join(dir, "id_rsa")
	cmd := exec.Command(python, "-c", fmt.Sprintf(genScript, privPath))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("python keygen failed: %v\n%s", err, stderr.String())
	}

	privPEMBytes, err := os.ReadFile(privPath)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(privPEMBytes)
	privKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("parsing python-generated private key: %v", err)
	}

	message := "pwnagotchi grid handshake payload"
	hashed := sha256.Sum256([]byte(message))
	goSig, err := rsa.SignPSS(rand.Reader, privKey, crypto.SHA256, hashed[:], &rsa.PSSOptions{SaltLength: 16, Hash: crypto.SHA256})
	if err != nil {
		t.Fatal(err)
	}
	goSigB64 := base64.StdEncoding.EncodeToString(goSig)

	verifyScript := `
from Crypto.PublicKey import RSA
from Crypto.Signature import PKCS1_PSS
import Crypto.Hash.SHA256 as SHA256
import base64, sys

with open(%q) as f:
    key = RSA.importKey(f.read())
hasher = SHA256.new(%q.encode("ascii"))
signer = PKCS1_PSS.new(key, saltLen=16)
sig = base64.b64decode(%q)
ok = signer.verify(hasher, sig)
print("OK" if ok else "FAIL")
`
	cmd = exec.Command(python, "-c", fmt.Sprintf(verifyScript, privPath, message, goSigB64))
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("python verify script failed: %v\n%s", err, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "OK" {
		t.Fatalf("python failed to verify a Go-produced PSS signature: %s", stdout.String())
	}

	// And the reverse direction: Python signs, Go verifies.
	signScript := `
from Crypto.PublicKey import RSA
from Crypto.Signature import PKCS1_PSS
import Crypto.Hash.SHA256 as SHA256
import base64

with open(%q) as f:
    key = RSA.importKey(f.read())
hasher = SHA256.new(%q.encode("ascii"))
signer = PKCS1_PSS.new(key, saltLen=16)
sig = signer.sign(hasher)
print(base64.b64encode(sig).decode("ascii"))
`
	stdout.Reset()
	cmd = exec.Command(python, "-c", fmt.Sprintf(signScript, privPath, message))
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("python sign script failed: %v\n%s", err, stderr.String())
	}
	pySigB64 := strings.TrimSpace(stdout.String())
	pySig, err := base64.StdEncoding.DecodeString(pySigB64)
	if err != nil {
		t.Fatal(err)
	}
	if err := rsa.VerifyPSS(&privKey.PublicKey, crypto.SHA256, hashed[:], pySig, &rsa.PSSOptions{SaltLength: 16, Hash: crypto.SHA256}); err != nil {
		t.Fatalf("go failed to verify a Python-produced PSS signature: %v", err)
	}
}
