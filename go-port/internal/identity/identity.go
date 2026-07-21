// Package identity ports pwnagotchi/identity.py: RSA keypair
// generation/loading (delegated to the external `pwngrid` binary, exactly
// like Python) and PSS message signing, used by the grid/mesh identity
// protocol.
package identity

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"log" // stdlib log; will route through internal/logging once that package exists (feature-matrix.md) — emits real output today, not a stub.
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// DefaultPath mirrors identity.DefaultPath.
const DefaultPath = "/etc/pwnagotchi/"

// View is the subset of pwnagotchi.ui.view.View that KeyPair calls into:
// on_keys_generation (shown once, before shelling out to pwngrid) and
// on_starting (shown once keys are confirmed loadable). A real
// internal/ui/view.View will satisfy this once that package exists — kept
// as a narrow interface so internal/identity doesn't have to depend on the
// (not yet ported) UI package.
type View interface {
	OnKeysGeneration()
	OnStarting()
}

// GenerateKeys mirrors identity.py's `os.system("pwngrid -generate -keys
// '%s'" % self.path)`, but invoked via os/exec with an explicit argv instead
// of a shell string — same executable, arguments, and (inherited) stdio,
// with no shell-injection surface. Overridable for tests, the same pattern
// internal/unit uses for host paths.
var GenerateKeys = func(path string) error {
	cmd := exec.Command("pwngrid", "-generate", "-keys", path)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

// KeyPair ports identity.KeyPair.
type KeyPair struct {
	Path            string
	PrivPath        string
	PubPath         string
	FingerprintPath string

	PrivKey *rsa.PrivateKey
	PubKey  *rsa.PublicKey

	// PubKeyPEM replicates pub_key.exportKey('PEM') plus the header/footer
	// swap to "RSA PUBLIC KEY" — see docs/known-differences.md. The body is
	// X.509 SubjectPublicKeyInfo DER despite the RSA-PKCS1-looking header;
	// this is what real Python produces and the grid protocol relies on.
	PubKeyPEM    string
	PubKeyPEMB64 string
	Fingerprint  string

	view View
}

// NewKeyPair ports KeyPair.__init__: ensures Path exists, then loops
// generating (via the external pwngrid binary, exactly like Python)
// and loading keys until a valid pair is loaded, deleting and regenerating
// on any corruption.
func NewKeyPair(path string, view View) (*KeyPair, error) {
	if path == "" {
		path = DefaultPath
	}
	kp := &KeyPair{
		Path:            path,
		PrivPath:        filepath.Join(path, "id_rsa"),
		FingerprintPath: filepath.Join(path, "fingerprint"),
		view:            view,
	}
	kp.PubPath = kp.PrivPath + ".pub"

	if _, err := os.Stat(kp.Path); os.IsNotExist(err) {
		if err := os.MkdirAll(kp.Path, 0o755); err != nil {
			return nil, err
		}
	}

	for {
		_, privErr := os.Stat(kp.PrivPath)
		_, pubErr := os.Stat(kp.PubPath)
		if os.IsNotExist(privErr) || os.IsNotExist(pubErr) {
			kp.view.OnKeysGeneration()
			log.Printf("generating %s ...", kp.PrivPath)
			if err := GenerateKeys(kp.Path); err != nil {
				log.Printf("pwngrid -generate -keys failed: %v", err)
			}
		}

		if err := kp.Load(); err != nil {
			log.Printf("error loading keys, maybe corrupted, deleting and regenerating ...: %v", err)
			os.Remove(kp.PrivPath)
			os.Remove(kp.PubPath)
			continue
		}

		kp.view.OnStarting()
		return kp, nil
	}
}

// Load reads PrivPath/PubPath from disk and (re)computes PubKeyPEM,
// PubKeyPEMB64, and Fingerprint, writing the fingerprint to
// FingerprintPath. Exported so callers (and tests) can (re)load a KeyPair
// whose Path/PrivPath/PubPath/FingerprintPath were set directly, without
// going through the full NewKeyPair generate-on-missing loop.
func (kp *KeyPair) Load() error {
	privPEM, err := os.ReadFile(kp.PrivPath)
	if err != nil {
		return err
	}
	priv, err := parseRSAPrivateKey(privPEM)
	if err != nil {
		return err
	}
	kp.PrivKey = priv

	pubPEM, err := os.ReadFile(kp.PubPath)
	if err != nil {
		return err
	}
	pub, err := parseRSAPublicKey(pubPEM)
	if err != nil {
		return err
	}
	kp.PubKey = pub

	pubKeyPEM, err := exportPublicKeyPythonStyle(pub)
	if err != nil {
		return err
	}
	kp.PubKeyPEM = pubKeyPEM

	pemASCII := []byte(pubKeyPEM)
	kp.PubKeyPEMB64 = base64.StdEncoding.EncodeToString(pemASCII)
	sum := sha256.Sum256(pemASCII)
	kp.Fingerprint = fmt.Sprintf("%x", sum)

	// Python: open(fingerprint_path, 'w+t').write(...) — a plain,
	// non-atomic write, not routed through ensure_write. Replicated as-is.
	return os.WriteFile(kp.FingerprintPath, []byte(kp.Fingerprint), 0o644)
}

// Sign ports KeyPair.sign: SHA256 + RSA-PSS (MGF1-SHA256, salt length 16),
// matching Crypto.Signature.pss's defaults as used by PKCS1_PSS.new(key,
// saltLen=16). PSS signatures are randomized (a fresh salt each call), so
// signatures are not byte-reproducible run-to-run in either language —
// interoperability is verified by cross-signing/cross-verifying against the
// real Python implementation, not by exact byte comparison.
func (kp *KeyPair) Sign(message string) (signature []byte, signatureB64 string, err error) {
	hashed := sha256.Sum256([]byte(message))
	sig, err := rsa.SignPSS(rand.Reader, kp.PrivKey, crypto.SHA256, hashed[:], &rsa.PSSOptions{
		SaltLength: 16,
		Hash:       crypto.SHA256,
	})
	if err != nil {
		return nil, "", err
	}
	return sig, base64.StdEncoding.EncodeToString(sig), nil
}

func parseRSAPrivateKey(data []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("identity: no PEM block found in private key")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	keyAny, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("identity: unrecognized private key format: %w", err)
	}
	key, ok := keyAny.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("identity: private key is not RSA")
	}
	return key, nil
}

