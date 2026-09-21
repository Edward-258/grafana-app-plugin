# Changelog

## 1.0.0 (Unreleased)

Initial release.

### Features

- ECS 资产对齐：从 Prometheus 采集监控标识，后端用阿里云 AK 全地域枚举并唯一命中，前端展示 ECS ID/规格/地域/租期（公网/内网 IP 永不出后端）。
- 租期信息：展示 CreationTime / ExpiredTime / 计费方式，按量付费实例屏蔽 2099 哨兵到期值。
- RBAC：`ecs:read` / `ecs:reveal` / `ecs:write` 三级 action，plugin.json 单源生成前后端常量；凭证仅存 `secureJsonData`。
- 稳定性：请求守卫三道闸（body 1MB、identities 2000 条、阿里云响应 4MB）、资产缓存 stale-while-revalidate（30 分钟硬上限）。
- 工程化：ESLint + Prettier（`@grafana/eslint-config`）、Playwright e2e（面板菜单扩展红线回归 + RBAC 拦截回归）、GitHub Actions CI（build 双关卡 + e2e 双实例）、is-compatible 前端 API 兼容检查、dependabot、golangci-lint。
