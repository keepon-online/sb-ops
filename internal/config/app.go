package config

import "strings"

const DefaultConfigPath = "config.yaml"

func NormalizeConfigPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return DefaultConfigPath
	}
	return path
}
