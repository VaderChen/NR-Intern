package bootstrap

import (
	"AgenticService/src/domain"
	"AgenticService/src/netpass"
	"context"
	"fmt"
	"time"
)

func (r *Runtime) ReverseProxyStatus(ctx context.Context) (domain.ReverseProxyStatus, error) {
	if err := ctx.Err(); err != nil {
		return domain.ReverseProxyStatus{}, err
	}
	if r == nil || r.ReverseProxy == nil {
		return domain.ReverseProxyStatus{}, fmt.Errorf("%w: reverse proxy manager is unavailable", domain.ErrNotFound)
	}
	status := r.ReverseProxy.Status()
	return r.reverseProxyStatusView(status), nil
}

func (r *Runtime) UpdateReverseProxy(ctx context.Context, input domain.UpdateReverseProxyInput) (domain.ReverseProxyStatus, error) {
	if err := ctx.Err(); err != nil {
		return domain.ReverseProxyStatus{}, err
	}
	if r == nil || r.ReverseProxy == nil {
		return domain.ReverseProxyStatus{}, fmt.Errorf("%w: reverse proxy manager is unavailable", domain.ErrNotFound)
	}
	status, err := r.ReverseProxy.UpdateConfig(netpass.ConfigUpdate{
		Endpoint: input.Endpoint, APIKey: input.APIKey, ClearAPIKey: input.ClearAPIKey, Name: input.Name,
	})
	if err != nil {
		return domain.ReverseProxyStatus{}, fmt.Errorf("%w: %v", domain.ErrInvalidInput, err)
	}
	return r.reverseProxyStatusView(status), nil
}

func (r *Runtime) StartReverseProxy(ctx context.Context, input domain.StartReverseProxyInput) (domain.ReverseProxyStatus, error) {
	if err := ctx.Err(); err != nil {
		return domain.ReverseProxyStatus{}, err
	}
	if r == nil || r.ReverseProxy == nil {
		return domain.ReverseProxyStatus{}, fmt.Errorf("%w: reverse proxy manager is unavailable", domain.ErrNotFound)
	}
	if !input.AcceptUsagePolicy {
		return domain.ReverseProxyStatus{}, fmt.Errorf("%w: 請先閱讀並同意 NetPass 使用政策與責任說明", domain.ErrInvalidInput)
	}
	status, err := r.ReverseProxy.Start()
	if err != nil {
		return domain.ReverseProxyStatus{}, fmt.Errorf("%w: %v", domain.ErrInvalidInput, err)
	}
	return r.reverseProxyStatusView(status), nil
}

func (r *Runtime) StopReverseProxy(ctx context.Context) (domain.ReverseProxyStatus, error) {
	if err := ctx.Err(); err != nil {
		return domain.ReverseProxyStatus{}, err
	}
	if r == nil || r.ReverseProxy == nil {
		return domain.ReverseProxyStatus{}, fmt.Errorf("%w: reverse proxy manager is unavailable", domain.ErrNotFound)
	}
	if err := r.ReverseProxy.Stop(); err != nil {
		return domain.ReverseProxyStatus{}, fmt.Errorf("%w: %v", domain.ErrInvalidInput, err)
	}
	return r.reverseProxyStatusView(r.ReverseProxy.Status()), nil
}

func (r *Runtime) reverseProxyStatusView(status netpass.Status) domain.ReverseProxyStatus {
	value := domain.ReverseProxyStatus{
		RuntimeChecked: status.RuntimeChecked, Available: status.Available, Running: status.Running,
		Connected: status.Connected, Endpoint: status.Endpoint, APIKeySet: status.APIKeySet, Name: status.Name,
		TargetPort: status.TargetPort, PID: status.PID, ClientID: status.ClientID, PublicURL: status.PublicURL,
		LastError: status.LastError,
	}
	if !status.StartedAt.IsZero() {
		value.StartedAt = status.StartedAt.Format(time.RFC3339)
	}
	return value
}
