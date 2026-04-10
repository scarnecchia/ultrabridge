package appconfig

import (
	"os"
)

// applyEnvOverrides overlays environment variables on top of DB values.
// For each key with a corresponding env var, the env var value replaces the DB value.
func applyEnvOverrides(vals map[string]string) {
	for key, envVar := range envVarForKey {
		if val, ok := os.LookupEnv(envVar); ok {
			vals[key] = val
		}
	}
}
