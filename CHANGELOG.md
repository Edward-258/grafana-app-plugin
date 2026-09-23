# Changelog

## 1.1.0 (Unreleased)

### Features

- Grafana 告警集成：捆绑后端数据源 `local-ecs-app-ds`（app+datasource 组合，`includes` 登记 + provisioning 预置 uid `ecs-ds`），把资产/账单快照暴露为告警规则可查询的数据帧。`assets` 帧为宽序列（time + 每实例一个带标签的 `daysToExpire`，标签 instanceId/name/type/region/chargeType 随告警实例带出）；`account` 帧单行（availableAmount/couponAmount/billTotal）。**规则在 Grafana Alerting UI 由用户自建**（资产页引导卡 + `/alerting/list` 深链），已实证全链路：实建 `daysToExpire < 1000` 规则 → 告警引擎无用户上下文取数评估 → 到期实例 Alerting、长期实例 Normal。
- 告警数据源凭证自动同步：app 后端启动（含配置保存触发的实例重建）时以 service account（plugin.json `iam` 新增 Grafana 核心 action `datasources:read/write`，`users.permissions:read` 先例）把配置页 AK 经数据源 API 写入 ds 的 `secureJsonData`——Secret 只能写不能读，前端无法搬运已存密文；幂等标记是 AK ID 的 sha256 前缀（存 ds `jsonData`，承诺值不泄漏本体），换 AK 自动重同步。
- 告警数据源权限分档与插件 UI 一致：`assets` 帧 `ecs:read`（Viewer 可读）、`account` 帧 `ecs:reveal`（Editor/Admin，与 `attachBilling` 同档），先鉴权后读设置（不向未授权调用者泄漏凭证配置状态）；评估态（无用户上下文）放行供告警引擎取数。注意：告警实例的触发值对能看告警的人天然可见（财务数字进告警即具广播属性）。

## 1.0.0 (Unreleased)

Initial release.

### Features

- ECS 资产对齐：从 Prometheus 采集监控标识，后端用阿里云 AK 全地域枚举并唯一命中，前端展示 ECS ID/规格/地域/租期（公网/内网 IP 永不出后端）。
- 租期信息：展示 CreationTime / ExpiredTime / 计费方式，按量付费实例屏蔽 2099 哨兵到期值。
- 账户概览（参照 cloudscope）：BSS 余额/现金/信用/代金券 + 当月账单按产品聚合（QueryInstanceBill 实付口径），随 SWR 快照缓存经 enrich/resolve 下发；资产页顶部概览栏 + 弹窗/详情 PostPaid 余额显示；**仅 ecs:reveal（Editor/Admin）可见，Viewer 响应不含**（真实 Viewer 用户实证）；金额兼容字符串/数字两种形态并剥离千分位逗号。
- 精确创建时间（BSS 补充链路）：`QueryAvailableInstances` 订单口径时间以独立字段 `leaseStart` 展示（列表"租赁开始"列 + 弹窗行，秒级精度）；ECS `creationTime`（分钟精度）保持原值不动，两者并列展示。复用 SWR 缓存与强制刷新，BSS 不可用时软降级置空。
- 资产列表刷新按钮：一键强制实时查询（`refresh:true` 绕过 SWR 缓存直达阿里云全地域枚举，结果回写缓存），进页面/切 tab 仍走缓存。
- RBAC：`ecs:read` / `ecs:reveal` / `ecs:write` 三级 action，plugin.json 单源生成前后端常量；凭证仅存 `secureJsonData`。
- 稳定性：请求守卫三道闸（body 1MB、identities 2000 条、阿里云响应 4MB）、资产缓存 stale-while-revalidate（30 分钟硬上限）。
- 安全加固：`GF_SECURITY_SECRET_KEY` 经 `.env` 注入（不入库），grafana.db 中 AK/SK 不再依赖公开默认密钥加密。
- 工程化：ESLint + Prettier（`@grafana/eslint-config`）、Playwright e2e（面板菜单扩展红线回归 + RBAC 拦截回归）、GitHub Actions CI（build 双关卡 + e2e 双实例）、is-compatible 前端 API 兼容检查、dependabot、golangci-lint。
