package secure

import (
	"bytes"
	"testing"
)

func TestAuthenticatedVersionedMaterial(t *testing.T) {
	v, err := New(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := v.Encrypt("domain", "credential", "password", "v1", Material{Password: []byte("synthetic-password")})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ciphertext, []byte("synthetic-password")) {
		t.Fatal("plaintext in envelope")
	}
	m, err := v.Decrypt("domain", "credential", "password", "v1", ciphertext)
	if err != nil || string(m.Password) != "synthetic-password" {
		t.Fatalf("decrypt: %v", err)
	}
	m.Clear()
	for _, parts := range [][4]string{{"other", "credential", "password", "v1"}, {"domain", "other", "password", "v1"}, {"domain", "credential", "key", "v1"}, {"domain", "credential", "password", "v2"}} {
		if _, err := v.Decrypt(parts[0], parts[1], parts[2], parts[3], ciphertext); err == nil {
			t.Fatal("accepted substituted credential context")
		}
	}
	corrupt := bytes.Clone(ciphertext)
	corrupt[len(corrupt)-1] ^= 1
	if _, err := v.Open(Context("domain", "credential", "password", "v1"), corrupt); err == nil {
		t.Fatal("accepted corrupted envelope")
	}
	corrupt[0] = 2
	if _, err := v.Open("", corrupt); err == nil {
		t.Fatal("accepted future envelope")
	}
	other, err := New(bytes.Repeat([]byte{2}, 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Open(Context("domain", "credential", "password", "v1"), ciphertext); err == nil {
		t.Fatal("accepted wrong master key")
	}
	if _, err := New(make([]byte, 16)); err == nil {
		t.Fatal("accepted short master key")
	}
}
