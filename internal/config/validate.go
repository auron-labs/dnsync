package config

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"
)

func Validate(cfg Config) error {
	if len(cfg.Sources) == 0 {
		return errors.New("at least one source must be configured")
	}

	if len(cfg.Outputs) == 0 {
		return errors.New("at least one output must be configured")
	}

	for i, source := range cfg.Sources {
		if strings.TrimSpace(source.Type) == "" {
			return fmt.Errorf("sources[%d].type is required", i)
		}

		if strings.TrimSpace(source.Name) == "" {
			return fmt.Errorf("sources[%d].name is required", i)
		}

		endpoint := strings.TrimSpace(source.Endpoint)
		if endpoint == "" {
			return fmt.Errorf("sources[%d].endpoint is required", i)
		}

		if !strings.HasPrefix(endpoint, "unix://") && !strings.HasPrefix(endpoint, "tcp://") {
			return fmt.Errorf("sources[%d].endpoint must use unix:// or tcp://", i)
		}

		hostIP := strings.TrimSpace(source.HostIP)
		if hostIP != "" {
			if _, err := netip.ParseAddr(hostIP); err != nil {
				return fmt.Errorf("sources[%d].host_ip must be a valid IP address", i)
			}
		}

		if baseDomain := normalizeDomainName(source.BaseDomain); strings.TrimSpace(source.BaseDomain) != "" && !isValidDomainName(baseDomain) {
			return fmt.Errorf("sources[%d].base_domain must be a valid domain name", i)
		}
	}

	enabledTunnels := 0
	for i, output := range cfg.Outputs {
		if strings.TrimSpace(output.Type) == "" {
			return fmt.Errorf("outputs[%d].type is required", i)
		}

		if strings.TrimSpace(output.Name) == "" {
			return fmt.Errorf("outputs[%d].name is required", i)
		}

		if !output.IsEnabled() {
			if output.Tunnel != nil {
				return fmt.Errorf("outputs[%d].tunnel cannot be enabled on a disabled output", i)
			}
			continue
		}

		switch strings.ToLower(strings.TrimSpace(output.Type)) {
		case "adguard":
			if strings.TrimSpace(output.URL) == "" {
				return fmt.Errorf("outputs[%d].url is required", i)
			}

			if strings.TrimSpace(output.Username) == "" {
				return fmt.Errorf("outputs[%d].username is required", i)
			}

			hasPassword := strings.TrimSpace(output.Password) != ""
			hasPasswordRef := strings.TrimSpace(output.PasswordRef) != ""
			if hasPassword == hasPasswordRef {
				return fmt.Errorf("outputs[%d] must set exactly one of password or password_ref", i)
			}
		case "cloudflare":
			if strings.TrimSpace(output.ZoneID) == "" {
				return fmt.Errorf("outputs[%d].zone_id is required", i)
			}

			hasAPIKey := strings.TrimSpace(output.APIKey) != ""
			hasAPIKeyRef := strings.TrimSpace(output.APIKeyRef) != ""
			if hasAPIKey == hasAPIKeyRef {
				return fmt.Errorf("outputs[%d] must set exactly one of api_key or api_key_ref", i)
			}
			if output.Tunnel != nil {
				enabledTunnels++
				if enabledTunnels > 1 {
					return errors.New("exactly one enabled cloudflare output may configure a tunnel")
				}
				if err := validateTunnelConfig(cfg, i, *output.Tunnel); err != nil {
					return err
				}
			}
		default:
			if output.Tunnel != nil {
				return fmt.Errorf("outputs[%d].tunnel is only supported for cloudflare outputs", i)
			}
			return fmt.Errorf("outputs[%d].type %q is not supported", i, output.Type)
		}
	}

	if strings.TrimSpace(cfg.State.Path) == "" {
		return errors.New("state.path is required")
	}

	level := strings.ToLower(strings.TrimSpace(cfg.Logging.Level))
	switch level {
	case "trace", "debug", "info", "warn", "error":
	default:
		return errors.New("logging.level must be one of trace, debug, info, warn, error")
	}

	format := strings.ToLower(strings.TrimSpace(cfg.Logging.Format))
	switch format {
	case "json", "text":
	default:
		return errors.New("logging.format must be one of json or text")
	}
	if cfg.Logging.MaxSizeMB < 0 {
		return errors.New("logging.max_size_mb must be greater than zero")
	}
	if cfg.Logging.MaxBackups < 0 {
		return errors.New("logging.max_backups must be greater than zero")
	}

	if strings.TrimSpace(cfg.Runtime.OperationTimeout) != "" {
		if err := validateDuration("runtime.operation_timeout", cfg.Runtime.OperationTimeout); err != nil {
			return err
		}
	}
	if strings.TrimSpace(cfg.Runtime.RemovalGracePeriod) != "" {
		if err := validateNonNegativeDuration("runtime.removal_grace_period", cfg.Runtime.RemovalGracePeriod); err != nil {
			return err
		}
	}

	if err := validateDuration("retry.initial_interval", cfg.Retry.InitialInterval); err != nil {
		return err
	}

	if err := validateDuration("retry.max_interval", cfg.Retry.MaxInterval); err != nil {
		return err
	}

	if err := validateDuration("retry.max_elapsed_time", cfg.Retry.MaxElapsedTime); err != nil {
		return err
	}

	return nil
}

