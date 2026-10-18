package app

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"github.com/nightnoryu/tesseract/internal/auth/domain"
	"golang.org/x/crypto/argon2"
	"strings"
	"unicode/utf8"
)

const prefix = "$argon2id$v=19$m=65536,t=3,p=1$"

func Hash(password string) (string, error) {
	if !utf8.ValidString(password) || utf8.RuneCountInString(password) < 12 || len(password) > 1024 {
		return "", domain.ErrPassword
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, 3, 65536, 1, 32)
	return prefix + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key), nil
}
func Verify(encoded, password string) bool {
	if len(password) > 1024 || !strings.HasPrefix(encoded, prefix) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(encoded, prefix), "$")
	if len(parts) != 2 {
		return false
	}
	salt, e1 := base64.RawStdEncoding.DecodeString(parts[0])
	key, e2 := base64.RawStdEncoding.DecodeString(parts[1])
	if e1 != nil || e2 != nil || len(salt) != 16 || len(key) != 32 {
		return false
	}
	actual := argon2.IDKey([]byte(password), salt, 3, 65536, 1, 32)
	return subtle.ConstantTimeCompare(key, actual) == 1
}
