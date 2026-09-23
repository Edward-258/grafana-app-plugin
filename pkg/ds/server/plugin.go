package server

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/instancemgmt"
	"github.com/grafana/grafana-plugin-sdk-go/data"

	"local-ecs-app/pkg/app/handler"
	"local-ecs-app/pkg/app/service"
)

var (
	_ backend.QueryDataHandler      = (*Datasource)(nil)
	_ backend.CheckHealthHandler    = (*Datasource)(nil)
	_ instancemgmt.InstanceDisposer = (*Datasource)(nil)
)

// PluginID 复用 app 侧单源常量（package.json name 生成 + "-ds" 后缀），
// 与 src/datasource/plugin.json 的 %PLUGIN_ID_DS% 同源。
const PluginID = handler.AlertingDatasourceType

type Datasource struct {
	resolver *service.Resolver
}

func New(_ context.Context, _ backend.DataSourceInstanceSettings) (instancemgmt.Instance, error) {
	return &Datasource{resolver: service.NewResolver()}, nil
}

func (d *Datasource) Dispose() {}

// queryModel 是前端 QueryEditor / 告警规则的下发查询体。
type queryModel struct {
	Frame string `json:"frame"`
}

// frameKind 约束帧类型；未知值回落 assets（fail-open 到非财务帧，且 assets
// 自身仍有 ecs:read 门禁）。
type frameKind string

const (
	frameAssets  frameKind = "assets"
	frameAccount frameKind = "account"
)

func parseFrame(raw string) frameKind {
	if frameKind(raw) == frameAccount {
		return frameAccount
	}
	return frameAssets
}

// authorize 帧级角色门禁：
//   - 无用户上下文 = 告警引擎评估等服务端调用，放行（评估身份不具 org 角色，
//     靠这一条让规则能在服务端取数；交互面永远带用户，不会命中此分支）；
//   - assets 帧：ecs:read（Viewer/Editor/Admin）；
//   - account 帧：ecs:reveal（Editor/Admin）——与 app 端 attachBilling 同档，
//     财务数据不对 Viewer 开放。
//
// 判定复用 handler.RoleHas（生成的 org 角色回退映射）；QueryData 走 gRPC，
// SDK 的 User 没有 id token，不存在 authz 路径，与 auth.go 匿名降级同语义。
func authorize(kind frameKind, user *backend.User) error {
	if user == nil {
		return nil
	}
	switch kind {
	case frameAccount:
		if !handler.RoleHas(user.Role, handler.ActionReveal) {
			return fmt.Errorf("账户概览（余额/账单）需要 Editor 及以上角色，当前角色 %q 无权查看", user.Role)
		}
	default:
		if !handler.RoleHas(user.Role, handler.ActionRead) {
			return fmt.Errorf("资产数据需要 ecs:read 权限，当前角色 %q 无权访问", user.Role)
		}
	}
	return nil
}

func (d *Datasource) QueryData(ctx context.Context, req *backend.QueryDataRequest) (*backend.QueryDataResponse, error) {
	// 先鉴权后读设置：未授权调用者不应借错误文案探测凭证配置状态。
	resp := backend.NewQueryDataResponse()
	for _, q := range req.Queries {
		var m queryModel
		if len(q.JSON) > 0 {
			if err := json.Unmarshal(q.JSON, &m); err != nil {
				resp.Responses[q.RefID] = backend.DataResponse{Error: fmt.Errorf("解析查询体失败: %w", err)}
				continue
			}
		}
		kind := parseFrame(m.Frame)
		if err := authorize(kind, req.PluginContext.User); err != nil {
			resp.Responses[q.RefID] = backend.DataResponse{Error: err}
			continue
		}
		cfg, err := settings(req.PluginContext)
		if err != nil {
			resp.Responses[q.RefID] = backend.DataResponse{Error: err}
			continue
		}
		instances, billing, err := d.resolver.Snapshot(ctx, cfg, false)
		if err != nil {
			resp.Responses[q.RefID] = backend.DataResponse{Error: err}
			continue
		}
		if kind == frameAccount {
			resp.Responses[q.RefID] = backend.DataResponse{Frames: data.Frames{accountFrame(billing, timeNow())}}
		} else {
			resp.Responses[q.RefID] = backend.DataResponse{Frames: data.Frames{assetsFrame(instances, timeNow())}}
		}
	}
	return resp, nil
}

func (d *Datasource) CheckHealth(ctx context.Context, req *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
	cfg, err := settings(req.PluginContext)
	if err != nil {
		return &backend.CheckHealthResult{
			Status:  backend.HealthStatusUnknown,
			Message: "未配置 AccessKey：请在「ECS 资产」插件配置页保存凭证（会自动同步到本数据源）",
		}, nil
	}
	if err := d.resolver.Ensure(ctx, cfg); err != nil {
		return &backend.CheckHealthResult{Status: backend.HealthStatusError, Message: err.Error()}, nil
	}
	return &backend.CheckHealthResult{Status: backend.HealthStatusOk, Message: "ok"}, nil
}

// settings 从 ds 实例设置取凭证。凭证只由 app 配置页保存时同步写入
// secureJsonData（加密存储），本数据源不提供第二录入口。
func settings(pCtx backend.PluginContext) (service.Config, error) {
	cfg := service.Config{}
	if s := pCtx.DataSourceInstanceSettings; s != nil {
		cfg.AccessKeyID = s.DecryptedSecureJSONData["accessKeyId"]
		cfg.AccessKeySecret = s.DecryptedSecureJSONData["accessKeySecret"]
	}
	if cfg.AccessKeyID == "" || cfg.AccessKeySecret == "" {
		return cfg, service.ErrNoSettings
	}
	return cfg, nil
}

// timeNow 抽出来便于测试注入。
var timeNow = time.Now
