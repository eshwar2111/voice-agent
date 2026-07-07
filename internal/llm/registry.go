package llm

import "sync"

type Factory func(key, model, baseURL string) Provider

var (
	mu        sync.RWMutex
	providers = make(map[string]Factory)
)

func Register(name string, factory Factory) {
	mu.Lock()
	defer mu.Unlock()
	providers[name] = factory
}

func AvailableProviders() []string {
	mu.RLock()
	defer mu.RUnlock()
	var names []string
	for k := range providers {
		names = append(names, k)
	}
	return names
}

func createProviderFromRegistry(name, key, model, baseURL string) (Provider, bool) {
	mu.RLock()
	defer mu.RUnlock()
	factory, ok := providers[name]
	if !ok {
		return nil, false
	}
	return factory(key, model, baseURL), true
}
