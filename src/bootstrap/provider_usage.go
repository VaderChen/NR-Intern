package bootstrap

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

const (
	providerUsageRefreshInterval    = 3 * time.Minute
	providerUsageRefreshTimeout     = 30 * time.Second
	providerUsageRefreshConcurrency = 4
)

// startModelLimitDiscovery 在啟動後向各 Provider 問一次模型限制。
//
// context window 是 Harness 決定何時壓縮的依據，未宣告時會退回 256K 的後備預算，
// 對本機模型等於永遠不壓縮。多數相容服務會在模型目錄回報這個值，開機問一次就好，
// 不必要求使用者自己去進階設定裡填。問不到只記一行 WARN，不影響啟動。
func (r *Runtime) startModelLimitDiscovery() {
	if r == nil || r.Model == nil {
		return
	}
	logger := slog.Default().With("service", r.Config.ServiceName)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		r.Model.DiscoverModelLimits(ctx, logger)
	}()
}

// startProviderUsageRefresher 在後端啟動後立即查詢一次，之後固定在背景更新。
func (r *Runtime) startProviderUsageRefresher() {
	if r == nil || r.Model == nil || r.providerUsageCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.providerUsageContext = ctx
	r.providerUsageCancel = cancel
	go func() {
		r.refreshProviderUsage(ctx)
		ticker := time.NewTicker(providerUsageRefreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.refreshProviderUsage(ctx)
			}
		}
	}()
}

// requestProviderUsageRefresh 供 Provider 設定異動後立即要求一次背景更新。
func (r *Runtime) requestProviderUsageRefresh() {
	if r == nil || r.providerUsageContext == nil {
		return
	}
	go r.refreshProviderUsage(r.providerUsageContext)
}

func (r *Runtime) refreshProviderUsage(ctx context.Context) {
	if r == nil || r.Model == nil || ctx == nil || ctx.Err() != nil || !r.providerUsageRefreshMu.TryLock() {
		return
	}
	defer r.providerUsageRefreshMu.Unlock()

	semaphore := make(chan struct{}, providerUsageRefreshConcurrency)
	var waitGroup sync.WaitGroup
	for _, provider := range r.Model.ListProviders() {
		providerID := provider.ID
		select {
		case <-ctx.Done():
			waitGroup.Wait()
			return
		case semaphore <- struct{}{}:
		}
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			defer func() { <-semaphore }()
			requestContext, cancel := context.WithTimeout(ctx, providerUsageRefreshTimeout)
			defer cancel()
			if err := r.Model.RefreshProviderUsage(requestContext, providerID); err != nil && requestContext.Err() == nil {
				r.logger.Debug("provider usage refresh skipped", "provider_id", providerID, "error", err)
			}
		}()
	}
	waitGroup.Wait()
}
