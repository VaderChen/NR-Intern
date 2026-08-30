package filestore

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var _ ports.WorkspaceRepository = (*WorkspaceRepository)(nil)

type WorkspaceRepository struct {
	mu       sync.RWMutex
	filePath string
	items    map[string]domain.Workspace
}

type workspaceFile struct {
	Version int                         `json:"version"`
	Items   map[string]domain.Workspace `json:"items"`
}

func NewWorkspaceRepository(dataDir string) (*WorkspaceRepository, error) {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return nil, fmt.Errorf("%w: data directory is required", domain.ErrInvalidInput)
	}
	root := filepath.Join(dataDir, "workspaces")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create workspace store: %w", err)
	}
	repository := &WorkspaceRepository{
		filePath: filepath.Join(root, "workspaces.json"),
		items:    map[string]domain.Workspace{},
	}
	if err := repository.load(); err != nil {
		return nil, err
	}
	return repository, nil
}

func (r *WorkspaceRepository) Create(ctx context.Context, input domain.CreateWorkspaceInput) (domain.Workspace, error) {
	if err := ctx.Err(); err != nil {
		return domain.Workspace{}, err
	}
	name := strings.TrimSpace(input.Name)
	providerIDs := normalizeWorkspaceProviderIDs(input.ProviderIDs)
	defaultProviderID := strings.TrimSpace(input.DefaultProviderID)
	if defaultProviderID == "" && len(providerIDs) > 0 {
		defaultProviderID = providerIDs[0]
	}
	if name == "" || len(providerIDs) == 0 || !containsString(providerIDs, defaultProviderID) {
		return domain.Workspace{}, fmt.Errorf("%w: workspace name, provider ids and a default provider in that set are required", domain.ErrInvalidInput)
	}
	instructions, err := normalizeInstructions(input.Instructions)
	if err != nil {
		return domain.Workspace{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.nameExistsLocked(name, "") {
		return domain.Workspace{}, fmt.Errorf("%w: workspace name %q already exists", domain.ErrConflict, name)
	}
	position := 100
	for _, item := range r.items {
		if item.Position >= position {
			position = item.Position + 100
		}
	}
	now := time.Now().UTC()
	value := domain.Workspace{
		ID:                domain.NewID("workspace"),
		Name:              name,
		Description:       strings.TrimSpace(input.Description),
		Instructions:      instructions,
		ProviderIDs:       providerIDs,
		DefaultProviderID: defaultProviderID,
		Model:             strings.TrimSpace(input.Model),
		Position:          position,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	previous := cloneWorkspaces(r.items)
	r.items[value.ID] = value
	if err := r.persistLocked(); err != nil {
		r.items = previous
		return domain.Workspace{}, err
	}
	return cloneWorkspace(value), nil
}

func (r *WorkspaceRepository) List(ctx context.Context) ([]domain.Workspace, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	values := make([]domain.Workspace, 0, len(r.items))
	for _, value := range r.items {
		values = append(values, cloneWorkspace(value))
	}
	r.mu.RUnlock()
	sort.SliceStable(values, func(i, j int) bool {
		if values[i].Position == values[j].Position {
			return strings.ToLower(values[i].Name) < strings.ToLower(values[j].Name)
		}
		return values[i].Position < values[j].Position
	})
	return values, nil
}

func (r *WorkspaceRepository) Get(ctx context.Context, workspaceID string) (domain.Workspace, error) {
	if err := ctx.Err(); err != nil {
		return domain.Workspace{}, err
	}
	r.mu.RLock()
	value, exists := r.items[strings.TrimSpace(workspaceID)]
	r.mu.RUnlock()
	if !exists {
		return domain.Workspace{}, fmt.Errorf("%w: workspace %q", domain.ErrNotFound, workspaceID)
	}
	return cloneWorkspace(value), nil
}

func (r *WorkspaceRepository) Update(ctx context.Context, workspaceID string, input domain.UpdateWorkspaceInput) (domain.Workspace, error) {
	if err := ctx.Err(); err != nil {
		return domain.Workspace{}, err
	}
	if input.Name == nil && input.Description == nil && input.Instructions == nil && input.ProviderIDs == nil && input.DefaultProviderID == nil && input.Model == nil && input.Position == nil {
		return domain.Workspace{}, fmt.Errorf("%w: at least one workspace field is required", domain.ErrInvalidInput)
	}
	workspaceID = strings.TrimSpace(workspaceID)
	r.mu.Lock()
	defer r.mu.Unlock()
	value, exists := r.items[workspaceID]
	if !exists {
		return domain.Workspace{}, fmt.Errorf("%w: workspace %q", domain.ErrNotFound, workspaceID)
	}
	if input.Name != nil {
		name := strings.TrimSpace(*input.Name)
		if name == "" {
			return domain.Workspace{}, fmt.Errorf("%w: workspace name cannot be empty", domain.ErrInvalidInput)
		}
		if r.nameExistsLocked(name, workspaceID) {
			return domain.Workspace{}, fmt.Errorf("%w: workspace name %q already exists", domain.ErrConflict, name)
		}
		value.Name = name
	}
	if input.Description != nil {
		value.Description = strings.TrimSpace(*input.Description)
	}
	if input.Instructions != nil {
		instructions, err := normalizeInstructions(*input.Instructions)
		if err != nil {
			return domain.Workspace{}, err
		}
		value.Instructions = instructions
	}
	if input.ProviderIDs != nil {
		value.ProviderIDs = normalizeWorkspaceProviderIDs(*input.ProviderIDs)
		if len(value.ProviderIDs) == 0 {
			return domain.Workspace{}, fmt.Errorf("%w: provider ids cannot be empty", domain.ErrInvalidInput)
		}
	}
	if input.DefaultProviderID != nil {
		value.DefaultProviderID = strings.TrimSpace(*input.DefaultProviderID)
	}
	if !containsString(value.ProviderIDs, value.DefaultProviderID) {
		return domain.Workspace{}, fmt.Errorf("%w: default provider must belong to provider ids", domain.ErrInvalidInput)
	}
	if input.Model != nil {
		value.Model = strings.TrimSpace(*input.Model)
	}
	if input.Position != nil {
		if *input.Position < 0 {
			return domain.Workspace{}, fmt.Errorf("%w: workspace position cannot be negative", domain.ErrInvalidInput)
		}
		value.Position = *input.Position
	}
	value.UpdatedAt = time.Now().UTC()
	previous := cloneWorkspaces(r.items)
	r.items[workspaceID] = value
	if err := r.persistLocked(); err != nil {
		r.items = previous
		return domain.Workspace{}, err
	}
	return cloneWorkspace(value), nil
}

func (r *WorkspaceRepository) Delete(ctx context.Context, workspaceID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	workspaceID = strings.TrimSpace(workspaceID)
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.items[workspaceID]; !exists {
		return fmt.Errorf("%w: workspace %q", domain.ErrNotFound, workspaceID)
	}
	previous := cloneWorkspaces(r.items)
	delete(r.items, workspaceID)
	if err := r.persistLocked(); err != nil {
		r.items = previous
		return err
	}
	return nil
}

func (r *WorkspaceRepository) load() error {
	data, err := os.ReadFile(r.filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read workspace store: %w", err)
	}
	var snapshot workspaceFile
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return fmt.Errorf("decode workspace store: %w", err)
	}
	if snapshot.Items != nil {
		r.items = snapshot.Items
	}
	return nil
}

func (r *WorkspaceRepository) persistLocked() error {
	data, err := json.MarshalIndent(workspaceFile{Version: 1, Items: r.items}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode workspace store: %w", err)
	}
	temporary := r.filePath + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return fmt.Errorf("write workspace store: %w", err)
	}
	if err := replaceFile(temporary, r.filePath); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("replace workspace store: %w", err)
	}
	return nil
}

func (r *WorkspaceRepository) nameExistsLocked(name, excludedID string) bool {
	for id, value := range r.items {
		if id != excludedID && strings.EqualFold(strings.TrimSpace(value.Name), name) {
			return true
		}
	}
	return false
}

func cloneWorkspaces(values map[string]domain.Workspace) map[string]domain.Workspace {
	cloned := make(map[string]domain.Workspace, len(values))
	for key, value := range values {
		cloned[key] = cloneWorkspace(value)
	}
	return cloned
}

func cloneWorkspace(value domain.Workspace) domain.Workspace {
	value.ProviderIDs = append([]string(nil), value.ProviderIDs...)
	return value
}

func normalizeWorkspaceProviderIDs(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
