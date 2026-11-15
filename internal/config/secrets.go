package config

import (
	"errors"
	"fmt"
	"strings"
)

const envRefPrefix = "ENV:"

type LookupEnvFunc func(string) (string, bool)

func ResolveSecrets(cfg Config, lookup LookupEnvFunc) (Config, error) {
	if lookup == nil {
		return Config{}, errors.New("secret lookup is required")
	}

	resolved := cfg
	resolved.Outputs = append([]OutputConfig(nil), cfg.Outputs...)

	for i := range resolved.Outputs {
		if !resolved.Outputs[i].IsEnabled() {
			continue
		}

		output := &resolved.Outputs[i]
		for _, secret := range []struct {
			field      string
			ref, value *string
		}{
			{field: "password_ref", ref: &output.PasswordRef, value: &output.Password},
			{field: "api_key_ref", ref: &output.APIKeyRef, value: &output.APIKey},
			{field: "api_token_ref", ref: &output.APITokenRef, value: &output.APIToken},
		} {
			ref := strings.TrimSpace(*secret.ref)
			if ref == "" {
				continue
			}

			name, err := parseEnvRef(ref)
			if err != nil {
				return Config{}, fmt.Errorf("outputs[%d].%s: %w", i, secret.field, err)
			}

			value, ok := lookup(name)
			if !ok || strings.TrimSpace(value) == "" {
				return Config{}, fmt.Errorf("outputs[%d].%s references an unset environment variable", i, secret.field)
			}

			*secret.value = value
			*secret.ref = ""
		}
	}

	return resolved, nil
}

func parseEnvRef(ref string) (string, error) {
	if !strings.HasPrefix(ref, envRefPrefix) {
		return "", fmt.Errorf("must start with %q", envRefPrefix)
	}

	name := strings.TrimSpace(strings.TrimPrefix(ref, envRefPrefix))
	if name == "" {
		return "", errors.New("must include an environment variable name")
	}

	return name, nil
}
