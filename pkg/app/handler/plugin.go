package handler

import (
	"context"
	"net/http"
	"sync"

	"github.com/grafana/authlib/authz"
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

	mx          sync.Mutex
	saToken     string
	authzClient *authz.EnforcementClientImpl
}

func NewApp(_ context.Context, _ backend.AppInstanceSettings) (instancemgmt.Instance, error) {
	a := &App{resolver: service.NewResolver()}
	mux := http.NewServeMux()
	mux.HandleFunc("/ecs/enrich", a.requireAction(actionRead, a.handleEnrich))
	mux.HandleFunc("/ecs/resolve", a.requireAction(actionRead, a.handleResolve))
	mux.HandleFunc("/ecs/ak", a.requireAction(actionRead, a.handleAK))
	mux.HandleFunc("/ecs/test", a.requireAction(actionReveal, a.handleTest))
	a.CallResourceHandler = httpadapter.New(mux)
	return a, nil
}

func (a *App) Dispose() {}

func (a *App) CheckHealth(ctx context.Context, req *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
	cfg, err := configFrom(req.PluginContext)
	if err != nil { // AK 未配置时 configFrom 已返回 ErrNoSettings
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
