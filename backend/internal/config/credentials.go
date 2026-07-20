package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type CredentialsConfig struct {
	ActiveKeyID string
	KeyringFile string
}

func LoadCredentialsConfig() (CredentialsConfig, error) {
	config := CredentialsConfig{
		ActiveKeyID: strings.TrimSpace(os.Getenv("FLOWSPACE_CREDENTIALS_ACTIVE_KEY_ID")),
		KeyringFile: strings.TrimSpace(os.Getenv("FLOWSPACE_CREDENTIALS_KEYRING_FILE")),
	}
	if config.ActiveKeyID == "" || config.KeyringFile == "" {
		return CredentialsConfig{}, errors.New("FLOWSPACE_CREDENTIALS_ACTIVE_KEY_ID and FLOWSPACE_CREDENTIALS_KEYRING_FILE are required")
	}
	info, err := os.Stat(config.KeyringFile)
	if err != nil {
		return CredentialsConfig{}, fmt.Errorf("credential keyring file: %w", err)
	}
	if info.IsDir() {
		return CredentialsConfig{}, errors.New("credential keyring path must be a file")
	}
	return config, nil
}

func (cfg CredentialsConfig) SafeSummary() string {
	return fmt.Sprintf("active_key_id=%s keyring_file=%s", cfg.ActiveKeyID, filepath.Base(cfg.KeyringFile))
}
