package application

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"context"
	"fmt"
	"strings"
)

func (s *Service) CreateProject(ctx context.Context, input domain.CreateProjectInput) (domain.Project, error) {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	if _, err := s.workspaces.Get(ctx, input.WorkspaceID); err != nil {
		return domain.Project{}, err
	}
	project, err := s.projects.Create(ctx, input)
	if err != nil {
		return domain.Project{}, err
	}
	if !project.Ephemeral {
		return project, nil
	}
	if !s.memoryIsolatedProjects.Load() {
		_ = s.projects.Delete(context.Background(), project.ID)
		return domain.Project{}, fmt.Errorf("%w: 記憶體隔離專案已在實驗性功能中關閉", domain.ErrConflict)
	}
	if s.ephemeralProjects == nil {
		_ = s.projects.Delete(context.Background(), project.ID)
		return domain.Project{}, fmt.Errorf("%w: memory-isolated project support is unavailable", domain.ErrConflict)
	}
	if _, err := s.ephemeralProjects.Prepare(ctx, project.ID, project.RAMDiskSizeMB); err != nil {
		_ = s.projects.Delete(context.Background(), project.ID)
		return domain.Project{}, fmt.Errorf("prepare memory-isolated project: %w", err)
	}
	return project, nil
}

// sessionInstructions 依 Workspace → Project 的順序收集職務說明。
//
// 這是「寫一次、每次對話都適用」的常駐指示：使用者不必在每個新對話重述工作規則，
// 排程建立的對話也會自動沿用所屬 Workspace 的說明。
// validateEphemeralSessionMove 擋下記憶體隔離專案的對話搬移。
//
// 搬出去等於把揮發資料變成永久資料，搬進來則是把已經寫在硬碟上的對話「當成」
// 揮發的——舊檔案其實還在。兩個方向都會讓使用者對「這個對話會不會留下」
// 產生錯誤認知，而這正是隔離專案唯一的賣點。
//
// 這個限制同時是儲存分流的前提：對話的根目錄在建立時決定且永不改變，
// 查詢時才不需要一份 sessionID 到專案的映射。
// ensureEphemeralProjectReady 在建立對話前確保隔離專案的 RAM disk 已掛載。
//
// 對話的儲存位置由 Session ID 決定，磁碟不在時根目錄解析會失敗（見
// filestore.resolveVolatileRoot）——那是刻意的，退回硬碟等於讓這個功能失效。
// 但「磁碟剛好沒掛」不該讓使用者無法開新對話，所以這裡先把它準備好；
// 真的掛不起來就明確擋下，絕不悄悄改用硬碟。
//
// StartRun 也會做同一件事（見組裝 sandbox 根目錄處），但那太晚了：對話目錄
// 在建立當下就寫出去了。
func (s *Service) ensureEphemeralProjectReady(ctx context.Context, projectID string) error {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil
	}
	project, err := s.projects.Get(ctx, projectID)
	if err != nil {
		return err
	}
	if !project.Ephemeral {
		return nil
	}
	if s.ephemeralProjects == nil {
		return fmt.Errorf("%w: memory-isolated project support is unavailable", domain.ErrConflict)
	}
	if _, err := s.ephemeralProjects.Prepare(ctx, project.ID, project.RAMDiskSizeMB); err != nil {
		return fmt.Errorf("prepare memory-isolated project: %w", err)
	}
	return nil
}

func (s *Service) validateEphemeralSessionMove(ctx context.Context, fromProjectID, toProjectID string) error {
	for _, projectID := range []string{fromProjectID, toProjectID} {
		if strings.TrimSpace(projectID) == "" {
			continue
		}
		project, err := s.projects.Get(ctx, projectID)
		if err != nil {
			return err
		}
		if project.Ephemeral {
			return fmt.Errorf("%w: 記憶體隔離專案的對話不能移動；它的工作檔案綁在該專案的 RAM Disk 上", domain.ErrConflict)
		}
	}
	return nil
}

func (s *Service) ListProjects(ctx context.Context) ([]domain.Project, error) {
	return s.projects.List(ctx)
}

func (s *Service) GetProject(ctx context.Context, projectID string) (domain.Project, error) {
	return s.projects.Get(ctx, strings.TrimSpace(projectID))
}

func (s *Service) UpdateProject(ctx context.Context, projectID string, input domain.UpdateProjectInput) (domain.Project, error) {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	return s.projects.Update(ctx, strings.TrimSpace(projectID), input)
}

