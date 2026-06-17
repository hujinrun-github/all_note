package config

import (
	"os"
	"strings"
)

const (
	ProductionPort = "8080"
	TestPort       = "18080"
)

type ServerConfig struct {
	Port string
}

func LoadServerConfig(environment string) ServerConfig {
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = defaultPort(environment)
	}

	return ServerConfig{Port: port}
}

func defaultPort(environment string) string {
	if environment == EnvironmentTest {
		return TestPort
	}
	return ProductionPort
}
