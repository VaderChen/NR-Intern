package filestore

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
)

const planFileVersion = 2

var _ ports.PlanRepository = (*PlanRepository)(nil)

type PlanRepository struct {
	mu   sync.RWMutex
	root string
	// roots 讓隔離專案的計畫跟著它的對話走，落在同一個 RAM disk 上。
	// 計畫內容含步驟敘述與驗證條件，與 transcript 同樣不該留在硬碟。
	roots atomic.Pointer[ProjectRoots]
}

// SetProjectRoots 注入根目錄解析；傳入 nil 會回到單一根目錄的行為。
func (r *PlanRepository) SetProjectRoots(roots ProjectRoots) {
	if r == nil {
		return
	}
	if roots == nil {
		r.roots.Store(nil)
		return
	}
	r.roots.Store(&roots)
}

// planRootFor 回傳指定 session 的計畫目錄。
//
// 額外的根底下再開一層 plans，讓 RAM disk 的結構與 dataDir 一致；
// 直接把檔案灑在根目錄會和其他後端資料混在一起。
//
// 磁碟不在時回傳錯誤而不是退回 dataDir，理由見 resolveVolatileRoot。
func (r *PlanRepository) planRootFor(sessionID string) (string, error) {
	root, routed, err := resolveVolatileRoot(&r.roots, sessionID, r.root)
	if err != nil {
		return "", err
	}
	if !routed {
		return root, nil
	}
	return filepath.Join(root, "plans"), nil
}

type planFile struct {
	Version   int           `json:"version"`
	SessionID string        `json:"session_id"`
	Plans     []domain.Plan `json:"plans"`
}

func NewPlanRepository(dataDir string) (*PlanRepository, error) {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return nil, fmt.Errorf("%w: data directory is required", domain.ErrInvalidInput)
	}
	root := filepath.Join(dataDir, "plans")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create plan store: %w", err)
	}
	return &PlanRepository{root: root}, nil
}

func (r *PlanRepository) List(ctx context.Context, sessionID string) ([]domain.Plan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := r.path(sessionID)
	if err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	values, err := r.read(path, sessionID)
	if errors.Is(err, os.ErrNotExist) {
		return []domain.Plan{}, nil
	}
	if err != nil {
		return nil, err
	}
	return values, nil
}

// Reconcile 讓計畫狀態跟 Session 的鎖定設定保持一致。舊版資料在讀取時
// 仍可維持原本的佇列格式，但一旦知道目前 Session 設定，就在這裡明確整理。
func (r *PlanRepository) Reconcile(ctx context.Context, sessionID string, locked bool) ([]domain.Plan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := r.path(sessionID)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	values, err := r.read(path, sessionID)
	if errors.Is(err, os.ErrNotExist) {
		return []domain.Plan{}, nil
	}
	if err != nil {
		return nil, err
	}
	if locked {
		values = normalizePlanQueue(values)
	} else {
		values = normalizeParallelPlans(values)
	}
	if err := r.write(path, sessionID, values); err != nil {
		return nil, err
	}
	return values, nil
}

func (r *PlanRepository) Get(ctx context.Context, sessionID, planID string) (domain.Plan, error) {
	values, err := r.List(ctx, sessionID)
	if err != nil {
		return domain.Plan{}, err
	}
	return planByID(values, strings.TrimSpace(planID))
}

