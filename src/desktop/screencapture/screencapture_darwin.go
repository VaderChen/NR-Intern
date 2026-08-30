//go:build darwin

package screencapture

/*
#cgo LDFLAGS: -framework AppKit
#include <stddef.h>

int nrInternCopyPNGToClipboard(const void *bytes, size_t length);
long long nrInternClipboardChangeCount(void);
int nrInternReadPNGFromClipboard(void **bytes, size_t *length);
int nrInternScreenCaptureUIRunning(void);
void nrInternFreeCaptureMemory(void *value);
*/
import "C"

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unsafe"
)

func capture(ctx context.Context) (Result, error) {
	if C.nrInternScreenCaptureUIRunning() != 0 {
		return Result{}, fmt.Errorf("macOS 擷取畫面已開啟，請先完成或取消目前的擷取")
	}

	preferences := readMacScreenshotPreferences()
	defer restoreMacScreenshotPreferences(preferences)
	if err := configureMacScreenshot(); err != nil {
		return Result{}, err
	}

	initialChangeCount := int64(C.nrInternClipboardChangeCount())
	const screenshotApp = "/System/Applications/Utilities/Screenshot.app"
	if output, err := exec.CommandContext(ctx, "/usr/bin/open", screenshotApp).CombinedOutput(); err != nil {
		return Result{}, fmt.Errorf("開啟 macOS Screenshot: %w: %s", err, strings.TrimSpace(string(output)))
	}

	const launchTimeout = 5 * time.Second
	const clipboardGracePeriod = 750 * time.Millisecond
	launchDeadline := time.Now().Add(launchTimeout)
	seenCaptureUI := false
	var exitedAt time.Time
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		currentChangeCount := int64(C.nrInternClipboardChangeCount())
		if currentChangeCount != initialChangeCount {
			if value := readMacClipboardPNG(); len(value) > 0 {
				return Result{Status: StatusCopied, PNG: value}, nil
			}
		}

		running := C.nrInternScreenCaptureUIRunning() != 0
		if running {
			seenCaptureUI = true
			exitedAt = time.Time{}
		} else if seenCaptureUI && exitedAt.IsZero() {
			exitedAt = time.Now()
		}
		if !exitedAt.IsZero() && time.Since(exitedAt) >= clipboardGracePeriod {
			return Result{}, ErrCanceled
		}
		if !seenCaptureUI && time.Now().After(launchDeadline) {
			return Result{}, fmt.Errorf("macOS Screenshot 未能啟動")
		}

		select {
		case <-ctx.Done():
			return Result{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

type macScreenshotPreferences struct {
	target       string
	targetExists bool
	style        string
	styleExists  bool
	video        bool
	videoExists  bool
}

func readMacScreenshotPreferences() macScreenshotPreferences {
	target, targetExists := readMacScreenshotPreference("target")
	style, styleExists := readMacScreenshotPreference("style")
	videoValue, videoExists := readMacScreenshotPreference("video")
	video, _ := strconv.ParseBool(videoValue)
	if videoValue == "1" {
		video = true
	}
	return macScreenshotPreferences{
		target: target, targetExists: targetExists,
		style: style, styleExists: styleExists,
		video: video, videoExists: videoExists,
	}
}

func readMacScreenshotPreference(key string) (string, bool) {
	output, err := exec.Command("/usr/bin/defaults", "read", "com.apple.screencapture", key).Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(output)), true
}

func configureMacScreenshot() error {
	settings := [][]string{
		{"write", "com.apple.screencapture", "target", "-string", "clipboard"},
		{"write", "com.apple.screencapture", "style", "-string", "selection"},
		{"write", "com.apple.screencapture", "video", "-bool", "false"},
	}
	for _, arguments := range settings {
		if output, err := exec.Command("/usr/bin/defaults", arguments...).CombinedOutput(); err != nil {
			return fmt.Errorf("設定 macOS Screenshot 模式: %w: %s", err, strings.TrimSpace(string(output)))
		}
	}
	return nil
}

func restoreMacScreenshotPreferences(preferences macScreenshotPreferences) {
	restoreMacScreenshotStringPreference("target", preferences.target, preferences.targetExists)
	restoreMacScreenshotStringPreference("style", preferences.style, preferences.styleExists)
	if preferences.videoExists {
		_ = exec.Command("/usr/bin/defaults", "write", "com.apple.screencapture", "video", "-bool", strconv.FormatBool(preferences.video)).Run()
	} else {
		_ = exec.Command("/usr/bin/defaults", "delete", "com.apple.screencapture", "video").Run()
	}
}

func restoreMacScreenshotStringPreference(key, value string, existed bool) {
	if existed {
		_ = exec.Command("/usr/bin/defaults", "write", "com.apple.screencapture", key, "-string", value).Run()
		return
	}
	_ = exec.Command("/usr/bin/defaults", "delete", "com.apple.screencapture", key).Run()
}

func readMacClipboardPNG() []byte {
	var output unsafe.Pointer
	var length C.size_t
	if C.nrInternReadPNGFromClipboard(&output, &length) == 0 || output == nil || length == 0 {
		return nil
	}
	defer C.nrInternFreeCaptureMemory(output)
	return C.GoBytes(output, C.int(length))
}

func copyPNGToClipboard(_ context.Context, value []byte) error {
	if len(value) == 0 {
		return fmt.Errorf("PNG 圖像不可為空")
	}
	if C.nrInternCopyPNGToClipboard(unsafe.Pointer(&value[0]), C.size_t(len(value))) == 0 {
		return fmt.Errorf("更新 macOS 系統剪貼簿失敗")
	}
	return nil
}
