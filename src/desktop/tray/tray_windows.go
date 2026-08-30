//go:build windows

package tray

import (
	"context"
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

const (
	wmDestroy      = 0x0002
	wmCommand      = 0x0111
	wmApp          = 0x8000
	wmTrayCallback = wmApp + 1
	wmLButtonUp    = 0x0202
	wmRButtonUp    = 0x0205
	wmClose        = 0x0010
	swHide         = 0
	mfString       = 0x00000000
	mfSeparator    = 0x00000800
	tpmRightButton = 0x0002
	tpmReturnCmd   = 0x0100
	nimAdd         = 0x00000000
	nimDelete      = 0x00000002
	nimSetVersion  = 0x00000004
	nifMessage     = 0x00000001
	nifIcon        = 0x00000002
	nifTip         = 0x00000004
	nifInfo        = 0x00000010
	notifyVersion  = 4
	idiApplication = 32512
	openCommand    = 1001
	quitCommand    = 1002
)

type trayPoint struct{ x, y int32 }

type trayMsg struct {
	hwnd    syscall.Handle
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	point   trayPoint
	private uint32
}

type trayWndClass struct {
	cbSize      uint32
	style       uint32
	windowProc  uintptr
	classExtra  int32
	windowExtra int32
	instance    syscall.Handle
	icon        syscall.Handle
	cursor      syscall.Handle
	background  syscall.Handle
	menuName    *uint16
	className   *uint16
	smallIcon   syscall.Handle
}

type notifyIconData struct {
	cbSize           uint32
	hwnd             syscall.Handle
	uid              uint32
	flags            uint32
	callbackMessage  uint32
	icon             syscall.Handle
	tip              [128]uint16
	state            uint32
	stateMask        uint32
	info             [256]uint16
	timeoutOrVersion uint32
	infoTitle        [64]uint16
	infoFlags        uint32
	guid             [16]byte
	balloonIcon      syscall.Handle
}

type trayController struct {
	hwnd        syscall.Handle
	className   *uint16
	title       string
	url         string
	openOnStart bool
}

var currentController *trayController

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
)

func run(ctx context.Context, options Options) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	controller, err := newTrayController(options)
	if err != nil {
		return err
	}
	currentController = controller
	defer func() { currentController = nil }()
	if controller.openOnStart {
		if err := openURL(controller.url); err != nil {
			return err
		}
	}

	go func(hwnd syscall.Handle) {
		<-ctx.Done()
		postMessage.Call(uintptr(hwnd), wmClose, 0, 0)
	}(controller.hwnd)

	var message trayMsg
	for {
		result, _, callErr := getMessage.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if int32(result) == -1 {
			return fmt.Errorf("read tray message: %w", callErr)
		}
		if result == 0 {
			return nil
		}
		translateMessage.Call(uintptr(unsafe.Pointer(&message)))
		dispatchMessage.Call(uintptr(unsafe.Pointer(&message)))
	}
}

func newTrayController(options Options) (*trayController, error) {
	className, err := syscall.UTF16PtrFromString("NRInternTrayWindow")
	if err != nil {
		return nil, err
	}
	controller := &trayController{className: className, title: options.Title, url: options.URL, openOnStart: options.OpenOnStart}
	if controller.title == "" {
		controller.title = "NR-Intern"
	}
	instance, _, _ := getModuleHandle.Call(0)
	icon, _, _ := loadIcon.Call(0, idiApplication)
	if icon == 0 {
		return nil, fmt.Errorf("load system tray icon")
	}
	windowClass := trayWndClass{
		cbSize:     uint32(unsafe.Sizeof(trayWndClass{})),
		style:      0,
		windowProc: syscall.NewCallback(trayWindowProc),
		instance:   syscall.Handle(instance),
		icon:       syscall.Handle(icon),
		cursor:     0,
		background: 0,
		className:  className,
		smallIcon:  syscall.Handle(icon),
	}
	if atom, _, callErr := registerClassEx.Call(uintptr(unsafe.Pointer(&windowClass))); atom == 0 && callErr != syscall.Errno(1410) {
		return nil, fmt.Errorf("register tray window: %w", callErr)
	}
	hwnd, _, callErr := createWindowEx.Call(
		0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(className)), 0,
		0, 0, 0, 0, 0, 0, instance, 0,
	)
	if hwnd == 0 {
		return nil, fmt.Errorf("create tray window: %w", callErr)
	}
	controller.hwnd = syscall.Handle(hwnd)
	showWindow.Call(hwnd, swHide)
	data := notifyIconData{
		cbSize:          uint32(unsafe.Sizeof(notifyIconData{})),
		hwnd:            controller.hwnd,
		uid:             1,
		flags:           nifMessage | nifIcon | nifTip,
		callbackMessage: wmTrayCallback,
		icon:            syscall.Handle(icon),
	}
	copy(data.tip[:], syscall.StringToUTF16(controller.title))
	if result, _, callErr := shellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&data))); result == 0 {
		destroyWindow.Call(hwnd)
		if callErr != nil {
			return nil, fmt.Errorf("add system tray icon: %w", callErr)
		}
		return nil, fmt.Errorf("add system tray icon")
	}
	data.flags = nifMessage | nifIcon | nifTip
	data.timeoutOrVersion = notifyVersion
	_, _, _ = shellNotifyIcon.Call(nimSetVersion, uintptr(unsafe.Pointer(&data)))
	return controller, nil
}

