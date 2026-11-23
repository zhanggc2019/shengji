//go:build !windows

package minutes

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
)

func protectCredential(root, scope, secret string) ([]byte, error) {
	cipher, err := credentialCipher(root, true)
	if err != nil {
		return nil, err
	}
	return cipher.Seal(nil, nil, []byte(secret), []byte(scope)), nil
}

func unprotectCredential(root, scope string, ciphertext []byte) (string, error) {
	cipher, err := credentialCipher(root, false)
	if err != nil {
		return "", err
	}
	plaintext, err := cipher.Open(nil, nil, ciphertext, []byte(scope))
	return string(plaintext), err
}

// SQLite serializes saves while the library lock prevents another process from creating the key.
func credentialCipher(root string, create bool) (cipher.AEAD, error) {
	path := filepath.Join(root, "model-credentials.key")
	key, err := os.ReadFile(path)
	if create && errors.Is(err, os.ErrNotExist) {
		key = make([]byte, 32)
		rand.Read(key)
		file, createErr := os.CreateTemp(root, ".model-credentials-*")
		if createErr != nil {
			return nil, createErr
		}
		defer os.Remove(file.Name())
		_, writeErr := file.Write(key)
		err = errors.Join(writeErr, file.Sync(), file.Close())
		if err == nil {
			err = os.Rename(file.Name(), path)
		}
	}
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCMWithRandomNonce(block)
}
