package agent

import "sync"

type Memory struct {
	mu      sync.RWMutex
	context map[string]string
}

func NewMemory() *Memory {
	return &Memory{
		context: make(map[string]string),
	}
}

func (m *Memory) Set(key string, value string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.context[key] = value
}

func (m *Memory) Get(key string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	val, exists := m.context[key]
	return val, exists
}

func (m *Memory) GetAll() map[string]string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Return a copy to avoid concurrent maps read/write issues downstream
	copyMap := make(map[string]string)
	for k, v := range m.context {
		copyMap[k] = v
	}
	return copyMap
}