func parseRSAPublicKey(data []byte) (*rsa.PublicKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("identity: no PEM block found in public key")
	}
	if key, err := x509.ParsePKCS1PublicKey(block.Bytes); err == nil {
		return key, nil
	}
	keyAny, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("identity: unrecognized public key format: %w", err)
	}
	key, ok := keyAny.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("identity: public key is not RSA")
	}
	return key, nil
}

// exportPublicKeyPythonStyle replicates pub_key.exportKey('PEM') (always
// X.509 SubjectPublicKeyInfo, regardless of the key's original on-disk
// format) followed by identity.py's `.replace('PUBLIC KEY', 'RSA PUBLIC
// KEY')` header/footer swap — verified to always fire in practice because
// pycryptodome's RSA public-key PEM export never emits the literal "RSA
// PUBLIC KEY" string. The result is a PEM whose header/footer claim PKCS1
// RSAPublicKey but whose body is actually SubjectPublicKeyInfo DER; that
// mismatch is exactly what real pwnagotchi computes fingerprints/signatures
// metadata from, so it must be preserved byte-for-byte.
func exportPublicKeyPythonStyle(pub *rsa.PublicKey) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", err
	}
	block := &pem.Block{Type: "PUBLIC KEY", Bytes: der}
	// pycryptodome's PEM encoder does not emit a trailing newline after the
	// final "-----END ...-----" line; Go's pem.EncodeToMemory always does.
	// Stripped to keep the fingerprint hash input byte-identical (verified
	// against a real pycryptodome-generated key — see identity_test.go).
	standard := strings.TrimSuffix(string(pem.EncodeToMemory(block)), "\n")
	return strings.ReplaceAll(standard, "PUBLIC KEY", "RSA PUBLIC KEY"), nil
}
