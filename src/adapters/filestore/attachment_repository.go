package filestore

import (
	"AgenticService/src/domain"
	"AgenticService/src/internal/textutil"
	"context"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

const attachmentDirectory = "attachments"

type AttachmentRepository struct {
	sessionsRoot string
}

func NewAttachmentRepository(dataDir string) (*AttachmentRepository, error) {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return nil, fmt.Errorf("%w: data directory is required", domain.ErrInvalidInput)
	}
	root := filepath.Join(dataDir, "sessions")
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("create attachment store: %w", err)
	}
	return &AttachmentRepository{sessionsRoot: root}, nil
}

func (r *AttachmentRepository) Save(ctx context.Context, sessionID, name, mediaType string, source io.Reader, maxBytes int64) (domain.Attachment, error) {
	if err := ctx.Err(); err != nil {
		return domain.Attachment{}, err
	}
	if source == nil {
		return domain.Attachment{}, fmt.Errorf("%w: attachment body is required", domain.ErrInvalidInput)
	}
	if maxBytes <= 0 {
		return domain.Attachment{}, fmt.Errorf("%w: attachment size limit is required", domain.ErrInvalidInput)
	}
	name = safeAttachmentName(name)
	if name == "" {
		return domain.Attachment{}, fmt.Errorf("%w: attachment filename is required", domain.ErrInvalidInput)
	}
	attachmentID := domain.NewID("attachment")
	directory, err := r.attachmentDir(sessionID, attachmentID)
	if err != nil {
		return domain.Attachment{}, err
	}
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return domain.Attachment{}, fmt.Errorf("create attachment directory: %w", err)
	}
	target := filepath.Join(directory, name)
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		_ = os.RemoveAll(directory)
		return domain.Attachment{}, fmt.Errorf("create attachment: %w", err)
	}
	written, copyErr := io.Copy(file, io.LimitReader(source, maxBytes+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || written > maxBytes {
		_ = os.RemoveAll(directory)
		switch {
		case copyErr != nil:
			return domain.Attachment{}, fmt.Errorf("save attachment: %w", copyErr)
		case closeErr != nil:
			return domain.Attachment{}, fmt.Errorf("close attachment: %w", closeErr)
		default:
			return domain.Attachment{}, fmt.Errorf("%w: attachment exceeds %d bytes", domain.ErrInvalidInput, maxBytes)
		}
	}
	absolute, err := filepath.Abs(target)
	if err != nil {
		_ = os.RemoveAll(directory)
		return domain.Attachment{}, fmt.Errorf("resolve attachment path: %w", err)
	}
	mediaType = strings.TrimSpace(mediaType)
	if mediaType == "" {
		mediaType = mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
	}
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	return domain.Attachment{
		ID:        attachmentID,
		SessionID: strings.TrimSpace(sessionID),
		Name:      name,
		MediaType: mediaType,
		Path:      filepath.Clean(absolute),
		Size:      written,
		CreatedAt: time.Now().UTC(),
	}, nil
}

func (r *AttachmentRepository) Get(ctx context.Context, sessionID, attachmentID string) (domain.Attachment, error) {
	if err := ctx.Err(); err != nil {
		return domain.Attachment{}, err
	}
	directory, err := r.attachmentDir(sessionID, attachmentID)
	if err != nil {
		return domain.Attachment{}, err
	}
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return domain.Attachment{}, fmt.Errorf("%w: attachment %q", domain.ErrNotFound, attachmentID)
	}
	if err != nil {
		return domain.Attachment{}, fmt.Errorf("read attachment: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil || !info.Mode().IsRegular() {
			continue
		}
		path, pathErr := filepath.Abs(filepath.Join(directory, entry.Name()))
		if pathErr != nil {
			return domain.Attachment{}, fmt.Errorf("resolve attachment path: %w", pathErr)
		}
		mediaType := mime.TypeByExtension(strings.ToLower(filepath.Ext(entry.Name())))
		if mediaType == "" {
			mediaType = "application/octet-stream"
		}
		return domain.Attachment{
			ID:        strings.TrimSpace(attachmentID),
			SessionID: strings.TrimSpace(sessionID),
			Name:      entry.Name(),
			MediaType: mediaType,
			Path:      filepath.Clean(path),
			Size:      info.Size(),
			CreatedAt: info.ModTime().UTC(),
		}, nil
	}
	return domain.Attachment{}, fmt.Errorf("%w: attachment %q has no file", domain.ErrNotFound, attachmentID)
}

func (r *AttachmentRepository) Delete(ctx context.Context, sessionID, attachmentID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	directory, err := r.attachmentDir(sessionID, attachmentID)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(directory); err != nil {
		return fmt.Errorf("delete attachment: %w", err)
	}
	return nil
}

func (r *AttachmentRepository) attachmentDir(sessionID, attachmentID string) (string, error) {
	sessionID = strings.TrimSpace(sessionID)
	attachmentID = strings.TrimSpace(attachmentID)
	if !safeStoreID(sessionID, "session_") || !safeStoreID(attachmentID, "attachment_") {
		return "", fmt.Errorf("%w: invalid session or attachment id", domain.ErrInvalidInput)
	}
	return filepath.Join(r.sessionsRoot, sessionID, "workspace", attachmentDirectory, attachmentID), nil
}

func safeStoreID(value, prefix string) bool {
	if !strings.HasPrefix(value, prefix) || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func safeAttachmentName(value string) string {
	value = strings.TrimSpace(textutil.NormalizeFullwidthASCII(value))
	value = filepath.Base(strings.ReplaceAll(value, "\\", "/"))
	value = strings.Map(func(character rune) rune {
		if character == 0 || character < 0x20 || character == 0x7f {
			return -1
		}
		return character
	}, value)
	if value == "" || value == "." || value == ".." {
		return ""
	}
	for utf8.RuneCountInString(value) > 180 {
		_, size := utf8.DecodeLastRuneInString(value)
		value = value[:len(value)-size]
	}
	return strings.TrimSpace(value)
}
