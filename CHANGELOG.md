# Changelog

## 1.1.0 (Unreleased)

### Features

- 多 AK/SK 支持与并发查找：配置页「阿里云 ECS」区改为**插槽列表**（加号新增一对 AK/SK，≤50 对；删除带确认、换 AK、重输 Secret，保存只发被修改的键）。存储走官方插件设置 API：`jsonData.akList`（slot uuid + 标签，零敏感物料）+ `secureJsonData` 的 `ak:<slot>:id/secret` 键（各自加密）——不直写 grafana.db、不引入 MySQL（取舍记录见 AGENTS.md 台账 15）。Resolver 按插槽分片缓存（每 AK 独立 SWR/单飞/硬上限，同插槽换 AK 立即失效），enrich/告警查询跨 AK 有限并发扇出（≤4）；单 AK 内唯一命中才 matched，跨 AK 歧义 matched=false 并点名冲突账号，单 AK 刷新失败独立降级不拖垮其它 AK（全部失败才整体报错）。告警帧随行新增 `ak`/`akLabel` 标签（assets 每序列、account 每账号一组三字段，标签集靠 ak 保证唯一），account 帧按账号展开；`/ecs/ak`、`/ecs/test` 改为按对下发（响应 `pairs[]`/`results[]`，enrich/resolve 的 `billing` 改为 `billings[]`），CheckHealth 点名失败账号。旧单对凭证自动以 legacy 插槽兼容（app 与 ds 侧均有回退），保存一次即完成插槽迁移，无需手工操作。
- Grafana 告警集成：捆绑后端数据源 `local-ecs-app-ds`（app+datasource 组合，`includes` 登记 + provisioning 预置 uid `ecs-ds`），把资产/账单快照暴露为告警规则可查询的数据帧。`assets` 帧为宽序列（time + 每实例一个带标签的 `daysToExpire`，标签 instanceId/name/type/region/chargeType 随告警实例带出），**只含包年包月实例**；`account` 帧单行（availableAmount/couponAmount 账户级 + **billTotal 仅统计按量付费实例**当月实付，带 chargeType=PostPaid 标签，实例级账单不可用时置 null 绝不发 0）。**规则在 Grafana Alerting UI 由用户自建**（资产页引导卡 + `/alerting/list` 深链），已实证全链路：实建规则 → 告警引擎无用户上下文取数评估 → 到期实例 Alerting、长期实例 Normal。
- 告警数据源凭证自动同步：app 后端启动（含配置保存触发的实例重建）时以 service account（plugin.json `iam` 新增 Grafana 核心 action `datasources:read/write`，`users.permissions:read` 先例）把配置页 AK 经数据源 API 写入 ds 的 `secureJsonData`——Secret 只能写不能读，前端无法搬运已存密文；幂等改为 ds `jsonData.akList` 与 app 插槽表的整体比对（slot uuid 非敏感），增删插槽/换标签/换 AK 自动重同步，删除的插槽空串清键。
- 告警数据源权限分档与插件 UI 一致：`assets` 帧 `ecs:read`（Viewer 可读）、`account` 帧 `ecs:reveal`（Editor/Admin，与 `attachBilling` 同档），先鉴权后读设置（不向未授权调用者泄漏凭证配置状态）；评估态（无用户上下文）放行供告警引擎取数。注意：告警实例的触发值对「能看告警的人」天然可见（财务数字进告警即具广播属性）。

### Fixed

- account 帧对告警引擎不可用：帧内混入 currency/billingCycle 字符串列导致整帧被判 long 形态、SSE 拒收（`input data must be a wide series`）；改为数值字段上的 labels 后又因三条序列 labels 撞车被拒（`frame cannot uniquely be identified`）——最终形态为 time + 三个带 `metric`/`currency`/`billingCycle` 标签的数值字段。已实建余额规则（availableAmount < 100）实证：三实例按 metric 标签区分、Alerting/Normal 状态符合数值预期、编辑器 Preview 正常。附带修复：查询/门禁断言通过不代表告警引擎接受帧，新增「禁止非数值列」守护测试。
- 告警帧按计费方式分域（用户拍板）：`assets` 到期帧只输出包年包月实例（按量付费无到期概念，null 序列只是图表噪音）；`account` 帧 `billTotal` 只统计按量付费实例当月实付（新增 `client.InstanceBills` 解析 QueryInstanceBill 的实例级明细，与快照计费方式求和；BSS 响应无 InstanceID 或实例账单失败时 billTotal 置 null 走 NoData，绝不发 0 冒充）。实测发现：按量付费实付可能被省钱计划抵扣为 0，属正常数据。

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
