package main

import (
	"AgenticService/src/bootstrap"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	configPath := flag.String("config", "", "AI Agent JSON 設定檔")
	listenOverride := flag.String("listen", "", "覆寫 HTTP 監聽位址")
	dataOverride := flag.String("data-dir", "", "覆寫資料目錄")
	flag.Parse()

	config, err := bootstrap.LoadConfig(*configPath)
	if err != nil {
		fatal(err)
	}
	if *listenOverride != "" {
		config.ListenAddress = *listenOverride
	}
	if *dataOverride != "" {
		config.DataDir = *dataOverride
	}
	runtime, err := bootstrap.Build(config)
	if err != nil {
		fatal(err)
	}
	config = runtime.Config
	server := &http.Server{
		Addr:              config.ListenAddress,
		Handler:           runtime.HTTPHandler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}
	go func() {
		slog.Info("AI Agent HTTP backend started", "address", config.ListenAddress, "data_dir", config.DataDir, "version", bootstrap.Version)
		if serveErr := server.ListenAndServe(); serveErr != nil && serveErr != http.ErrServerClosed {
			fatal(serveErr)
		}
	}()

	stopContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-stopContext.Done()
	shutdownContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := runtime.Close(shutdownContext); err != nil {
		slog.Error("AI Agent run shutdown failed", "error", err)
	}
	if err := server.Shutdown(shutdownContext); err != nil {
		slog.Error("HTTP backend shutdown failed", "error", err)
	}
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
