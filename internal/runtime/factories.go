package runtime

import (
	"errors"
	"fmt"
	"strings"

	"github.com/auron-labs/dnsync/internal/config"
	"github.com/auron-labs/dnsync/internal/contracts"
	adguardprovider "github.com/auron-labs/dnsync/internal/providers/adguard"
	cloudflareprovider "github.com/auron-labs/dnsync/internal/providers/cloudflare"
	dockerprovider "github.com/auron-labs/dnsync/internal/providers/docker"
	piholeprovider "github.com/auron-labs/dnsync/internal/providers/pihole"
	technitiumprovider "github.com/auron-labs/dnsync/internal/providers/technitium"
	telltalednsprovider "github.com/auron-labs/dnsync/internal/providers/telltaledns"
)

type SourceFactory func(config.SourceConfig, Deps) (contracts.Source, error)

type OutputFactory func(config.OutputConfig, Deps) (contracts.Output, error)

type FactoryRegistry struct {
	sourceFactories map[string]SourceFactory
	outputFactories map[string]OutputFactory
}

func NewFactoryRegistry() *FactoryRegistry {
	return &FactoryRegistry{
		sourceFactories: make(map[string]SourceFactory),
		outputFactories: make(map[string]OutputFactory),
	}
}

func NewDefaultFactoryRegistry() *FactoryRegistry {
	registry := NewFactoryRegistry()
	mustRegister(registry.RegisterSource("docker", func(cfg config.SourceConfig, deps Deps) (contracts.Source, error) {
		return dockerprovider.New(cfg, deps.Logger)
	}))
	mustRegister(registry.RegisterOutput("adguard", func(cfg config.OutputConfig, _ Deps) (contracts.Output, error) {
		return adguardprovider.New(cfg), nil
	}))
	mustRegister(registry.RegisterOutput("cloudflare", func(cfg config.OutputConfig, deps Deps) (contracts.Output, error) {
		return cloudflareprovider.New(cfg, deps.InstallationID)
	}))
	mustRegister(registry.RegisterOutput("telltaledns", func(cfg config.OutputConfig, _ Deps) (contracts.Output, error) {
		return telltalednsprovider.New(cfg), nil
	}))
	mustRegister(registry.RegisterOutput("pihole", func(cfg config.OutputConfig, _ Deps) (contracts.Output, error) {
		return piholeprovider.New(cfg), nil
	}))
	mustRegister(registry.RegisterOutput("technitium", func(cfg config.OutputConfig, deps Deps) (contracts.Output, error) {
		return technitiumprovider.New(cfg, deps.InstallationID)
	}))
	return registry
}

func (r *FactoryRegistry) RegisterSource(providerType string, factory SourceFactory) error {
	providerType = strings.TrimSpace(providerType)
	if providerType == "" {
		return errors.New("source provider type is required")
	}

	if factory == nil {
		return fmt.Errorf("source factory for %q is required", providerType)
	}

	if _, exists := r.sourceFactories[providerType]; exists {
		return fmt.Errorf("source provider type %q is already registered", providerType)
	}

	r.sourceFactories[providerType] = factory
	return nil
}

func (r *FactoryRegistry) RegisterOutput(providerType string, factory OutputFactory) error {
	providerType = strings.TrimSpace(providerType)
	if providerType == "" {
		return errors.New("output provider type is required")
	}

	if factory == nil {
		return fmt.Errorf("output factory for %q is required", providerType)
	}

	if _, exists := r.outputFactories[providerType]; exists {
		return fmt.Errorf("output provider type %q is already registered", providerType)
	}

	r.outputFactories[providerType] = factory
	return nil
}

func (r *FactoryRegistry) BuildProviders(cfg config.Config, deps Deps) ([]contracts.Source, []contracts.Output, error) {
	sources, err := r.BuildSources(cfg, deps)
	if err != nil {
		return nil, nil, err
	}

	outputs, err := r.BuildOutputs(cfg, deps)
	if err != nil {
		return nil, nil, err
	}

	return sources, outputs, nil
}

func (r *FactoryRegistry) BuildSources(cfg config.Config, deps Deps) ([]contracts.Source, error) {
	sources := make([]contracts.Source, 0, len(cfg.Sources))
	for i, sourceCfg := range cfg.Sources {
		factory, ok := r.sourceFactories[sourceCfg.Type]
		if !ok {
			return nil, fmt.Errorf("sources[%d]: unknown provider type %q", i, sourceCfg.Type)
		}

		source, err := factory(sourceCfg, deps)
		if err != nil {
			return nil, fmt.Errorf("sources[%d]: %w", i, err)
		}

		sources = append(sources, source)
	}

	return sources, nil
}

func (r *FactoryRegistry) BuildOutputs(cfg config.Config, deps Deps) ([]contracts.Output, error) {
	outputs := make([]contracts.Output, 0, len(cfg.Outputs))
	for i, outputCfg := range cfg.Outputs {
		if !outputCfg.IsEnabled() {
			continue
		}

		factory, ok := r.outputFactories[outputCfg.Type]
		if !ok {
			return nil, fmt.Errorf("outputs[%d]: unknown provider type %q", i, outputCfg.Type)
		}

		output, err := factory(outputCfg, deps)
		if err != nil {
			return nil, fmt.Errorf("outputs[%d]: %w", i, err)
		}

		outputs = append(outputs, output)
	}

	return outputs, nil
}

func mustRegister(err error) {
	if err != nil {
		panic(err)
	}
}
