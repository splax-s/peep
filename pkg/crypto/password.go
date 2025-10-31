package crypto

import "golang.org/x/crypto/bcrypt"

// HashPassword hashes plaintext using bcrypt.
func HashPassword(plain string) ([]byte, error) {
	return bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
}

// ComparePassword compares plaintext to hashed secret.
func ComparePassword(hash []byte, plain string) error {
	return bcrypt.CompareHashAndPassword(hash, []byte(plain))
}
