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

var _ ports.ProjectRepository = (*ProjectRepository)(nil)

const maxInstructionCharacters = 8000

type ProjectRepository struct {
	mu       sync.RWMutex
	filePath string
	items    map[string]domain.Project
}

type projectFile struct {
	Version int                       `json:"version"`
	Items   map[string]domain.Project `json:"items"`
}

func NewProjectRepository(dataDir string) (*ProjectRepository, error) {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return nil, fmt.Errorf("%w: data directory is required", domain.ErrInvalidInput)
	}
	root := filepath.Join(dataDir, "projects")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create project store: %w", err)
	}
	repository := &ProjectRepository{
		filePath: filepath.Join(root, "projects.json"),
		items:    map[string]domain.Project{},
	}
	if err := repository.load(); err != nil {
		return nil, err
	}
	return repository, nil
}

func (r *ProjectRepository) Create(ctx context.Context, input domain.CreateProjectInput) (domain.Project, error) {
	if err := ctx.Err(); err != nil {
		return domain.Project{}, err
	}
	name := strings.TrimSpace(input.Name)
	workspaceID := strings.TrimSpace(input.WorkspaceID)
	if name == "" || workspaceID == "" {
		return domain.Project{}, fmt.Errorf("%w: project name and workspace id are required", domain.ErrInvalidInput)
	}
	sandboxRoots, err := normalizeProjectSandboxRoots(input.SandboxRoots)
	if err != nil {
		return domain.Project{}, err
	}
	instructions, err := normalizeInstructions(input.Instructions)
	if err != nil {
		return domain.Project{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.nameExistsLocked(workspaceID, name, "") {
		return domain.Project{}, fmt.Errorf("%w: project name %q already exists", domain.ErrConflict, name)
	}
	position := 100
	for _, item := range r.items {
		if item.WorkspaceID == workspaceID && item.Position >= position {
			position = item.Position + 100
		}
	}
	now := time.Now().UTC()
	value := domain.Project{
		ID:           domain.NewID("project"),
		WorkspaceID:  workspaceID,
		Name:         name,
		Description:  strings.TrimSpace(input.Description),
		Instructions: instructions,
		SandboxRoots: sandboxRoots,
		Position:     position,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	previous := cloneProjects(r.items)
	r.items[value.ID] = value
	if err := r.persistLocked(); err != nil {
		r.items = previous
		return domain.Project{}, err
	}
	return cloneProject(value), nil
}

func (r *ProjectRepository) List(ctx context.Context) ([]domain.Project, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	values := make([]domain.Project, 0, len(r.items))
	for _, value := range r.items {
		values = append(values, cloneProject(value))
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

func (r *ProjectRepository) Get(ctx context.Context, projectID string) (domain.Project, error) {
	if err := ctx.Err(); err != nil {
		return domain.Project{}, err
	}
	r.mu.RLock()
	value, exists := r.items[strings.TrimSpace(projectID)]
	r.mu.RUnlock()
	if !exists {
		return domain.Project{}, fmt.Errorf("%w: project %q", domain.ErrNotFound, projectID)
	}
	return cloneProject(value), nil
}

func (r *ProjectRepository) Update(ctx context.Context, projectID string, input domain.UpdateProjectInput) (domain.Project, error) {
	if err := ctx.Err(); err != nil {
		return domain.Project{}, err
	}
	if input.Name == nil && input.Description == nil && input.Instructions == nil && input.SandboxRoots == nil && input.Position == nil {
		return domain.Project{}, fmt.Errorf("%w: at least one project field is required", domain.ErrInvalidInput)
	}
	projectID = strings.TrimSpace(projectID)
	r.mu.Lock()
	defer r.mu.Unlock()
	value, exists := r.items[projectID]
	if !exists {
		return domain.Project{}, fmt.Errorf("%w: project %q", domain.ErrNotFound, projectID)
	}
	if input.Name != nil {
		name := strings.TrimSpace(*input.Name)
		if name == "" {
			return domain.Project{}, fmt.Errorf("%w: project name cannot be empty", domain.ErrInvalidInput)
		}
		if r.nameExistsLocked(value.WorkspaceID, name, projectID) {
			return domain.Project{}, fmt.Errorf("%w: project name %q already exists", domain.ErrConflict, name)
		}
		value.Name = name
	}
	if input.Description != nil {
		value.Description = strings.TrimSpace(*input.Description)
	}
	if input.Instructions != nil {
		instructions, err := normalizeInstructions(*input.Instructions)
		if err != nil {
			return domain.Project{}, err
		}
		value.Instructions = instructions
	}
	if input.SandboxRoots != nil {
		sandboxRoots, err := normalizeProjectSandboxRoots(*input.SandboxRoots)
		if err != nil {
			return domain.Project{}, err
		}
		value.SandboxRoots = sandboxRoots
	}
	if input.Position != nil {
		if *input.Position < 0 {
			return domain.Project{}, fmt.Errorf("%w: project position cannot be negative", domain.ErrInvalidInput)
		}
		value.Position = *input.Position
	}
	value.UpdatedAt = time.Now().UTC()
	previous := cloneProjects(r.items)
	r.items[projectID] = value
	if err := r.persistLocked(); err != nil {
		r.items = previous
		return domain.Project{}, err
	}
	return cloneProject(value), nil
}

func (r *ProjectRepository) Delete(ctx context.Context, projectID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	projectID = strings.TrimSpace(projectID)
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.items[projectID]; !exists {
		return fmt.Errorf("%w: project %q", domain.ErrNotFound, projectID)
	}
	previous := cloneProjects(r.items)
	delete(r.items, projectID)
	if err := r.persistLocked(); err != nil {
		r.items = previous
		return err
	}
	return nil
}

func (r *ProjectRepository) load() error {
	data, err := os.ReadFile(r.filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read project store: %w", err)
	}
	var snapshot projectFile
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return fmt.Errorf("decode project store: %w", err)
	}
	if snapshot.Items != nil {
		for id, value := range snapshot.Items {
			sandboxRoots, normalizeErr := normalizeProjectSandboxRoots(value.SandboxRoots)
			if normalizeErr != nil {
				return fmt.Errorf("validate stored project %q: %w", id, normalizeErr)
			}
			value.SandboxRoots = sandboxRoots
			snapshot.Items[id] = value
		}
		r.items = snapshot.Items
	}
	return nil
}

func (r *ProjectRepository) persistLocked() error {
	data, err := json.MarshalIndent(projectFile{Version: 2, Items: r.items}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode project store: %w", err)
	}
	temporary := r.filePath + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return fmt.Errorf("write project store: %w", err)
	}
	if err := replaceFile(temporary, r.filePath); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("replace project store: %w", err)
	}
	return nil
}

func (r *ProjectRepository) nameExistsLocked(workspaceID, name, excludedID string) bool {
	for id, value := range r.items {
		if id != excludedID && value.WorkspaceID == workspaceID && strings.EqualFold(strings.TrimSpace(value.Name), name) {
			return true
		}
	}
	return false
}

func cloneProjects(values map[string]domain.Project) map[string]domain.Project {
	cloned := make(map[string]domain.Project, len(values))
	for key, value := range values {
		cloned[key] = cloneProject(value)
	}
	return cloned
}

func cloneProject(value domain.Project) domain.Project {
	value.SandboxRoots = append([]string(nil), value.SandboxRoots...)
	return value
}

// normalizeInstructions 是 Workspace 與 Project 職務說明的共用檢查。
// 上限存在的理由是這段文字每一輪都會進入提示，不設限會安靜地吃掉 context 預算。
func normalizeInstructions(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len([]rune(value)) > maxInstructionCharacters {
		return "", fmt.Errorf("%w: instructions cannot exceed %d characters", domain.ErrInvalidInput, maxInstructionCharacters)
	}
	return value, nil
}

func normalizeProjectSandboxRoots(values []string) ([]string, error) {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if !filepath.IsAbs(value) {
			return nil, fmt.Errorf("%w: sandbox root must be an absolute path: %q", domain.ErrInvalidInput, value)
		}
		resolved, err := filepath.EvalSymlinks(filepath.Clean(value))
		if err != nil {
			return nil, fmt.Errorf("%w: resolve sandbox root %q: %v", domain.ErrInvalidInput, value, err)
		}
		resolved, err = filepath.Abs(resolved)
		if err != nil {
			return nil, fmt.Errorf("%w: resolve sandbox root %q: %v", domain.ErrInvalidInput, value, err)
		}
		resolved = filepath.Clean(resolved)
		if filepath.Dir(resolved) == resolved {
			return nil, fmt.Errorf("%w: filesystem root cannot be used as a project sandbox", domain.ErrInvalidInput)
		}
		info, err := os.Stat(resolved)
		if err != nil {
			return nil, fmt.Errorf("%w: inspect sandbox root %q: %v", domain.ErrInvalidInput, value, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("%w: sandbox root is not a directory: %q", domain.ErrInvalidInput, value)
		}
		if _, exists := seen[resolved]; exists {
			continue
		}
		seen[resolved] = struct{}{}
		result = append(result, resolved)
	}
	return result, nil
}
