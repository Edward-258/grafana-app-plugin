package handler

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/grafana/authlib/authz"
	authzcache "github.com/grafana/authlib/cache"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
)

// RBAC action 常量、PluginID 与 roleActions 回退映射全部由 zz_generated.go
// 从 src/plugin.json 生成（scripts/gen-permissions.js），本文件不再手写任何权限字符串。

// Grafana 转发用户身份 id token 的 header（需 externalServiceAccounts / idForwarded）。
const idTokenHeader = "X-Grafana-Id"

// authZClient 返回按官方 RBAC 指南初始化的 authz 客户端；service account token
// 不变时复用同一实例（token 来自 GrafanaConfig，由 iam 配置段下发）。
func (a *App) authZClient(r *http.Request) (*authz.EnforcementClientImpl, error) {
	ctx := r.Context()
	logger := log.DefaultLogger.FromContext(ctx)
	cfg := backend.GrafanaConfigFromContext(ctx)

	saToken, err := cfg.PluginAppClientSecret()
	if err != nil || saToken == "" {
		if err == nil {
			err = errors.New("service account token not found")
		}
		logger.Error("获取插件 service account token 失败", "error", err)
		return nil, err
	}

	a.mx.Lock()
	defer a.mx.Unlock()
	if saToken == a.saToken && a.authzClient != nil {
		return a.authzClient, nil
	}

	grafanaURL, err := cfg.AppURL()
	if err != nil {
		logger.Error("获取 Grafana AppURL 失败", "error", err)
		return nil, err
	}

	client, err := authz.NewEnforcementClient(
		authz.Config{
			APIURL:  grafanaURL,
			Token:   saToken,
			JWKsURL: strings.TrimRight(grafanaURL, "/") + "/api/signing-keys/keys",
		},
		authz.WithSearchByPrefix(PluginID),
		authz.WithCache(authzcache.NewLocalCache(authzcache.Config{
			Expiry:          10 * time.Second,
			CleanupInterval: 5 * time.Second,
		})),
	)
	if err != nil {
		logger.Error("初始化 authz client 失败", "error", err)
		return nil, err
	}

	a.saToken = saToken
	a.authzClient = client
	return client, nil
}

// hasAction 判定当前请求调用者是否持有 action：
// 有 X-Grafana-Id 时走官方 authz 校验；authz 不可用或报错（匿名 token 无法
// 查询、授权服务故障）时降级到 org 角色映射——角色仍由 Grafana 提供，不会
// 放大权限。无用户信息（服务端发起）一律拒绝。
func (a *App) hasAction(r *http.Request, action string) bool {
	logger := log.DefaultLogger.FromContext(r.Context())

	roleFallback := func() bool {
		user := backend.PluginConfigFromContext(r.Context()).User
		if user == nil {
			logger.Debug("无用户信息，拒绝", "action", action)
			return false
		}
		if !roleActions[user.Role][action] {
			logger.Debug("org 角色不含该 action", "role", user.Role, "action", action)
			return false
		}
		return true
	}

	idToken := r.Header.Get(idTokenHeader)
	if idToken == "" {
		return roleFallback()
	}

	client, err := a.authZClient(r)
	if err != nil {
		logger.Warn("authz client 不可用，降级为 org 角色判定", "error", err)
		return roleFallback()
	}
	ok, err := client.HasAccess(r.Context(), idToken, action)
	if err != nil {
		// 匿名会话的 token 无法在授权服务器查询（"can only query server for
		// users and service-accounts"），authz 故障同理——降级而非拒绝。
		logger.Warn("authz 校验出错，降级为 org 角色判定", "action", action, "error", err)
		return roleFallback()
	}
	return ok
}

// requireAction 是资源端点的 action 门槛中间件。
func (a *App) requireAction(action string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.hasAction(r, action) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "权限不足，需要 " + action})
			return
		}
		next(w, r)
	}
}
