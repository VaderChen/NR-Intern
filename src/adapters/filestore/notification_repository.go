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
)

var _ ports.NotificationRepository = (*NotificationRepository)(nil)

const maxStoredNotifications = 1000

type NotificationRepository struct {
	mu       sync.RWMutex
	filePath string
	items    map[string]domain.Notification
}

type notificationFile struct {
	Version    int                            `json:"version"`
	Items      map[string]domain.Notification `json:"items"`
	DedupeKeys map[string]string              `json:"dedupe_keys,omitempty"`
}

func NewNotificationRepository(dataDir string) (*NotificationRepository, error) {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return nil, fmt.Errorf("%w: data directory is required", domain.ErrInvalidInput)
	}
	root := filepath.Join(dataDir, "notifications")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create notification store: %w", err)
	}
	r := &NotificationRepository{filePath: filepath.Join(root, "notifications.json"), items: map[string]domain.Notification{}}
	if err := r.load(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *NotificationRepository) Save(ctx context.Context, value domain.Notification) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(value.ID) == "" || strings.TrimSpace(value.Title) == "" || strings.TrimSpace(value.Message) == "" {
		return fmt.Errorf("%w: notification id, title and message are required", domain.ErrInvalidInput)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if value.DedupeKey != "" {
		for _, existing := range r.items {
			if existing.DedupeKey == value.DedupeKey {
				return nil
			}
		}
	}
	if value.Metadata != nil {
		value.Metadata = cloneAnyMap(value.Metadata)
	}
	r.items[value.ID] = value
	values := make([]domain.Notification, 0, len(r.items))
	for _, item := range r.items {
		values = append(values, item)
	}
	sort.Slice(values, func(i, j int) bool { return values[i].CreatedAt.Before(values[j].CreatedAt) })
	for len(values) > maxStoredNotifications {
		delete(r.items, values[0].ID)
		values = values[1:]
	}
	return r.persistLocked()
}

func (r *NotificationRepository) List(ctx context.Context, limit int, unreadOnly bool) ([]domain.Notification, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	values := make([]domain.Notification, 0, len(r.items))
	for _, item := range r.items {
		if unreadOnly && item.Read {
			continue
		}
		item.Metadata = cloneAnyMap(item.Metadata)
		values = append(values, item)
	}
	r.mu.RUnlock()
	sort.SliceStable(values, func(i, j int) bool { return values[i].CreatedAt.After(values[j].CreatedAt) })
	if limit <= 0 || limit > maxStoredNotifications {
		limit = maxStoredNotifications
	}
	if len(values) > limit {
		values = values[:limit]
	}
	return values, nil
}

func (r *NotificationRepository) MarkRead(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	value, ok := r.items[strings.TrimSpace(id)]
	if !ok {
		return fmt.Errorf("%w: notification %q", domain.ErrNotFound, id)
	}
	value.Read = true
	r.items[value.ID] = value
	return r.persistLocked()
}

func (r *NotificationRepository) MarkAllRead(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, value := range r.items {
		value.Read = true
		r.items[id] = value
	}
	return r.persistLocked()
}

func (r *NotificationRepository) DeleteRead(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, value := range r.items {
		if value.Read {
			delete(r.items, id)
		}
	}
	return r.persistLocked()
}

// DeleteSession 避免揮發性對話消失後，通知中心仍留下無法開啟的 Session 連結。
func (r *NotificationRepository) DeleteSession(ctx context.Context, sessionID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("%w: session id is required", domain.ErrInvalidInput)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	previous := make(map[string]domain.Notification, len(r.items))
	for id, item := range r.items {
		previous[id] = item
		if item.SessionID == sessionID {
			delete(r.items, id)
		}
	}
	if len(previous) == len(r.items) {
		return nil
	}
	if err := r.persistLocked(); err != nil {
		r.items = previous
		return err
	}
	return nil
}

func (r *NotificationRepository) load() error {
	data, err := os.ReadFile(r.filePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read notification store: %w", err)
	}
	var snapshot notificationFile
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return fmt.Errorf("decode notification store: %w", err)
	}
	if snapshot.Items != nil {
		r.items = snapshot.Items
	}
	for id, key := range snapshot.DedupeKeys {
		if item, ok := r.items[id]; ok {
			item.DedupeKey = key
			r.items[id] = item
		}
	}
	return nil
}

func (r *NotificationRepository) persistLocked() error {
	dedupeKeys := make(map[string]string)
	for id, item := range r.items {
		if item.DedupeKey != "" {
			dedupeKeys[id] = item.DedupeKey
		}
	}
	data, err := json.MarshalIndent(notificationFile{Version: 2, Items: r.items, DedupeKeys: dedupeKeys}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode notification store: %w", err)
	}
	tmp := r.filePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write notification store: %w", err)
	}
	if err := replaceFile(tmp, r.filePath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace notification store: %w", err)
	}
	return nil
}

func cloneAnyMap(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
