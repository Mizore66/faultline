//go:build faultline_test

package witness

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsjson"
)

func testKey(seed byte) (ed25519.PublicKey, string) {
	s := make([]byte, 32)
	for i := range s {
		s[i] = seed
	}
	priv := ed25519.NewKeyFromSeed(s)
	der, _ := x509.MarshalPKCS8PrivateKey(priv)
	return priv.Public().(ed25519.PublicKey), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func keyringFor(t *testing.T, approvedBy string, pub ed25519.PublicKey) jsjson.Value {
	t.Helper()
	der, _ := x509.MarshalPKIXPublicKey(pub)
	pubPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	k := keyObject{ed25519: true, public: pub}
	r := jsjson.NewObj()
	r.Set("approvedBy", jsjson.MakeString(approvedBy))
	r.Set("keyId", jsjson.MakeString(publicKeyFingerprint(k)))
	r.Set("algorithm", jsjson.MakeString("ED25519"))
	r.Set("publicKeyPem", jsjson.MakeString(pubPEM))
	o := jsjson.NewObj()
	o.Set("schemaVersion", jsjson.MakeString(reviewerKeyringVersion))
	o.Set("reviewers", jsjson.MakeArray([]jsjson.Value{jsjson.MakeObject(r)}))
	return jsjson.MakeObject(o)
}

func frozenStore(t *testing.T) string {
	t.Helper()
	store := t.TempDir()
	if _, err := Propose(store, proposalInput(t)); err != nil {
		t.Fatal(err)
	}
	approval, _ := jsjson.Parse(`{"approvedBy":"reviewer@example.test"}`)
	if _, err := Approve(store, "demo-1", approval); err != nil {
		t.Fatal(err)
	}
	if _, err := Freeze(store, "demo-1", FreezeOptions{}); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestSignThenVerify(t *testing.T) {
	clock.ResetForTest("sign")
	store := frozenStore(t)
	pub, privPEM := testKey(1)
	keyring := keyringFor(t, "reviewer@example.test", pub)
	receipt, err := SignAuthenticatedApproval(store, "demo-1", SignOptions{PrivateKeyPEM: privPEM, Keyring: keyring})
	if err != nil {
		t.Fatal(err)
	}
	v := VerifyAuthenticatedApproval(store, "demo-1", VerifyAuthOptions{Keyring: &keyring, RequireSignature: true})
	if v.Get("signatureStatus").Str() != "VERIFIED" || !v.Get("valid").Bool() || receipt.Get("keyId").Str() == "" {
		t.Fatal(jsjson.Stringify(v))
	}
	info, _ := os.Stat(filepath.Join(store, "authenticated-approvals", "demo-1.json"))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode())
	}
	if _, err := SignAuthenticatedApproval(store, "demo-1", SignOptions{PrivateKeyPEM: privPEM, Keyring: keyring}); err == nil ||
		!strings.HasPrefix(err.Error(), "Authenticated witness approval already exists and cannot be overwritten: ") {
		t.Fatalf("second sign: %v", err)
	}
}

func TestSignRejectsWrongKeyAndIdentity(t *testing.T) {
	clock.ResetForTest("sign2")
	store := frozenStore(t)
	pub, _ := testKey(1)
	_, otherPEM := testKey(2)
	_, err := SignAuthenticatedApproval(store, "demo-1", SignOptions{PrivateKeyPEM: otherPEM, Keyring: keyringFor(t, "reviewer@example.test", pub)})
	if err == nil || err.Error() != "Reviewer private key is not present in the supplied reviewer keyring." {
		t.Fatalf("%v", err)
	}
	_, privPEM := testKey(1)
	_, err = SignAuthenticatedApproval(store, "demo-1", SignOptions{PrivateKeyPEM: privPEM, Keyring: keyringFor(t, "someone@example.test", pub)})
	if err == nil || err.Error() != "Reviewer keyring identity does not match the frozen witness approval actor." {
		t.Fatalf("%v", err)
	}
	_, err = SignAuthenticatedApproval(store, "demo-1", SignOptions{PrivateKeyPEM: "garbage", Keyring: keyringFor(t, "reviewer@example.test", pub)})
	if err == nil || err.Error() != "Reviewer private key is invalid: error:1E08010C:DECODER routines::unsupported" {
		t.Fatalf("%v", err)
	}
}

func TestVerifyWithoutReceiptIsNotPresent(t *testing.T) {
	clock.ResetForTest("sign3")
	store := frozenStore(t)
	pub, _ := testKey(1)
	keyring := keyringFor(t, "reviewer@example.test", pub)
	v := VerifyAuthenticatedApproval(store, "demo-1", VerifyAuthOptions{Keyring: &keyring})
	if v.Get("signatureStatus").Str() != "NOT_PRESENT" || !v.Get("valid").Bool() {
		t.Fatal(jsjson.Stringify(v))
	}
	v = VerifyAuthenticatedApproval(store, "demo-1", VerifyAuthOptions{Keyring: &keyring, RequireSignature: true})
	if v.Get("valid").Bool() || v.Get("errors").Items()[0].Str() != "authenticated reviewer signature is required but missing" {
		t.Fatal(jsjson.Stringify(v))
	}
}