func (r *PlanRepository) Create(ctx context.Context, value domain.Plan) (domain.Plan, error) {
	if err := ctx.Err(); err != nil {
		return domain.Plan{}, err
	}
	if err := domain.ValidatePlan(value); err != nil {
		return domain.Plan{}, err
	}
	path, err := r.path(value.SessionID)
	if err != nil {
		return domain.Plan{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	values, err := r.read(path, value.SessionID)
	if errors.Is(err, os.ErrNotExist) {
		values = []domain.Plan{}
	} else if err != nil {
		return domain.Plan{}, err
	}
	for _, existing := range values {
		if existing.ID == value.ID {
			return domain.Plan{}, fmt.Errorf("%w: duplicate plan id %q", domain.ErrConflict, value.ID)
		}
	}
	values = normalizePlanQueue(append(values, value))
	if err := r.write(path, value.SessionID, values); err != nil {
		return domain.Plan{}, err
	}
	return planByID(values, value.ID)
}

func (r *PlanRepository) Update(ctx context.Context, value domain.Plan) (domain.Plan, error) {
	if err := ctx.Err(); err != nil {
		return domain.Plan{}, err
	}
	if err := domain.ValidatePlan(value); err != nil {
		return domain.Plan{}, err
	}
	path, err := r.path(value.SessionID)
	if err != nil {
		return domain.Plan{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	values, err := r.read(path, value.SessionID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return domain.Plan{}, fmt.Errorf("%w: plan %q", domain.ErrNotFound, value.ID)
		}
		return domain.Plan{}, err
	}
	updated := false
	for index := range values {
		if values[index].ID == value.ID {
			values[index] = value
			updated = true
			break
		}
	}
	if !updated {
		return domain.Plan{}, fmt.Errorf("%w: plan %q", domain.ErrNotFound, value.ID)
	}
	values = normalizePlanQueue(values)
	if err := r.write(path, value.SessionID, values); err != nil {
		return domain.Plan{}, err
	}
	return planByID(values, value.ID)
}

func (r *PlanRepository) Delete(ctx context.Context, sessionID, planID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := r.path(sessionID)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	values, err := r.read(path, sessionID)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	planID = strings.TrimSpace(planID)
	filtered := make([]domain.Plan, 0, len(values))
	for _, value := range values {
		if value.ID != planID {
			filtered = append(filtered, value)
		}
	}
	if len(filtered) == len(values) {
		return nil
	}
	if len(filtered) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("delete plans: %w", err)
		}
		return nil
	}
	filtered = normalizePlanQueue(filtered)
	return r.write(path, sessionID, filtered)
}

func (r *PlanRepository) DeleteSession(ctx context.Context, sessionID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := r.path(sessionID)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete plans: %w", err)
	}
	return nil
}

func (r *PlanRepository) Reorder(ctx context.Context, sessionID string, planIDs []string) ([]domain.Plan, error) {
	return r.ReorderWithPolicy(ctx, sessionID, planIDs, true)
}

// ReorderWithPolicy 依 Session 的計畫鎖定設定排序。保留 Reorder 的鎖定預設，
// 維持既有 Repository 呼叫端的相容行為。
func (r *PlanRepository) ReorderWithPolicy(ctx context.Context, sessionID string, planIDs []string, locked bool) ([]domain.Plan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := r.path(sessionID)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	values, err := r.read(path, sessionID)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: plans for session %q", domain.ErrNotFound, sessionID)
	}
	if err != nil {
		return nil, err
	}
	if !locked {
		values = normalizeParallelPlans(values)
	}
	if len(planIDs) != len(values) {
		return nil, fmt.Errorf("%w: plan order must contain every plan exactly once", domain.ErrInvalidInput)
	}
	byID := make(map[string]domain.Plan, len(values))
	for _, value := range values {
		byID[value.ID] = value
	}
	reordered := make([]domain.Plan, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, rawID := range planIDs {
		planID := strings.TrimSpace(rawID)
		value, exists := byID[planID]
		if !exists {
			return nil, fmt.Errorf("%w: unknown plan id %q", domain.ErrInvalidInput, planID)
		}
		if _, duplicate := seen[planID]; duplicate {
			return nil, fmt.Errorf("%w: duplicate plan id %q", domain.ErrInvalidInput, planID)
		}
		seen[planID] = struct{}{}
		reordered = append(reordered, value)
	}
	active := currentActivePlan(values)
	first := firstUnfinishedPlan(reordered)
	if locked && active != nil && first != nil && active.ID != first.ID && domain.PlanHasProgress(*active) {
		return nil, fmt.Errorf("%w: plan %q has already started and must remain before queued plans", domain.ErrConflict, active.Title)
	}
	if locked {
		reordered = normalizePlanQueue(reordered)
	} else {
		reordered = normalizeParallelPlans(reordered)
	}
	if err := r.write(path, sessionID, reordered); err != nil {
		return nil, err
	}
	return reordered, nil
}

