# Changelog

## 1.0.0 (Unreleased)

Initial release.

### Features

- ECS 资产对齐：从 Prometheus 采集监控标识，后端用阿里云 AK 全地域枚举并唯一命中，前端展示 ECS ID/规格/地域/租期（公网/内网 IP 永不出后端）。
- 租期信息：展示 CreationTime / ExpiredTime / 计费方式，按量付费实例屏蔽 2099 哨兵到期值。
- 精确创建时间（BSS 补充链路）：`QueryAvailableInstances` 订单口径时间以独立字段 `leaseStart` 展示（列表"租赁开始"列 + 弹窗行，秒级精度）；ECS `creationTime`（分钟精度）保持原值不动，两者并列展示。复用 SWR 缓存与强制刷新，BSS 不可用时软降级置空。
- 资产列表刷新按钮：一键强制实时查询（`refresh:true` 绕过 SWR 缓存直达阿里云全地域枚举，结果回写缓存），进页面/切 tab 仍走缓存。
- RBAC：`ecs:read` / `ecs:reveal` / `ecs:write` 三级 action，plugin.json 单源生成前后端常量；凭证仅存 `secureJsonData`。
- 稳定性：请求守卫三道闸（body 1MB、identities 2000 条、阿里云响应 4MB）、资产缓存 stale-while-revalidate（30 分钟硬上限）。
- 安全加固：`GF_SECURITY_SECRET_KEY` 经 `.env` 注入（不入库），grafana.db 中 AK/SK 不再依赖公开默认密钥加密。
- 工程化：ESLint + Prettier（`@grafana/eslint-config`）、Playwright e2e（面板菜单扩展红线回归 + RBAC 拦截回归）、GitHub Actions CI（build 双关卡 + e2e 双实例）、is-compatible 前端 API 兼容检查、dependabot、golangci-lint。
