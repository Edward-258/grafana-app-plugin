// 由 scripts/gen-permissions.js 从 src/plugin.json 生成，请勿手改。
// 改 roles[]/grants 后运行 npm run build 重新生成；
// zz_generated_test.go 会独立重推导并守护一致性。

package handler

const (
	PluginID = "local-ecs-app"

	actionRead   = "local-ecs-app.ecs:read"
	actionReveal = "local-ecs-app.ecs:reveal"
	actionWrite  = "local-ecs-app.ecs:write"
)

// roleActions 是无 id token 时的回退映射，与 plugin.json 各角色的 grants 等价（生成）。
var roleActions = map[string]map[string]bool{
	"Admin":  {actionRead: true, actionReveal: true, actionWrite: true},
	"Editor": {actionRead: true, actionReveal: true},
	"Viewer": {actionRead: true},
}
