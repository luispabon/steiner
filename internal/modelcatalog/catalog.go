package modelcatalog

import (
	"net/http"
	"sort"
	"sync"

	"github.com/luispabon/steiner/internal/config"
)

// EnumeratorDispatcher creates an enumerator for a provider type.
type EnumeratorDispatcher func(providerType string, client *http.Client) (Enumerator, error)

// Service combines provider model discovery, configured model definitions, and
// model popularity into a catalog.
type Service struct {
	dispatcher       EnumeratorDispatcher
	cache            *Cache
	popularity       *Store
	client           *http.Client
	DiscoveryEnabled bool
	mu               sync.RWMutex
	discovered       map[string][]DiscoveredModel
}

// NewService creates a model catalog service. discoveryEnabled defaults to true
// when omitted.
func NewService(dispatcher EnumeratorDispatcher, cache *Cache, popularity *Store, client *http.Client, discoveryEnabled ...bool) *Service {
	if dispatcher == nil {
		dispatcher = func(providerType string, client *http.Client) (Enumerator, error) {
			return ForTypeWithClient(config.ProviderType(providerType), client)
		}
	}
	if cache == nil {
		cache = NewCache("")
	}
	if popularity == nil {
		popularity = NewStore("")
	}
	if client == nil {
		client = http.DefaultClient
	}
	enabled := true
	if len(discoveryEnabled) > 0 {
		enabled = discoveryEnabled[0]
	}
	return &Service{
		dispatcher:       dispatcher,
		cache:            cache,
		popularity:       popularity,
		client:           client,
		DiscoveryEnabled: enabled,
		discovered:       make(map[string][]DiscoveredModel),
	}
}

// Choices returns configured and discovered model choices, ranked for display.
func (s *Service) Choices(cfg *config.Config, activeRef string) []ModelChoice {
	if cfg == nil {
		return nil
	}

	counts := s.popularity.Snapshot()
	activeAlias, activeID, _ := config.ParseModelReference(cfg, activeRef)
	discovered := s.discoveredModels(cfg)
	choices, defined := configuredChoices(cfg, activeAlias, activeID, counts, discovered)
	choices = append(choices, discoveredChoices(discovered, defined, activeAlias, activeID, counts)...)
	return rankChoices(choices)
}

func (s *Service) discoveredModels(cfg *config.Config) map[Key]DiscoveredModel {
	discovered := make(map[Key]DiscoveredModel)
	if !s.DiscoveryEnabled {
		return discovered
	}
	aliases := make([]string, 0, len(cfg.Providers))
	for alias := range cfg.Providers {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	for _, alias := range aliases {
		provider := cfg.Providers[alias]
		s.mu.RLock()
		models, refreshed := s.discovered[alias]
		s.mu.RUnlock()
		if !refreshed {
			var found bool
			models, found, _ = s.cache.Load(alias, string(provider.Type), provider.BaseURL)
			if !found {
				continue
			}
		}
		for _, model := range models {
			if model.ID != "" {
				discovered[Key{ProviderAlias: alias, ModelID: model.ID}] = model
			}
		}
	}
	return discovered
}

func (s *Service) setDiscovered(alias string, models []DiscoveredModel) {
	cloned := append([]DiscoveredModel(nil), models...)
	s.mu.Lock()
	s.discovered[alias] = cloned
	s.mu.Unlock()
}

func configuredChoices(cfg *config.Config, activeAlias, activeID string, counts map[Key]int, discovered map[Key]DiscoveredModel) ([]ModelChoice, map[Key]struct{}) {
	aliases := make([]string, 0, len(cfg.Models.Definitions))
	for alias := range cfg.Models.Definitions {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	choices := make([]ModelChoice, 0, len(cfg.Models.Definitions)+len(discovered))
	defined := make(map[Key]struct{}, len(cfg.Models.Definitions))
	for _, alias := range aliases {
		definition := cfg.Models.Definitions[alias]
		if definition.Provider == "" || definition.ID == "" {
			continue
		}
		key := Key{ProviderAlias: definition.Provider, ModelID: definition.ID}
		defined[key] = struct{}{}
		model := discovered[key]
		display := definition.Provider + "/" + alias
		efforts := model.SupportedEfforts
		if definition.Advanced.Reasoning.SupportedEfforts != nil {
			efforts = definition.Advanced.Reasoning.SupportedEfforts
		}
		choices = append(choices, ModelChoice{
			Ref:              alias,
			Display:          display,
			ProviderAlias:    definition.Provider,
			ModelID:          definition.ID,
			Aliased:          true,
			SwitchCount:      counts[key],
			SupportedEfforts: efforts,
			Current:          definition.Provider == activeAlias && definition.ID == activeID,
		})
	}
	return choices, defined
}

func discoveredChoices(discovered map[Key]DiscoveredModel, defined map[Key]struct{}, activeAlias, activeID string, counts map[Key]int) []ModelChoice {
	discoveredKeys := make([]Key, 0, len(discovered))
	for key := range discovered {
		discoveredKeys = append(discoveredKeys, key)
	}
	sort.Slice(discoveredKeys, func(i, j int) bool {
		if discoveredKeys[i].ProviderAlias != discoveredKeys[j].ProviderAlias {
			return discoveredKeys[i].ProviderAlias < discoveredKeys[j].ProviderAlias
		}
		return discoveredKeys[i].ModelID < discoveredKeys[j].ModelID
	})
	choices := make([]ModelChoice, 0, len(discoveredKeys))
	for _, key := range discoveredKeys {
		if _, ok := defined[key]; ok {
			continue
		}
		model := discovered[key]
		display := key.ProviderAlias + "/" + key.ModelID
		choices = append(choices, ModelChoice{
			Ref:              key.ProviderAlias + "/" + key.ModelID,
			Display:          display,
			ProviderAlias:    key.ProviderAlias,
			ModelID:          key.ModelID,
			SwitchCount:      counts[key],
			SupportedEfforts: model.SupportedEfforts,
			Current:          key.ProviderAlias == activeAlias && key.ModelID == activeID,
		})
	}
	return choices
}

func rankChoices(choices []ModelChoice) []ModelChoice {
	sort.SliceStable(choices, func(i, j int) bool {
		if choices[i].SwitchCount != choices[j].SwitchCount {
			return choices[i].SwitchCount > choices[j].SwitchCount
		}
		if choices[i].Aliased != choices[j].Aliased {
			return choices[i].Aliased
		}
		return choices[i].Display < choices[j].Display
	})
	return choices
}