func validateTunnelConfig(cfg Config, outputIndex int, tunnel TunnelConfig) error {
	if strings.TrimSpace(tunnel.AccountID) == "" {
		return fmt.Errorf("outputs[%d].tunnel.account_id is required", outputIndex)
	}
	if strings.TrimSpace(tunnel.Name) == "" {
		return fmt.Errorf("outputs[%d].tunnel.name is required", outputIndex)
	}
	sourceName := strings.TrimSpace(tunnel.Source)
	if sourceName == "" {
		return fmt.Errorf("outputs[%d].tunnel.source is required", outputIndex)
	}

	matches := 0
	for _, source := range cfg.Sources {
		if strings.EqualFold(strings.TrimSpace(source.Type), "docker") && strings.TrimSpace(source.Name) == sourceName {
			matches++
		}
	}
	if matches != 1 {
		return fmt.Errorf("outputs[%d].tunnel.source must identify exactly one docker source", outputIndex)
	}
	if strings.TrimSpace(tunnel.ConnectorImage) != "" && strings.ContainsAny(tunnel.ConnectorImage, "\r\n") {
		return fmt.Errorf("outputs[%d].tunnel.connector_image must be a valid single-line image reference", outputIndex)
	}
	return nil
}

func normalizeDomainName(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	return strings.TrimSuffix(value, ".")
}

func isValidDomainName(value string) bool {
	if value == "" || strings.HasPrefix(value, ".") || strings.Contains(value, "..") {
		return false
	}

	for _, label := range strings.Split(value, ".") {
		if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}

		for _, ch := range label {
			if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && ch != '-' {
				return false
			}
		}
	}

	return true
}

func validateDuration(field string, raw string) error {
	value := strings.TrimSpace(raw)
	if value == "" {
		return fmt.Errorf("%s is required", field)
	}

	duration, err := time.ParseDuration(value)
	if err != nil {
		return fmt.Errorf("%s must be a valid duration: %w", field, err)
	}

	if duration <= 0 {
		return fmt.Errorf("%s must be greater than zero", field)
	}

	return nil
}

func validateNonNegativeDuration(field string, raw string) error {
	value := strings.TrimSpace(raw)
	duration, err := time.ParseDuration(value)
	if err != nil {
		return fmt.Errorf("%s must be a valid duration: %w", field, err)
	}
	if duration < 0 {
		return fmt.Errorf("%s must be zero or greater", field)
	}
	return nil
}
