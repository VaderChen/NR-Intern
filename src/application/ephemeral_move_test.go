package application

import (
	"context"
	"errors"
	"strings"
	"testing"

	"AgenticService/src/domain"
)

// stubProjects 讓測試能指定哪些 Project 是記憶體隔離的。
type stubProjects struct{ ephemeral map[string]bool }

func (stubProjects) Create(context.Context, domain.CreateProjectInput) (domain.Project, error) {
	return domain.Project{}, nil
}
func (stubProjects) List(context.Context) ([]domain.Project, error) { return nil, nil }
func (p stubProjects) Get(_ context.Context, id string) (domain.Project, error) {
	if _, exists := p.ephemeral[id]; !exists {
		return domain.Project{}, domain.ErrNotFound
	}
	return domain.Project{ID: id, Ephemeral: p.ephemeral[id]}, nil
}
func (stubProjects) Update(context.Context, string, domain.UpdateProjectInput) (domain.Project, error) {
	return domain.Project{}, nil
}
func (stubProjects) Delete(context.Context, string) error { return nil }

func moveValidator(t *testing.T) *Service {
	t.Helper()
	return &Service{projects: stubProjects{ephemeral: map[string]bool{
		"project_ram":    true,
		"project_ram_2":  true,
		"project_disk":   false,
		"project_disk_2": false,
	}}}
}

// 搬出去等於把揮發資料變成永久資料；搬進來則是把已經寫在硬碟上的對話
// 當成揮發的，舊檔案其實還在。兩個方向都會讓使用者對「這個對話會不會留下」
// 產生錯誤認知，而那正是隔離專案唯一的賣點。
func TestEphemeralSessionCannotMoveInOrOut(t *testing.T) {
	service := moveValidator(t)
	ctx := context.Background()
	cases := map[string][2]string{
		"搬出隔離專案":   {"project_ram", "project_disk"},
		"搬入隔離專案":   {"project_disk", "project_ram"},
		"隔離專案之間互搬": {"project_ram", "project_ram_2"},
		"搬出到未分類":   {"project_ram", ""},
		"從未分類搬入":   {"", "project_ram"},
	}
	for name, pair := range cases {
		err := service.validateEphemeralSessionMove(ctx, pair[0], pair[1])
		if err == nil {
			t.Fatalf("%s：應該被拒絕", name)
		}
		if !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("%s：錯誤型別應為 ErrConflict，實際 %v", name, err)
		}
		if !strings.Contains(err.Error(), "RAM Disk") {
			t.Fatalf("%s：訊息應說明原因，實際 %q", name, err)
		}
	}
}

// 一般專案之間的搬移是既有功能，不能被這個限制波及。
func TestNormalSessionMoveStillAllowed(t *testing.T) {
	service := moveValidator(t)
	ctx := context.Background()
	for _, pair := range [][2]string{
		{"project_disk", "project_disk_2"},
		{"project_disk", ""},
		{"", "project_disk"},
	} {
		if err := service.validateEphemeralSessionMove(ctx, pair[0], pair[1]); err != nil {
			t.Fatalf("%s → %s 應該允許，實際 %v", pair[0], pair[1], err)
		}
	}
}
