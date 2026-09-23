package handler

// 供捆绑数据源（pkg/ds）复用 RBAC 判定。ds 的 QueryData 走 gRPC，SDK 的
// backend.User 只有 Role（没有 id token），因此与 auth.go 的匿名降级路径同
// 语义：按生成的 roleActions（org 角色 → grants 映射）判定，不放大权限。
// action 常量本体由 zz_generated.go 单源生成，这里只做导出别名。

const (
	ActionRead   = actionRead
	ActionReveal = actionReveal
)

func RoleHas(role, action string) bool { return roleActions[role][action] }
