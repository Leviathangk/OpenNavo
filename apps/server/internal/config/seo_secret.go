package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
)

// SEO credentials (IndexNow key, Cloudflare token) use their own purpose derivation and
// per-field authenticated data, so neither can be swapped with the other or with
// GitHub/translation ciphertexts. api and worker share the environment root key.
func (c Config) seoCipher() (cipher.AEAD, error) {
	secret := c.SEOSettingsEncryptionKey
	if secret == "" {
		secret = c.JWTSecret
	}
	if len(secret) < 32 {
		return nil, errors.New("SEO encryption configuration missing")
	}
	derive := hmac.New(sha256.New, []byte(secret))
	_, _ = derive.Write([]byte("opennavo:seo-settings:v1"))
	block, err := aes.NewCipher(derive.Sum(nil))
	if err != nil {
		return nil, errors.New("SEO encryption unavailable")
	}
	return cipher.NewGCM(block)
}

// EncryptSEOSecret encrypts a credential for the named seo_settings field.
func (c Config) EncryptSEOSecret(field, value string) (string, error) {
	aead, err := c.seoCipher()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", errors.New("SEO encryption unavailable")
	}
	return base64.StdEncoding.EncodeToString(aead.Seal(nonce, nonce, []byte(value), []byte("seo_settings:"+field))), nil
}

// DecryptSEOSecret never includes the ciphertext or plaintext in its errors.
func (c Config) DecryptSEOSecret(field, value string) (string, error) {
	aead, err := c.seoCipher()
	if err != nil {
		return "", err
	}
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(data) < aead.NonceSize() {
		return "", errors.New("SEO credential unavailable")
	}
	plain, err := aead.Open(nil, data[:aead.NonceSize()], data[aead.NonceSize():], []byte("seo_settings:"+field))
	if err != nil {
		return "", errors.New("SEO credential unavailable")
	}
	return string(plain), nil
}