func trayWindowProc(hwnd syscall.Handle, message uint32, wParam, lParam uintptr) uintptr {
	switch message {
	case wmTrayCallback:
		switch uint32(lParam) {
		case wmLButtonUp:
			if currentController != nil {
				_ = openURL(currentController.url)
			}
		case wmRButtonUp:
			showTrayMenu(hwnd)
		}
	case wmCommand:
		switch uint32(wParam & 0xffff) {
		case openCommand:
			if currentController != nil {
				_ = openURL(currentController.url)
			}
		case quitCommand:
			postMessage.Call(uintptr(hwnd), wmClose, 0, 0)
		}
	case wmClose:
		removeTrayIcon(hwnd)
		destroyWindow.Call(uintptr(hwnd))
	case wmDestroy:
		postQuitMessage.Call(0)
	}
	result, _, _ := defWindowProc.Call(uintptr(hwnd), uintptr(message), wParam, lParam)
	return result
}

func showTrayMenu(hwnd syscall.Handle) {
	menu, _, _ := createPopupMenu.Call()
	if menu == 0 {
		return
	}
	openTitle, _ := syscall.UTF16PtrFromString("開啟 NR-Intern")
	quitTitle, _ := syscall.UTF16PtrFromString("結束 NR-Intern")
	appendMenu.Call(menu, mfString, openCommand, uintptr(unsafe.Pointer(openTitle)))
	appendMenu.Call(menu, mfSeparator, 0, 0)
	appendMenu.Call(menu, mfString, quitCommand, uintptr(unsafe.Pointer(quitTitle)))
	var point trayPoint
	getCursorPos.Call(uintptr(unsafe.Pointer(&point)))
	setForegroundWindow.Call(uintptr(hwnd))
	command, _, _ := trackPopupMenu.Call(menu, tpmRightButton|tpmReturnCmd, uintptr(point.x), uintptr(point.y), 0, uintptr(hwnd), 0)
	destroyMenu.Call(menu)
	if command != 0 {
		postMessage.Call(uintptr(hwnd), wmCommand, command, 0)
	}
}

func removeTrayIcon(hwnd syscall.Handle) {
	data := notifyIconData{cbSize: uint32(unsafe.Sizeof(notifyIconData{})), hwnd: hwnd, uid: 1}
	data.flags = nifMessage
	_, _, _ = shellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&data)))
}

func openURL(value string) error {
	if value == "" {
		return nil
	}
	verb, _ := syscall.UTF16PtrFromString("open")
	target, err := syscall.UTF16PtrFromString(value)
	if err != nil {
		return err
	}
	result, _, callErr := shellExecute.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(target)), 0, 0, 1)
	if result <= 32 {
		if callErr == syscall.Errno(0) {
			return fmt.Errorf("ShellExecuteW failed with code %d", result)
		}
		return callErr
	}
	return nil
}

var (
	registerClassEx     = user32.NewProc("RegisterClassExW")
	createWindowEx      = user32.NewProc("CreateWindowExW")
	loadIcon            = user32.NewProc("LoadIconW")
	showWindow          = user32.NewProc("ShowWindow")
	getMessage          = user32.NewProc("GetMessageW")
	translateMessage    = user32.NewProc("TranslateMessage")
	dispatchMessage     = user32.NewProc("DispatchMessageW")
	defWindowProc       = user32.NewProc("DefWindowProcW")
	destroyWindow       = user32.NewProc("DestroyWindow")
	postQuitMessage     = user32.NewProc("PostQuitMessage")
	postMessage         = user32.NewProc("PostMessageW")
	createPopupMenu     = user32.NewProc("CreatePopupMenu")
	appendMenu          = user32.NewProc("AppendMenuW")
	getCursorPos        = user32.NewProc("GetCursorPos")
	setForegroundWindow = user32.NewProc("SetForegroundWindow")
	trackPopupMenu      = user32.NewProc("TrackPopupMenu")
	destroyMenu         = user32.NewProc("DestroyMenu")
	getModuleHandle     = kernel32.NewProc("GetModuleHandleW")
	shellNotifyIcon     = shell32.NewProc("Shell_NotifyIconW")
	shellExecute        = shell32.NewProc("ShellExecuteW")
)
