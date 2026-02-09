package llm

import (
	"fmt"
	"log"
	"time"

	"genesis/pkg/config"

	jsoniter "github.com/json-iterator/go"
)

// NewFromConfig acts as a universal entry point for instantiating an LLM terminal
// client from raw JSON configuration. It automatically detects provider types,
// validates credentials, and applies engine-level technical parameters.
//
// Logic Flow:
//  1. Unmarshals raw JSON into a slice of ProviderGroupConfig.
//  2. Iterates through each group and retrieves the matching ProviderFactory from global registry.
//  3. Creates one or more atomic LLMClients (one per model/key combination) per group.
//  4. If multiple atomic clients are initialized, it wraps them into a single
//     FallbackClient with automatic retry and failover logic.
//
// Parameters:
//   - rawLLM: The raw "llm" section from the app config.
//   - system: System-level technical parameters (timeouts, retries).
//
// Returns:
//   - A single LLMClient (atomic or fallback) ready for use.
func NewFromConfig(rawLLM jsoniter.RawMessage, system *config.SystemConfig) (LLMClient, error) {
	var allAtomicClients []LLMClient

	if rawLLM == nil {
		return nil, fmt.Errorf("missing 'llm' config")
	}

	var groups []ProviderGroupConfig
	if err := jsoniter.Unmarshal(rawLLM, &groups); err != nil {
		return nil, fmt.Errorf("failed to parse 'llm' config: %v", err)
	}

	for _, group := range groups {
		log.Printf("Loading LLM Group: %s (%d models)", group.Type, len(group.Models))

		factory, ok := GetProviderFactory(group.Type)
		if !ok {
			log.Printf("Unknown provider type: %s", group.Type)
			continue
		}

		clients, err := factory.Create(group, system)
		if err != nil {
			log.Printf("Failed to create clients for %s: %v", group.Type, err)
			continue
		}

		allAtomicClients = append(allAtomicClients, clients...)
	}

	if len(allAtomicClients) == 0 {
		return nil, fmt.Errorf("no LLM clients could be initialized")
	}

	log.Printf("✅ Total atomic LLM clients initialized: %d", len(allAtomicClients))

	// If only one, return it directly
	if len(allAtomicClients) == 1 {
		return allAtomicClients[0], nil
	}

	// Otherwise wrap in a FallbackClient with system-level retry settings
	return &FallbackClient{
		Clients:    allAtomicClients,
		MaxRetries: system.MaxRetries,
		RetryDelay: time.Duration(system.RetryDelayMs) * time.Millisecond,
	}, nil
}
