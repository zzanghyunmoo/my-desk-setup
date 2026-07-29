package planning

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

func Digest(plan Plan) (string, error) {
	plan.Digest = ""
	encoded, err := json.Marshal(plan)
	if err != nil {
		return "", fmt.Errorf("encode plan for digest: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
