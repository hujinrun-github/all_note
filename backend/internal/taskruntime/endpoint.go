package taskruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/hujinrun/flowspace/internal/airuntime"
	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/tenantmigration"
)

var ErrDatabaseEndpointUnavailable = errors.New("task runtime database endpoint is unavailable")

// DatabaseEndpointConfig is already decrypted and validated by its source.
// The task runtime factory consumes no control-store repository or general
// profile API, keeping endpoint resolution as a narrow injected capability.
type DatabaseEndpointConfig struct {
	WorkspaceID      string
	EndpointID       string
	ProfileVersionID string
	Storage          storage.Config
}

type DatabaseEndpointConfigSource interface {
	LoadDatabaseEndpointConfig(context.Context, string, string) (DatabaseEndpointConfig, error)
}

type endpointProfileSource interface {
	LoadEndpointProfile(context.Context, string, string, string) (airuntime.EndpointProfile, error)
}

// ProfileDatabaseEndpointConfigSource adapts immutable verified control-plane
// profile versions into storage.Config. It never initializes a database.
type ProfileDatabaseEndpointConfigSource struct {
	profiles    endpointProfileSource
	environment string
}

func NewProfileDatabaseEndpointConfigSource(profiles endpointProfileSource, environment string) (*ProfileDatabaseEndpointConfigSource, error) {
	if profiles == nil || strings.TrimSpace(environment) == "" {
		return nil, errors.New("database endpoint profile source and environment are required")
	}
	return &ProfileDatabaseEndpointConfigSource{profiles: profiles, environment: strings.TrimSpace(environment)}, nil
}

func (s *ProfileDatabaseEndpointConfigSource) LoadDatabaseEndpointConfig(ctx context.Context, workspaceID, endpointID string) (DatabaseEndpointConfig, error) {
	if s == nil || s.profiles == nil || strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(endpointID) == "" {
		return DatabaseEndpointConfig{}, ErrDatabaseEndpointUnavailable
	}
	profile, err := s.profiles.LoadEndpointProfile(ctx, workspaceID, "data_store", endpointID)
	if err != nil {
		return DatabaseEndpointConfig{}, fmt.Errorf("%w: load immutable profile", ErrDatabaseEndpointUnavailable)
	}
	if profile.EndpointID != endpointID || profile.Kind != "data_store" || strings.TrimSpace(profile.ProfileVersionID) == "" {
		return DatabaseEndpointConfig{}, fmt.Errorf("%w: profile identity mismatch", ErrDatabaseEndpointUnavailable)
	}

	result := DatabaseEndpointConfig{
		WorkspaceID: workspaceID, EndpointID: endpointID, ProfileVersionID: profile.ProfileVersionID,
	}
	switch strings.ToLower(strings.TrimSpace(profile.Provider)) {
	case string(storage.DriverPostgres):
		cfg, err := postgresStorageConfig(s.environment, profile.ConfigJSON, profile.Secret)
		if err != nil {
			return DatabaseEndpointConfig{}, fmt.Errorf("%w: invalid PostgreSQL profile", ErrDatabaseEndpointUnavailable)
		}
		result.Storage = cfg
	case string(storage.DriverSQLite):
		cfg, err := sqliteStorageConfig(s.environment, profile.ConfigJSON, profile.Secret)
		if err != nil {
			return DatabaseEndpointConfig{}, fmt.Errorf("%w: invalid SQLite profile", ErrDatabaseEndpointUnavailable)
		}
		result.Storage = cfg
	default:
		return DatabaseEndpointConfig{}, fmt.Errorf("%w: unsupported provider", ErrDatabaseEndpointUnavailable)
	}
	return result, nil
}

func postgresStorageConfig(environment, configJSON string, secret []byte) (storage.Config, error) {
	var profile struct {
		Endpoint string `json:"endpoint"`
		Schema   string `json:"schema"`
	}
	if err := json.Unmarshal([]byte(configJSON), &profile); err != nil {
		return storage.Config{}, err
	}
	if strings.TrimSpace(profile.Schema) == "" {
		profile.Schema = "public"
	}
	endpoint, err := tenantmigration.ParsePostgresEndpoint(profile.Endpoint, profile.Schema)
	if err != nil {
		return storage.Config{}, err
	}
	parsed, err := url.Parse(endpoint.URL)
	if err != nil || parsed.User == nil || strings.TrimSpace(parsed.User.Username()) == "" {
		return storage.Config{}, errors.New("PostgreSQL username is required")
	}
	if _, embedded := parsed.User.Password(); embedded {
		return storage.Config{}, errors.New("embedded PostgreSQL password is forbidden")
	}
	if len(secret) > 0 {
		parsed.User = url.UserPassword(parsed.User.Username(), string(secret))
	}
	endpoint.URL = parsed.String()
	runtimeURL, err := endpoint.URLWithSchema()
	if err != nil {
		return storage.Config{}, err
	}
	cfg := storage.Config{Env: environment, Driver: storage.DriverPostgres, URL: runtimeURL, Name: endpoint.Database}
	if err := storage.ValidateStorageConfig(cfg); err != nil {
		return storage.Config{}, err
	}
	return cfg, nil
}

func sqliteStorageConfig(environment, configJSON string, secret []byte) (storage.Config, error) {
	if len(secret) != 0 {
		return storage.Config{}, errors.New("SQLite profile must not contain a secret")
	}
	var profile struct {
		Path       string `json:"path"`
		SQLitePath string `json:"sqlite_path"`
	}
	if err := json.Unmarshal([]byte(configJSON), &profile); err != nil {
		return storage.Config{}, err
	}
	path := strings.TrimSpace(profile.Path)
	if path == "" {
		path = strings.TrimSpace(profile.SQLitePath)
	}
	if path == "" {
		return storage.Config{}, errors.New("SQLite path is required")
	}
	cfg := storage.Config{Env: environment, Driver: storage.DriverSQLite, SQLitePath: filepath.Clean(path), Name: filepath.Base(path)}
	if err := storage.ValidateStorageConfig(cfg); err != nil {
		return storage.Config{}, err
	}
	return cfg, nil
}

var _ DatabaseEndpointConfigSource = (*ProfileDatabaseEndpointConfigSource)(nil)