func (r *PlanRepository) read(path, sessionID string) ([]domain.Plan, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		return nil, fmt.Errorf("read plans: %w", err)
	}
	var envelope struct {
		Plans json.RawMessage `json:"plans"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("decode plans: %w", err)
	}
	var values []domain.Plan
	if envelope.Plans != nil {
		var file planFile
		if err := json.Unmarshal(data, &file); err != nil {
			return nil, fmt.Errorf("decode plans: %w", err)
		}
		if strings.TrimSpace(file.SessionID) != "" && file.SessionID != strings.TrimSpace(sessionID) {
			return nil, fmt.Errorf("decode plans: session mismatch")
		}
		values = file.Plans
	} else {
		var legacy domain.Plan
		if err := json.Unmarshal(data, &legacy); err != nil {
			return nil, fmt.Errorf("decode legacy plan: %w", err)
		}
		values = []domain.Plan{legacy}
	}
	for _, value := range values {
		if value.SessionID != strings.TrimSpace(sessionID) {
			return nil, fmt.Errorf("decode plans: session mismatch")
		}
		if err := domain.ValidatePlan(value); err != nil {
			return nil, fmt.Errorf("decode plans: %w", err)
		}
	}
	return values, nil
}

func (r *PlanRepository) write(path, sessionID string, values []domain.Plan) error {
	for _, value := range values {
		if err := domain.ValidatePlan(value); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(planFile{Version: planFileVersion, SessionID: strings.TrimSpace(sessionID), Plans: values}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode plans: %w", err)
	}
	temporary, err := os.CreateTemp(r.root, ".plans-*.tmp")
	if err != nil {
		return fmt.Errorf("create plans temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("secure plans temporary file: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write plans: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close plans: %w", err)
	}
	if err := replaceFile(temporaryPath, path); err != nil {
		return fmt.Errorf("replace plans: %w", err)
	}
	return nil
}

func normalizePlanQueue(values []domain.Plan) []domain.Plan {
	normalized := append([]domain.Plan(nil), values...)
	activeAssigned := false
	for index := range normalized {
		normalized[index].Position = index
		if domain.PlanIsTerminal(normalized[index]) {
			continue
		}
		if !activeAssigned {
			normalized[index].Status = domain.PlanStatusActive
			activeAssigned = true
		} else {
			normalized[index].Status = domain.PlanStatusQueued
		}
	}
	return normalized
}

func normalizeParallelPlans(values []domain.Plan) []domain.Plan {
	normalized := append([]domain.Plan(nil), values...)
	for index := range normalized {
		normalized[index].Position = index
		if !domain.PlanIsTerminal(normalized[index]) {
			normalized[index].Status = domain.PlanStatusActive
		}
	}
	return normalized
}

func currentActivePlan(values []domain.Plan) *domain.Plan {
	for index := range values {
		if values[index].Status == domain.PlanStatusActive {
			return &values[index]
		}
	}
	return nil
}

func firstUnfinishedPlan(values []domain.Plan) *domain.Plan {
	for index := range values {
		if !domain.PlanIsTerminal(values[index]) {
			return &values[index]
		}
	}
	return nil
}

func planByID(values []domain.Plan, planID string) (domain.Plan, error) {
	for _, value := range values {
		if value.ID == planID {
			return value, nil
		}
	}
	return domain.Plan{}, fmt.Errorf("%w: plan %q", domain.ErrNotFound, planID)
}

func (r *PlanRepository) path(sessionID string) (string, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", fmt.Errorf("%w: session id is required", domain.ErrInvalidInput)
	}
	name := base64.RawURLEncoding.EncodeToString([]byte(sessionID)) + ".json"
	root, err := r.planRootFor(sessionID)
	if err != nil {
		return "", err
	}
	// 隔離專案的計畫目錄要用時才建：RAM disk 可能還沒掛載，啟動時一律建立
	// 會在磁碟不存在時失敗。
	if root != r.root {
		if err := os.MkdirAll(root, 0o700); err != nil {
			return "", fmt.Errorf("create plan store: %w", err)
		}
	}
	return filepath.Join(root, name), nil
}
