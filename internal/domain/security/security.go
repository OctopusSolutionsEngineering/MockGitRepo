package security

import (
	"crypto/sha256"
	"encoding/hex"
)

func GetSha256(input string) string {
	hash := sha256.Sum256([]byte(input))
	return hex.EncodeToString(hash[:])
}
