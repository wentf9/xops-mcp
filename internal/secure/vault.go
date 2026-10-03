// Package secure encrypts deployment credentials without storing the master
// key in the database or discovering keys in personal configuration.
package secure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

type Vault struct{ aead cipher.AEAD }

type Material struct {
	Password   []byte `json:"password,omitempty"`
	PrivateKey []byte `json:"private_key,omitempty"`
	Passphrase []byte `json:"passphrase,omitempty"`
}

func (m *Material) Clear() {
	clear(m.Password)
	clear(m.PrivateKey)
	clear(m.Passphrase)
	*m = Material{}
}

func New(key []byte) (*Vault, error) {
	if len(key) != 32 {
		return nil, errors.New("master key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("initialize credential cipher: %w", err)
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, fmt.Errorf("initialize credential encryption: %w", err)
	}
	return &Vault{aead}, nil
}

// Each envelope has a format version. Authenticated context prevents moving a
// ciphertext between deployments, credential IDs, kinds, or material versions.
func (v *Vault) Seal(context string, plaintext []byte) []byte {
	return append([]byte{1}, v.aead.Seal(nil, nil, plaintext, []byte(context))...)
}

func (v *Vault) Open(context string, envelope []byte) ([]byte, error) {
	if len(envelope) < 1 || envelope[0] != 1 {
		return nil, errors.New("unsupported credential envelope version")
	}
	plaintext, err := v.aead.Open(nil, nil, envelope[1:], []byte(context))
	if err != nil {
		return nil, errors.New("credential authentication failed")
	}
	return plaintext, nil
}

func Context(domain, id, kind, version string) string {
	return domain + "\x00" + id + "\x00" + kind + "\x00" + version
}

func (v *Vault) Encrypt(domain, id, kind, version string, material Material) ([]byte, error) {
	data, err := json.Marshal(material)
	if err != nil {
		return nil, fmt.Errorf("encode credential material: %w", err)
	}
	defer clear(data)
	return v.Seal(Context(domain, id, kind, version), data), nil
}

func (v *Vault) Decrypt(domain, id, kind, version string, ciphertext []byte) (Material, error) {
	data, err := v.Open(Context(domain, id, kind, version), ciphertext)
	if err != nil {
		return Material{}, err
	}
	defer clear(data)
	var material Material
	if err := json.Unmarshal(data, &material); err != nil {
		material.Clear()
		return Material{}, errors.New("invalid credential material")
	}
	return material, nil
}

func ID() string {
	var data [16]byte
	rand.Read(data[:])
	return hex.EncodeToString(data[:])
}
