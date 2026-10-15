package secure

import (
	"encoding/pem"
	"errors"
	"strings"

	cryptoSSH "golang.org/x/crypto/ssh"
)

var (
	ErrPassphraseRequired    = errors.New("private key is passphrase-protected; passphrase is required")
	ErrPassphraseIncorrect   = errors.New("private key passphrase is incorrect")
	ErrPassphraseUnnecessary = errors.New("private key is not passphrase-protected; please omit passphrase")
	ErrPrivateKeyInvalid     = errors.New("private key format is invalid")
)

type PrivateKeyInfo struct {
	Protected   bool
	KeyType     string
	Fingerprint string
}

func InspectPrivateKey(key, passphrase []byte) (*PrivateKeyInfo, error) {
	if len(key) == 0 || len(key) > 128<<10 || len(passphrase) > 64<<10 {
		return nil, errors.New("invalid private key material size or fields")
	}

	signer, err := cryptoSSH.ParsePrivateKey(key)
	if err == nil {
		info := &PrivateKeyInfo{
			Protected:   false,
			KeyType:     signer.PublicKey().Type(),
			Fingerprint: cryptoSSH.FingerprintSHA256(signer.PublicKey()),
		}
		if len(passphrase) > 0 {
			return info, ErrPassphraseUnnecessary
		}
		return info, nil
	}

	var missing *cryptoSSH.PassphraseMissingError
	if errors.As(err, &missing) {
		info := &PrivateKeyInfo{Protected: true}
		if len(passphrase) == 0 {
			return info, ErrPassphraseRequired
		}
		signer, err := cryptoSSH.ParsePrivateKeyWithPassphrase(key, passphrase)
		if err != nil {
			return info, ErrPassphraseIncorrect
		}
		info.KeyType = signer.PublicKey().Type()
		info.Fingerprint = cryptoSSH.FingerprintSHA256(signer.PublicKey())
		return info, nil
	}

	if len(passphrase) > 0 {
		signer, err := cryptoSSH.ParsePrivateKeyWithPassphrase(key, passphrase)
		if err == nil {
			return &PrivateKeyInfo{
				Protected:   true,
				KeyType:     signer.PublicKey().Type(),
				Fingerprint: cryptoSSH.FingerprintSHA256(signer.PublicKey()),
			}, nil
		}
		if strings.Contains(err.Error(), "not password protected") {
			return &PrivateKeyInfo{Protected: false}, ErrPassphraseUnnecessary
		}
		block, _ := pem.Decode(key)
		if block != nil && strings.Contains(block.Type, "ENCRYPTED") {
			return &PrivateKeyInfo{Protected: true}, ErrPassphraseIncorrect
		}
		return nil, ErrPrivateKeyInvalid
	}

	return nil, ErrPrivateKeyInvalid
}

func ValidateMaterial(kind string, material Material) error {
	switch kind {
	case "password":
		if len(material.Password) == 0 || len(material.Password) > 64<<10 || len(material.PrivateKey) > 0 || len(material.Passphrase) > 0 {
			return errors.New("password material must contain only a nonempty password up to 64 KiB")
		}
	case "key":
		if len(material.Password) > 0 {
			return errors.New("password must be empty for key credentials")
		}
		_, err := InspectPrivateKey(material.PrivateKey, material.Passphrase)
		return err
	default:
		return errors.New("credential kind must be password or key")
	}
	return nil
}
