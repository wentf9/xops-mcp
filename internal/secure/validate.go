package secure

import (
	"errors"
	cryptoSSH "golang.org/x/crypto/ssh"
)

func ValidateMaterial(kind string, material Material) error {
	switch kind {
	case "password":
		if len(material.Password) == 0 || len(material.Password) > 64<<10 || len(material.PrivateKey) > 0 || len(material.Passphrase) > 0 {
			return errors.New("password material must contain only a nonempty password up to 64 KiB")
		}
	case "key":
		if len(material.Password) > 0 || len(material.PrivateKey) == 0 || len(material.PrivateKey) > 128<<10 || len(material.Passphrase) > 64<<10 {
			return errors.New("invalid private key material size or fields")
		}
		var err error
		if len(material.Passphrase) > 0 {
			_, err = cryptoSSH.ParsePrivateKeyWithPassphrase(material.PrivateKey, material.Passphrase)
		} else {
			_, err = cryptoSSH.ParsePrivateKey(material.PrivateKey)
		}
		if err != nil {
			return errors.New("private key or passphrase is invalid")
		}
	default:
		return errors.New("credential kind must be password or key")
	}
	return nil
}
