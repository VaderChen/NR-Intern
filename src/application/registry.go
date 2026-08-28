package application

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"fmt"
	"sort"
	"strings"
	"sync"
)

type Registry struct {
	mu      sync.RWMutex
	engines map[string]ports.AgentEngine
}

func NewRegistry(engines ...ports.AgentEngine) (*Registry, error) {
	registry := &Registry{engines: map[string]ports.AgentEngine{}}
	for _, engine := range engines {
		if err := registry.Register(engine); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

func (r *Registry) Register(engine ports.AgentEngine) error {
	if engine == nil {
		return fmt.Errorf("%w: agent engine is nil", domain.ErrInvalidInput)
	}
	descriptor := engine.Descriptor()
	id := strings.TrimSpace(descriptor.ID)
	if id == "" {
		return fmt.Errorf("%w: agent id is required", domain.ErrInvalidInput)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.engines[id]; exists {
		return fmt.Errorf("%w: agent %q already registered", domain.ErrConflict, id)
	}
	r.engines[id] = engine
	return nil
}

func (r *Registry) Get(id string) (ports.AgentEngine, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	engine := r.engines[strings.TrimSpace(id)]
	if engine == nil {
		return nil, fmt.Errorf("%w: agent %q", domain.ErrNotFound, id)
	}
	return engine, nil
}

func (r *Registry) List() []domain.AgentDescriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]domain.AgentDescriptor, 0, len(r.engines))
	for _, engine := range r.engines {
		items = append(items, engine.Descriptor())
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items
}

func (r *Registry) Engines() []ports.AgentEngine {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]ports.AgentEngine, 0, len(r.engines))
	for _, engine := range r.engines {
		items = append(items, engine)
	}
	return items
}
