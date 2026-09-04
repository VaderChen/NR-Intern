package bootstrap

import (
	"context"
	"log/slog"

	"AgenticService/src/adapters/filestore"
	"AgenticService/src/domain"
	"AgenticService/src/ports"
)

// ephemeralProjectRoots 把「Session 該放在哪個根目錄」接到 RAM disk 池。
//
// 歸屬資訊編碼在 Session ID 裡，所以解析不需要查詢 Project 儲存，也不需要
// 任何會過期的快取——Session 建立後不會換 Project（見
// Service.validateEphemeralSessionMove），編進去的代碼永遠成立。
type ephemeralProjectRoots struct{ pool *RAMDiskPool }

var _ filestore.ProjectRoots = ephemeralProjectRoots{}

func (r ephemeralProjectRoots) RootFor(sessionID string) string {
	code := domain.EphemeralProjectCodeFromSessionID(sessionID)
	if code == "" {
		return ""
	}
	return r.pool.RootForProjectCode(code)
}

func (r ephemeralProjectRoots) AdditionalRoots() []string {
	return r.pool.MountedRoots()
}

// newEphemeralSessionIDFactory 讓隔離專案的 Session 一建立就帶著歸屬。
//
// 查不到 Project、或該 Project 不是隔離的，就回空字串讓 filestore 用預設格式。
// 查詢失敗同樣退回預設：寧可讓對話落在 dataDir，也不要產生一個解析得出代碼、
// 卻對應不到任何 RAM disk 的 ID——那種 Session 之後每次存取都會失敗。
func newEphemeralSessionIDFactory(projects ports.ProjectRepository, logger *slog.Logger) filestore.SessionIDFactory {
	return func(projectID string) string {
		if projectID == "" {
			return ""
		}
		project, err := projects.Get(context.Background(), projectID)
		if err != nil {
			logger.Warn("could not resolve project for session id", "project_id", projectID, "error", err)
			return ""
		}
		if !project.Ephemeral {
			return ""
		}
		return domain.NewEphemeralSessionID(project.ID)
	}
}
