package utils

import (
	"errors"

	"github.com/lejianwen/rustdesk-api/v2/george/auth"
	"golang.org/x/crypto/bcrypt"
)

// EncryptPassword hashes the input password using argon2id (George).
// Legacy bcrypt/MD5 hashes keep working in VerifyPassword and are re-hashed on login.
func EncryptPassword(password string) (string, error) {
	return auth.HashPassword(password)
}

// VerifyPassword checks the input password against the stored hash.
// Supports argon2id (current), bcrypt (upstream) and legacy MD5. When a
// non-argon2id hash matches, a new argon2id hash is returned so the caller
// can migrate the stored password transparently.
func VerifyPassword(hash, input string) (bool, string, error) {
	if auth.IsArgon2(hash) {
		ok, err := auth.VerifyArgon2(hash, input)
		return ok, "", err
	}
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(input))
	if err == nil {
		newHash, err2 := auth.HashPassword(input)
		return true, newHash, err2
	}

	var invalidPrefixErr bcrypt.InvalidHashPrefixError
	if errors.As(err, &invalidPrefixErr) || errors.Is(err, bcrypt.ErrHashTooShort) {
		// Try fallback to legacy MD5 hash verification
		if hash == Md5(input+"rustdesk-api") {
			newHash, err2 := auth.HashPassword(input)
			if err2 != nil {
				return true, "", err2
			}
			return true, newHash, nil
		}
	}
	if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
		return false, "", nil
	}
	return false, "", err
}
