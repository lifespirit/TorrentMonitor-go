package core

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const passwordHashIterations = 120000

// HashPassword returns a self-contained salted password hash. It intentionally
// avoids external dependencies so the scaffold remains a small Go module.
func HashPassword(password string) string {
	password = strings.TrimSpace(password)
	if password == "" {
		return ""
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		fallback := []byte(fmt.Sprintf("%d", time.Now().UnixNano()))
		copy(salt, fallback)
	}
	dk := derivePasswordKey([]byte(password), salt, passwordHashIterations)
	return fmt.Sprintf("tm-pbkdf2-sha256$%d$%s$%s", passwordHashIterations, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(dk))
}

func VerifyPassword(password, encoded string) bool {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" || strings.TrimSpace(password) == "" {
		return false
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "tm-pbkdf2-sha256" {
		return false
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations < 10000 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil || len(salt) == 0 {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil || len(expected) == 0 {
		return false
	}
	actual := derivePasswordKey([]byte(strings.TrimSpace(password)), salt, iterations)
	if len(actual) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

func derivePasswordKey(password, salt []byte, iterations int) []byte {
	// PBKDF2-HMAC-SHA256, dkLen=32. Implemented locally to keep go.mod dependency-free.
	mac := hmac.New(sha256.New, password)
	mac.Write(salt)
	mac.Write([]byte{0, 0, 0, 1})
	u := mac.Sum(nil)
	out := make([]byte, len(u))
	copy(out, u)
	for i := 1; i < iterations; i++ {
		mac = hmac.New(sha256.New, password)
		mac.Write(u)
		u = mac.Sum(nil)
		for j := range out {
			out[j] ^= u[j]
		}
	}
	return out
}
