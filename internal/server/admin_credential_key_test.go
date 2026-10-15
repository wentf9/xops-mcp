package server_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"testing"
	"time"

	"github.com/wentf9/xops-mcp/internal/service"
	cryptoSSH "golang.org/x/crypto/ssh"
)

func TestAdminCredentialKeyValidationAndSave(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()

	_, privEd, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	unencBlock, err := cryptoSSH.MarshalPrivateKey(privEd, "")
	if err != nil {
		t.Fatal(err)
	}
	unencPEM := string(pem.EncodeToMemory(unencBlock))

	encBlock, err := cryptoSSH.MarshalPrivateKeyWithPassphrase(privEd, "", []byte("test-passphrase"))
	if err != nil {
		t.Fatal(err)
	}
	encPEM := string(pem.EncodeToMemory(encBlock))

	cfg := adminConfig(t)
	p := newAdminPeer(t, ctx, cfg)
	p.initialize()

	// 1. Test validate-key with unencrypted key (no passphrase)
	resBytes := p.request("POST", "/api/v1/credentials/validate-key", map[string]string{
		"privateKey": unencPEM,
		"passphrase": "",
	}, 200, nil)
	var res struct {
		Valid       bool   `json:"valid"`
		Protected   bool   `json:"protected"`
		KeyType     string `json:"keyType"`
		Fingerprint string `json:"fingerprint"`
		Message     string `json:"message"`
	}
	if err := json.Unmarshal(resBytes, &res); err != nil {
		t.Fatal(err)
	}
	if !res.Valid || res.Protected || res.KeyType != "ssh-ed25519" || res.Fingerprint == "" {
		t.Fatalf("unexpected unencrypted validate-key result: %+v", res)
	}

	// 2. Test validate-key with unencrypted key and unneeded passphrase
	resBytes = p.request("POST", "/api/v1/credentials/validate-key", map[string]string{
		"privateKey": unencPEM,
		"passphrase": "some-passphrase",
	}, 200, nil)
	if err := json.Unmarshal(resBytes, &res); err != nil {
		t.Fatal(err)
	}
	if res.Valid || res.Protected {
		t.Fatalf("expected valid=false, protected=false for unneeded pass: %+v", res)
	}

	// 3. Test validate-key with protected key (no passphrase)
	resBytes = p.request("POST", "/api/v1/credentials/validate-key", map[string]string{
		"privateKey": encPEM,
		"passphrase": "",
	}, 200, nil)
	if err := json.Unmarshal(resBytes, &res); err != nil {
		t.Fatal(err)
	}
	if res.Valid || !res.Protected {
		t.Fatalf("expected valid=false, protected=true for missing pass: %+v", res)
	}

	// 4. Test validate-key with protected key (wrong passphrase)
	resBytes = p.request("POST", "/api/v1/credentials/validate-key", map[string]string{
		"privateKey": encPEM,
		"passphrase": "wrong-passphrase",
	}, 200, nil)
	if err := json.Unmarshal(resBytes, &res); err != nil {
		t.Fatal(err)
	}
	if res.Valid || !res.Protected {
		t.Fatalf("expected valid=false, protected=true for wrong pass: %+v", res)
	}

	// 5. Test validate-key with protected key (correct passphrase)
	resBytes = p.request("POST", "/api/v1/credentials/validate-key", map[string]string{
		"privateKey": encPEM,
		"passphrase": "test-passphrase",
	}, 200, nil)
	if err := json.Unmarshal(resBytes, &res); err != nil {
		t.Fatal(err)
	}
	if !res.Valid || !res.Protected || res.KeyType != "ssh-ed25519" || res.Fingerprint == "" {
		t.Fatalf("unexpected protected validate-key result: %+v", res)
	}

	// 6. Test validate-key with invalid/malformed key
	resBytes = p.request("POST", "/api/v1/credentials/validate-key", map[string]string{
		"privateKey": "invalid key format",
		"passphrase": "",
	}, 200, nil)
	if err := json.Unmarshal(resBytes, &res); err != nil {
		t.Fatal(err)
	}
	if res.Valid {
		t.Fatalf("expected valid=false for invalid key: %+v", res)
	}

	// 7. Test SaveCredential (POST /credentials) with protected key without passphrase -> 422
	p.request("POST", "/api/v1/credentials", service.CredentialInput{
		Name: "enc-no-pass",
		Kind: "key",
		Secret: &service.MaterialInput{
			PrivateKey: encPEM,
			Passphrase: "",
		},
	}, 422, nil)

	// 8. Test SaveCredential with protected key and wrong passphrase -> 422
	p.request("POST", "/api/v1/credentials", service.CredentialInput{
		Name: "enc-wrong-pass",
		Kind: "key",
		Secret: &service.MaterialInput{
			PrivateKey: encPEM,
			Passphrase: "wrong-passphrase",
		},
	}, 422, nil)

	// 9. Test SaveCredential with unencrypted key and unneeded passphrase -> 422
	p.request("POST", "/api/v1/credentials", service.CredentialInput{
		Name: "unenc-with-pass",
		Kind: "key",
		Secret: &service.MaterialInput{
			PrivateKey: unencPEM,
			Passphrase: "unneeded-passphrase",
		},
	}, 422, nil)

	// 10. Test SaveCredential with unencrypted key and no passphrase -> 200
	unencCredID := p.save("credentials", service.CredentialInput{
		Name: "unenc-key",
		Kind: "key",
		Secret: &service.MaterialInput{
			PrivateKey: unencPEM,
			Passphrase: "",
		},
	})
	if unencCredID == "" {
		t.Fatal("empty unencCredID")
	}

	// 11. Test SaveCredential with protected key and correct passphrase -> 200
	encCredID := p.save("credentials", service.CredentialInput{
		Name: "enc-key",
		Kind: "key",
		Secret: &service.MaterialInput{
			PrivateKey: encPEM,
			Passphrase: "test-passphrase",
		},
	})
	if encCredID == "" {
		t.Fatal("empty encCredID")
	}

	// 12. Test updating credential name without changing material (Secret == nil) -> 200
	p.request("PUT", "/api/v1/credentials/"+encCredID, service.CredentialInput{
		Name: "enc-key-renamed",
		Kind: "key",
	}, 200, nil)
}
