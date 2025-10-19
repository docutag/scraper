package main

import (
	"os"
	"testing"
)

func TestGetEnv(t *testing.T) {
	tests := []struct {
		name         string
		key          string
		defaultValue string
		envValue     string
		setEnv       bool
		want         string
	}{
		{
			name:         "environment variable set",
			key:          "TEST_VAR",
			defaultValue: "default",
			envValue:     "custom",
			setEnv:       true,
			want:         "custom",
		},
		{
			name:         "environment variable not set",
			key:          "TEST_VAR_NOT_SET",
			defaultValue: "default",
			envValue:     "",
			setEnv:       false,
			want:         "default",
		},
		{
			name:         "environment variable set to empty string",
			key:          "TEST_VAR_EMPTY",
			defaultValue: "default",
			envValue:     "",
			setEnv:       true,
			want:         "default",
		},
		{
			name:         "both empty",
			key:          "TEST_VAR_BOTH_EMPTY",
			defaultValue: "",
			envValue:     "",
			setEnv:       false,
			want:         "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clean up any existing env var
			os.Unsetenv(tt.key)

			// Set env var if needed
			if tt.setEnv {
				os.Setenv(tt.key, tt.envValue)
				defer os.Unsetenv(tt.key)
			}

			got := getEnv(tt.key, tt.defaultValue)
			if got != tt.want {
				t.Errorf("getEnv(%q, %q) = %q, want %q", tt.key, tt.defaultValue, got, tt.want)
			}
		})
	}
}

func TestGetEnvWithRealEnvVars(t *testing.T) {
	tests := []struct {
		name         string
		envVars      map[string]string
		key          string
		defaultValue string
		want         string
	}{
		{
			name: "PORT environment variable",
			envVars: map[string]string{
				"PORT": "9090",
			},
			key:          "PORT",
			defaultValue: "8080",
			want:         "9090",
		},
		{
			name: "DB_PATH environment variable",
			envVars: map[string]string{
				"DB_PATH": "/custom/path/db.sqlite",
			},
			key:          "DB_PATH",
			defaultValue: "scraper.db",
			want:         "/custom/path/db.sqlite",
		},
		{
			name: "OLLAMA_URL environment variable",
			envVars: map[string]string{
				"OLLAMA_URL": "http://ollama-server:11434",
			},
			key:          "OLLAMA_URL",
			defaultValue: "http://localhost:11434",
			want:         "http://ollama-server:11434",
		},
		{
			name: "OLLAMA_MODEL environment variable",
			envVars: map[string]string{
				"OLLAMA_MODEL": "llama3.1",
			},
			key:          "OLLAMA_MODEL",
			defaultValue: "gpt-oss:20b",
			want:         "llama3.1",
		},
		{
			name: "OLLAMA_VISION_MODEL environment variable",
			envVars: map[string]string{
				"OLLAMA_VISION_MODEL": "llama3.2-vision:latest",
			},
			key:          "OLLAMA_VISION_MODEL",
			defaultValue: "gpt-oss:20b",
			want:         "llama3.2-vision:latest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up environment variables
			for k, v := range tt.envVars {
				os.Setenv(k, v)
				defer os.Unsetenv(k)
			}

			got := getEnv(tt.key, tt.defaultValue)
			if got != tt.want {
				t.Errorf("getEnv(%q, %q) = %q, want %q", tt.key, tt.defaultValue, got, tt.want)
			}
		})
	}
}

func TestGetEnvPrecedence(t *testing.T) {
	// Test that environment variable takes precedence over default
	key := "TEST_PRECEDENCE"
	envValue := "from_environment"
	defaultValue := "from_default"

	os.Setenv(key, envValue)
	defer os.Unsetenv(key)

	result := getEnv(key, defaultValue)
	if result != envValue {
		t.Errorf("Expected environment variable to take precedence. Got %q, want %q", result, envValue)
	}

	// Verify default is used when env var is not set
	os.Unsetenv(key)
	result = getEnv(key, defaultValue)
	if result != defaultValue {
		t.Errorf("Expected default value when env var not set. Got %q, want %q", result, defaultValue)
	}
}