// DeleteProject 只刪除空專案；Session 必須先移至其他專案或未分類，避免隱含級聯刪除。
// DeleteProject 刪除專案。force 為真時連同專案底下的對話一起刪除。
//
// 預設拒絕是對的：專案裡的對話是使用者的工作紀錄，不該因為刪一個分類就一起消失。
// 但拒絕之後使用者唯一的出路是手動一則一則刪，對話多的時候等於刪不掉——
// force 讓他在知道後果的前提下一次完成，而不是提高門檻逼他放棄。
func (s *Service) DeleteProject(ctx context.Context, projectID string, force bool) error {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	projectID = strings.TrimSpace(projectID)
	project, err := s.projects.Get(ctx, projectID)
	if err != nil {
		return err
	}
	type ownedSession struct {
		engine ports.AgentEngine
		id     string
	}
	owned := []ownedSession{}
	for _, engine := range s.registry.Engines() {
		sessions, err := engine.ListSessions(ctx)
		if err != nil {
			return err
		}
		for _, session := range sessions {
			if session.ProjectID != projectID {
				continue
			}
			if !force {
				return fmt.Errorf("%w: project still contains sessions", domain.ErrConflict)
			}
			// force 只允許級聯刪除，不允許在工作執行中卸載磁碟。
			if s.hasActiveSession(session.ID) {
				return fmt.Errorf("%w: project has a queued or running run", domain.ErrConflict)
			}
			owned = append(owned, ownedSession{engine: engine, id: session.ID})
		}
	}
	// 對話先刪、專案後刪：反過來的話中途失敗會留下一批指向不存在專案的孤兒對話，
	// 它們在側邊欄不屬於任何分組，使用者也找不到入口處理。
	for _, session := range owned {
		if err := s.deleteSessionLocked(ctx, session.engine, session.id); err != nil {
			return fmt.Errorf("刪除專案底下的對話 %s: %w", session.id, err)
		}
	}
	if project.Ephemeral && s.ephemeralProjects != nil {
		if err := s.ephemeralProjects.Release(ctx, projectID); err != nil {
			return fmt.Errorf("release memory-isolated project: %w", err)
		}
	}
	return s.projects.Delete(ctx, projectID)
}

func (s *Service) CreateWorkspace(ctx context.Context, input domain.CreateWorkspaceInput) (domain.Workspace, error) {
	input.ProviderIDs = normalizeStrings(input.ProviderIDs)
	input.DefaultProviderID = strings.TrimSpace(input.DefaultProviderID)
	if len(input.ProviderIDs) == 0 || input.DefaultProviderID == "" {
		return domain.Workspace{}, fmt.Errorf("%w: provider_ids and default_provider_id are required", domain.ErrInvalidInput)
	}
	for _, providerID := range input.ProviderIDs {
		if err := s.validateProvider(providerID); err != nil {
			return domain.Workspace{}, err
		}
	}
	if !contains(input.ProviderIDs, input.DefaultProviderID) {
		return domain.Workspace{}, fmt.Errorf("%w: default provider must belong to provider_ids", domain.ErrInvalidInput)
	}
	return s.workspaces.Create(ctx, input)
}

func (s *Service) ListWorkspaces(ctx context.Context) ([]domain.Workspace, error) {
	return s.workspaces.List(ctx)
}

func (s *Service) GetWorkspace(ctx context.Context, workspaceID string) (domain.Workspace, error) {
	return s.workspaces.Get(ctx, strings.TrimSpace(workspaceID))
}

func (s *Service) UpdateWorkspace(ctx context.Context, workspaceID string, input domain.UpdateWorkspaceInput) (domain.Workspace, error) {
	current, err := s.workspaces.Get(ctx, strings.TrimSpace(workspaceID))
	if err != nil {
		return domain.Workspace{}, err
	}
	providerIDs := current.ProviderIDs
	defaultProviderID := current.DefaultProviderID
	if input.ProviderIDs != nil {
		values := normalizeStrings(*input.ProviderIDs)
		input.ProviderIDs = &values
		providerIDs = values
	}
	for _, providerID := range providerIDs {
		if err := s.validateProvider(providerID); err != nil {
			return domain.Workspace{}, err
		}
	}
	if input.DefaultProviderID != nil {
		value := strings.TrimSpace(*input.DefaultProviderID)
		input.DefaultProviderID = &value
		defaultProviderID = value
	}
	if len(providerIDs) == 0 || !contains(providerIDs, defaultProviderID) {
		return domain.Workspace{}, fmt.Errorf("%w: default provider must belong to provider_ids", domain.ErrInvalidInput)
	}
	if err := s.validateProvider(defaultProviderID); err != nil {
		return domain.Workspace{}, err
	}
	return s.workspaces.Update(ctx, strings.TrimSpace(workspaceID), input)
}

func (s *Service) DeleteWorkspace(ctx context.Context, workspaceID string) error {
	workspaceID = strings.TrimSpace(workspaceID)
	if _, err := s.workspaces.Get(ctx, workspaceID); err != nil {
		return err
	}
	projects, err := s.projects.List(ctx)
	if err != nil {
		return err
	}
	for _, project := range projects {
		if project.WorkspaceID == workspaceID {
			return fmt.Errorf("%w: workspace still contains projects", domain.ErrConflict)
		}
	}
	for _, engine := range s.registry.Engines() {
		sessions, err := engine.ListSessions(ctx)
		if err != nil {
			return err
		}
		for _, session := range sessions {
			if session.WorkspaceID == workspaceID {
				return fmt.Errorf("%w: workspace still contains sessions", domain.ErrConflict)
			}
		}
	}
	values, err := s.workspaces.List(ctx)
	if err != nil {
		return err
	}
	if len(values) <= 1 {
		return fmt.Errorf("%w: cannot delete the last workspace", domain.ErrConflict)
	}
	return s.workspaces.Delete(ctx, workspaceID)
}
