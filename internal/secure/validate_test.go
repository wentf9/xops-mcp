package secure

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/pem"
	"errors"
	"testing"

	cryptoSSH "golang.org/x/crypto/ssh"
)

func TestInspectAndValidatePrivateKey(t *testing.T) {
	_, privEd, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	unencBlock, err := cryptoSSH.MarshalPrivateKey(privEd, "")
	if err != nil {
		t.Fatal(err)
	}
	unencPEM := pem.EncodeToMemory(unencBlock)

	encBlock, err := cryptoSSH.MarshalPrivateKeyWithPassphrase(privEd, "", []byte("ed-passphrase"))
	if err != nil {
		t.Fatal(err)
	}
	encPEM := pem.EncodeToMemory(encBlock)

	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	rsaEncBlock, err := cryptoSSH.MarshalPrivateKeyWithPassphrase(rsaKey, "", []byte("rsa-passphrase"))
	if err != nil {
		t.Fatal(err)
	}
	rsaEncPEM := pem.EncodeToMemory(rsaEncBlock)

	// Test 1: Unencrypted key without passphrase
	info, err := InspectPrivateKey(unencPEM, nil)
	if err != nil {
		t.Fatalf("unencrypted without passphrase: %v", err)
	}
	if info.Protected || info.KeyType != "ssh-ed25519" || info.Fingerprint == "" {
		t.Fatalf("unexpected info for unencrypted key: %+v", info)
	}
	if err := ValidateMaterial("key", Material{PrivateKey: unencPEM}); err != nil {
		t.Fatalf("validate unencrypted without passphrase: %v", err)
	}

	// Test 2: Unencrypted key with unnecessary passphrase
	info, err = InspectPrivateKey(unencPEM, []byte("unneeded"))
	if !errors.Is(err, ErrPassphraseUnnecessary) {
		t.Fatalf("expected ErrPassphraseUnnecessary, got %v", err)
	}
	if info == nil || info.Protected {
		t.Fatalf("unexpected info: %+v", info)
	}
	if err := ValidateMaterial("key", Material{PrivateKey: unencPEM, Passphrase: []byte("unneeded")}); !errors.Is(err, ErrPassphraseUnnecessary) {
		t.Fatalf("expected ErrPassphraseUnnecessary from ValidateMaterial, got %v", err)
	}

	// Test 3: Protected Ed25519 key without passphrase
	info, err = InspectPrivateKey(encPEM, nil)
	if !errors.Is(err, ErrPassphraseRequired) {
		t.Fatalf("expected ErrPassphraseRequired, got %v", err)
	}
	if info == nil || !info.Protected {
		t.Fatalf("expected protected=true, got %+v", info)
	}
	if err := ValidateMaterial("key", Material{PrivateKey: encPEM}); !errors.Is(err, ErrPassphraseRequired) {
		t.Fatalf("expected ErrPassphraseRequired from ValidateMaterial, got %v", err)
	}

	// Test 4: Protected Ed25519 key with wrong passphrase
	info, err = InspectPrivateKey(encPEM, []byte("wrong-passphrase"))
	if !errors.Is(err, ErrPassphraseIncorrect) {
		t.Fatalf("expected ErrPassphraseIncorrect, got %v", err)
	}
	if info == nil || !info.Protected {
		t.Fatalf("expected protected=true, got %+v", info)
	}
	if err := ValidateMaterial("key", Material{PrivateKey: encPEM, Passphrase: []byte("wrong-passphrase")}); !errors.Is(err, ErrPassphraseIncorrect) {
		t.Fatalf("expected ErrPassphraseIncorrect from ValidateMaterial, got %v", err)
	}

	// Test 5: Protected Ed25519 key with correct passphrase
	info, err = InspectPrivateKey(encPEM, []byte("ed-passphrase"))
	if err != nil {
		t.Fatalf("protected with correct passphrase: %v", err)
	}
	if !info.Protected || info.KeyType != "ssh-ed25519" || info.Fingerprint == "" {
		t.Fatalf("unexpected info for protected key: %+v", info)
	}
	if err := ValidateMaterial("key", Material{PrivateKey: encPEM, Passphrase: []byte("ed-passphrase")}); err != nil {
		t.Fatalf("validate protected with correct passphrase: %v", err)
	}

	// Test 6: Protected RSA key with correct and incorrect passphrase
	if _, err := InspectPrivateKey(rsaEncPEM, []byte("wrong")); !errors.Is(err, ErrPassphraseIncorrect) {
		t.Fatalf("expected ErrPassphraseIncorrect for rsa, got %v", err)
	}
	info, err = InspectPrivateKey(rsaEncPEM, []byte("rsa-passphrase"))
	if err != nil || !info.Protected || info.KeyType != "ssh-rsa" {
		t.Fatalf("expected success for rsa, got info=%+v err=%v", info, err)
	}

	// Test 7: Malformed / garbage key
	if _, err := InspectPrivateKey([]byte("not a valid key"), nil); !errors.Is(err, ErrPrivateKeyInvalid) {
		t.Fatalf("expected ErrPrivateKeyInvalid, got %v", err)
	}
	if _, err := InspectPrivateKey([]byte("not a valid key"), []byte("pass")); !errors.Is(err, ErrPrivateKeyInvalid) {
		t.Fatalf("expected ErrPrivateKeyInvalid with pass, got %v", err)
	}
}

func TestValidateMaterialPassword(t *testing.T) {
	if err := ValidateMaterial("password", Material{Password: []byte("secure-password")}); err != nil {
		t.Fatalf("valid password failed: %v", err)
	}
	if err := ValidateMaterial("password", Material{Password: nil}); err == nil {
		t.Fatal("accepted empty password")
	}
	if err := ValidateMaterial("password", Material{Password: []byte("pass"), PrivateKey: []byte("key")}); err == nil {
		t.Fatal("accepted password with private key")
	}
	if err := ValidateMaterial("unknown", Material{}); err == nil {
		t.Fatal("accepted unknown kind")
	}
}
