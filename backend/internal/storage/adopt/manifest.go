package adopt

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Manifest struct {
	ID             string   `json:"id"`
	Provider       string   `json:"provider"`
	Role           string   `json:"role"`
	RequiredTables []string `json:"required_tables"`
	Checksum       string   `json:"-"`
}

func LoadFile(path string) (Manifest, error) {
	contents, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	if err := json.Unmarshal(contents, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode adopt manifest: %w", err)
	}
	if manifest.ID == "" || manifest.Provider == "" || manifest.Role == "" || len(manifest.RequiredTables) == 0 {
		return Manifest{}, fmt.Errorf("adopt manifest is incomplete")
	}
	sum := sha256.Sum256(contents)
	manifest.Checksum = hex.EncodeToString(sum[:])
	return manifest, nil
}

func (m Manifest) Verify(id, checksum, provider, role string) error {
	if id != m.ID || checksum != m.Checksum {
		return fmt.Errorf("adopt manifest identity/checksum mismatch")
	}
	if provider != m.Provider || role != m.Role {
		return fmt.Errorf("adopt manifest scope mismatch")
	}
	return nil
}
