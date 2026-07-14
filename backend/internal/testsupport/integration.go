package testsupport

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// EnvLookup mirrors os.Getenv and keeps integration policy deterministic in tests.
type EnvLookup func(string) string

// IntegrationTargetFromEnv reports whether an integration target is available.
func IntegrationTargetFromEnv(target, valueEnv, requiredEnv string, getenv EnvLookup) (string, bool, error) {
	value := strings.TrimSpace(getenv(valueEnv))
	requiredValue := strings.TrimSpace(getenv(requiredEnv))
	required := false
	if requiredValue != "" {
		parsed, err := strconv.ParseBool(requiredValue)
		if err != nil {
			return "", false, fmt.Errorf("%s must be a boolean: %w", requiredEnv, err)
		}
		required = parsed
	}
	if value != "" {
		return value, true, nil
	}
	if required {
		return "", false, fmt.Errorf("%s is required for %s integration tests", valueEnv, target)
	}
	return "", false, nil
}

// IntegrationTarget uses the process environment and is intended for test harnesses.
func IntegrationTarget(target, valueEnv, requiredEnv string) (string, bool, error) {
	return IntegrationTargetFromEnv(target, valueEnv, requiredEnv, os.Getenv)
}
