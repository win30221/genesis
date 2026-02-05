package llm

import (
	"fmt"
	"log"
	"time"

	"genesis/pkg/config"

	jsoniter "github.com/json-iterator/go"
)

// NewFromConfig 根據設定檔建立 LLM Client
func NewFromConfig(rawLLM jsoniter.RawMessage, system *config.SystemConfig) (LLMClient, error) {
	var allAtomicClients []LLMClient

	if rawLLM == nil {
		return nil, fmt.Errorf("missing 'llm' config")
	}

	var groups []ProviderGroupConfig
	if err := json.Unmarshal(rawLLM, &groups); err != nil {
		return nil, fmt.Errorf("failed to parse 'llm' config: %v", err)
	}

	for _, group := range groups {
		log.Printf("Loading LLM Group: %s (%d models)", group.Type, len(group.Models))

		factory, ok := GetProviderFactory(group.Type)
		if !ok {
			log.Printf("⚠️ Unknown provider type: %s", group.Type)
			continue
		}

		clients, err := factory.Create(group, system)
		if err != nil {
			log.Printf("⚠️ Failed to create clients for %s: %v", group.Type, err)
			continue
		}

		allAtomicClients = append(allAtomicClients, clients...)
	}

	if len(allAtomicClients) == 0 {
		return nil, fmt.Errorf("no LLM clients could be initialized")
	}

	log.Printf("✅ Total atomic LLM clients initialized: %d", len(allAtomicClients))

	// 如果只有一個，直接回傳
	if len(allAtomicClients) == 1 {
		return allAtomicClients[0], nil
	}

	// 否則包裹在 FallbackClient 中，並代入系統層級的重試設定
	return &FallbackClient{
		Clients:    allAtomicClients,
		MaxRetries: system.MaxRetries,
		RetryDelay: time.Duration(system.RetryDelayMs) * time.Millisecond,
	}, nil
}
