package handler

import (
	"context"
	"net/http"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/instancemgmt"
	"github.com/grafana/grafana-plugin-sdk-go/backend/resource/httpadapter"

	"local-ecs-app/pkg/app/service"
)

var (
	_ backend.CallResourceHandler   = (*App)(nil)
	_ instancemgmt.InstanceDisposer = (*App)(nil)
	_ backend.CheckHealthHandler    = (*App)(nil)
)

type App struct {
	backend.CallResourceHandler
	resolver *service.Resolver
}

func NewApp(_ context.Context, _ backend.AppInstanceSettings) (instancemgmt.Instance, error) {
	a := &App{resolver: service.NewResolver()}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", a.handleHealth)
	mux.HandleFunc("/ecs/enrich", a.handleEnrich)
	mux.HandleFunc("/ecs/resolve", a.handleResolve)
	mux.HandleFunc("/ecs/test", a.handleTest)
	a.CallResourceHandler = httpadapter.New(mux)
	return a, nil
}

func (a *App) Dispose() {}

func (a *App) CheckHealth(ctx context.Context, req *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
	cfg, err := configFrom(req.PluginContext)
	if err != nil || cfg.AccessKeyID == "" || cfg.AccessKeySecret == "" {
		return &backend.CheckHealthResult{
			Status:  backend.HealthStatusUnknown,
			Message: "未配置 AccessKey",
		}, nil
	}
	if err := a.resolver.Ensure(ctx, cfg); err != nil {
		return &backend.CheckHealthResult{Status: backend.HealthStatusError, Message: err.Error()}, nil
	}
	return &backend.CheckHealthResult{Status: backend.HealthStatusOk, Message: "ok"}, nil
}
