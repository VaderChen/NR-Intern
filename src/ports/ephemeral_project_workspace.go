package ports

import "context"

// EphemeralProjectWorkspace 把平台 RAM Disk 實作隔離在 Bootstrap；Application
// 只需要取得可信任的專案根目錄，不應知道磁碟掛載命令與裝置名稱。
type EphemeralProjectWorkspace interface {
	Prepare(ctx context.Context, projectID string, sizeMB int) (string, error)
	StageFile(ctx context.Context, projectID, sourcePath, fileName string) (string, error)
	Release(ctx context.Context, projectID string) error
}
