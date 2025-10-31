package config

import (
	"log"
	"os"
	"strconv"
)

// GetString retrieves an environment variable or returns a fallback when unset.
func GetString(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

// GetInt retrieves an environment variable as integer or returns fallback.
func GetInt(key string, fallback int) int {
	if value, ok := os.LookupEnv(key); ok {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			log.Printf("invalid value for %s: %v", key, err)
			return fallback
		}
		return parsed
	}
	return fallback
}

// GetBool retrieves an environment variable as bool or returns fallback.
func GetBool(key string, fallback bool) bool {
	if value, ok := os.LookupEnv(key); ok {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			log.Printf("invalid value for %s: %v", key, err)
			return fallback
		}
		return parsed
	}
	return fallback
}
