package filestore

import (
	"AgenticService/src/domain"
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestProjectRepositoryCreatesMemoryIsolatedProject(t *testing.T) {
	repository, err := NewProjectRepository(t.TempDir())
	if err != nil {
		t.Fatalf("NewProjectRepository: %v", err)
	}
	project, err := repository.Create(context.Background(), domain.CreateProjectInput{
		Name: "隔離工作", WorkspaceID: "workspace_1", Ephemeral: true, RAMDiskSizeMB: 1024,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !project.Ephemeral || project.RAMDiskSizeMB != 1024 || len(project.SandboxRoots) != 0 {
		t.Fatalf("project = %+v", project)
	}
	reloaded, err := NewProjectRepository(filepathDir(repository.filePath))
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	stored, err := reloaded.Get(context.Background(), project.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !stored.Ephemeral || stored.RAMDiskSizeMB != 1024 {
		t.Fatalf("stored project = %+v", stored)
	}
}

func TestProjectRepositoryRejectsHostRootsForMemoryIsolatedProject(t *testing.T) {
	repository, err := NewProjectRepository(t.TempDir())
	if err != nil {
		t.Fatalf("NewProjectRepository: %v", err)
	}
	_, err = repository.Create(context.Background(), domain.CreateProjectInput{
		Name: "錯誤隔離", WorkspaceID: "workspace_1", Ephemeral: true, RAMDiskSizeMB: 512,
		SandboxRoots: []string{t.TempDir()},
	})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("Create error = %v, want invalid input", err)
	}
}

// filepathDir 回到 Repository 的 dataDir；測試刻意走重新載入，避免只驗到記憶體內欄位。
func filepathDir(projectFilePath string) string {
	return filepath.Dir(filepath.Dir(projectFilePath))
}
