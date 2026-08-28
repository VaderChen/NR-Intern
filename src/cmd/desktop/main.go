package main

import (
	"AgenticService/src/bootstrap"
	"AgenticService/src/desktop/httpui"
	"AgenticService/src/desktop/launcher"
	"AgenticService/src/desktop/supervisor"
	"AgenticService/src/desktop/window"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type options struct {
	configPath    string
	uiListen      string
	backendURL    string
	backendBinary string
	backendToken  string
	autoStart     bool
	nativeWindow  bool
	openBrowser   bool
	backendChild  bool
	backendListen string
}

func main() {
	var value options
	flag.StringVar(&value.configPath, "config", "", "AI Agent JSON 設定檔")
	flag.StringVar(&value.uiListen, "ui-listen", "127.0.0.1:8790", "桌面 HTML UI 監聽位址")
	flag.StringVar(&value.backendURL, "backend-url", "http://127.0.0.1:8787", "後端 HTTP URL")
	flag.StringVar(&value.backendBinary, "backend-bin", "", "可選的獨立後端執行檔；省略時由桌面程式啟動子模式")
	flag.StringVar(&value.backendToken, "backend-token", "", "連接外部後端時使用的 Bearer token")
	flag.BoolVar(&value.autoStart, "auto-start", true, "後端不存在時自動啟動")
	flag.BoolVar(&value.nativeWindow, "window", true, "使用原生桌面視窗載入 HTML UI")
	flag.BoolVar(&value.openBrowser, "open", true, "原生視窗不可用時開啟預設瀏覽器")
	flag.BoolVar(&value.backendChild, "backend-child", false, "內部後端子程序模式")
	flag.StringVar(&value.backendListen, "backend-listen", "", "內部後端子程序監聽位址")
	flag.Parse()

	if value.backendChild {
		runBackendChild(value)
		return
	}
	if err := runDesktop(value); err != nil {
		fatal(err)
	}
}

func runDesktop(value options) error {
	if !loopbackAddress(value.uiListen) {
		return fmt.Errorf("桌面 UI 僅允許監聽 loopback 位址")
	}
	config, err := bootstrap.LoadConfig(value.configPath)
	if err != nil {
		return err
	}
	if value.backendToken == "" {
		value.backendToken = config.APIToken
	}
	backendAddress, err := backendHost(value.backendURL)
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	arguments := []string{}
	backendExecutable := value.backendBinary
	if backendExecutable == "" {
		backendExecutable = executable
		arguments = append(arguments, "-backend-child=true", "-backend-listen", backendAddress)
	} else {
		arguments = append(arguments, "-listen", backendAddress)
	}
	if value.configPath != "" {
		arguments = append(arguments, "-config", value.configPath)
	}
	workingDirectory, _ := os.Getwd()
	manager, err := supervisor.New(supervisor.Config{
		BackendURL:     value.backendURL,
		Executable:     backendExecutable,
		Arguments:      arguments,
		WorkingDir:     workingDirectory,
		StartupTimeout: 25 * time.Second,
		StopTimeout:    10 * time.Second,
		LogMaxBytes:    1024 * 1024,
	})
	if err != nil {
		return err
	}
	if value.autoStart {
		startupContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := manager.Start(startupContext); err != nil {
			slog.Warn("backend auto-start failed; console will remain available", "error", err)
		}
		cancel()
	}
	ui, err := httpui.New(httpui.Config{Supervisor: manager, BackendToken: value.backendToken})
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", value.uiListen)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: ui, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 1 << 20}
	uiURL := "http://" + listener.Addr().String()
	go func() {
		slog.Info("desktop console started", "url", uiURL, "backend", value.backendURL)
		if serveErr := server.Serve(listener); serveErr != nil && serveErr != http.ErrServerClosed {
			slog.Error("desktop console failed", "error", serveErr)
		}
	}()
	stopContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	visibleUI := false
	if value.nativeWindow {
		if err := window.Run(stopContext, window.Options{
			Title:  "NR-Intern Agent Console",
			URL:    uiURL,
			Width:  1280,
			Height: 820,
		}); err != nil {
			if !errors.Is(err, window.ErrUnavailable) {
				slog.Warn("native desktop window failed", "error", err)
			} else {
				slog.Info("native desktop window unavailable", "error", err)
			}
		} else {
			visibleUI = true
			stop()
		}
	}
	if !visibleUI && stopContext.Err() == nil {
		if value.openBrowser {
			if err := launcher.OpenURL(uiURL); err != nil {
				slog.Warn("unable to open browser", "error", err)
			}
		} else {
			slog.Info("desktop console is running without a visible UI", "url", uiURL)
		}
		<-stopContext.Done()
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownContext)
	if err := manager.Stop(shutdownContext); err != nil && !errors.Is(err, supervisor.ErrExternalBackend) {
		slog.Warn("owned backend stop failed", "error", err)
	}
	return nil
}

func runBackendChild(value options) {
	config, err := bootstrap.LoadConfig(value.configPath)
	if err != nil {
		fatal(err)
	}
	if value.backendListen != "" {
		config.ListenAddress = value.backendListen
	}
	runtime, err := bootstrap.Build(config)
	if err != nil {
		fatal(err)
	}
	config = runtime.Config
	server := &http.Server{Addr: config.ListenAddress, Handler: runtime.HTTPHandler, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 1 << 20}
	go func() {
		if serveErr := server.ListenAndServe(); serveErr != nil && serveErr != http.ErrServerClosed {
			fatal(serveErr)
		}
	}()
	stopContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-stopContext.Done()
	shutdownContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownContext)
}

func backendHost(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid backend URL %q", rawURL)
	}
	return parsed.Host, nil
}

func loopbackAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	host = strings.Trim(host, "[]")
	return strings.EqualFold(host, "localhost") || net.ParseIP(host).IsLoopback()
}

func fatal(err error) {
	name := filepath.Base(os.Args[0])
	_, _ = fmt.Fprintf(os.Stderr, "%s: %v\n", name, err)
	os.Exit(1)
}
