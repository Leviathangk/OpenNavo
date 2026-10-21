package content

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// JSONHash hashes JSON after normalizing whitespace and object key order.
func JSONHash(raw []byte) string {
	var value any
	if json.Unmarshal(raw, &value) == nil {
		if encoded, err := json.Marshal(value); err == nil {
			raw = encoded
		}
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
