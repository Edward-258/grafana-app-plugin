# AGENTS.md — local-ecs-app 开发须知

Grafana App 插件 `local-ecs-app`：阿里云 ECS 资产（ID/规格/地域）对齐已有 Prometheus dashboard。

> 本文件是项目**唯一知识库**（2026-10-05 起原 `docs/grafana-plugin-official-spec.md` 与 `.zcode/plans/` 的内容已全部并入此文件）。仓库里的 md 只保留三份：README（面向用户）、本文件（面向 agent，改动前先读）、CHANGELOG（官方打包必备）。

## 红线（违反即事故）

1. **UI 扩展双登记**：代码 `addLink()` 注册的扩展必须同时在 `src/plugin.json` 的 `extensions.addedLinks[]` 声明，缺一会静默消失（无 server 日志，只有浏览器 console 报错）。改动 plugin.json 或扩展注册后必须浏览器验证菜单仍存在。
2. **ECS IP 永不进浏览器**：后端 `Public()` 裁剪 + `TestPublicAssetOmitsIPs` 守护，任何 model/接口改动不得让 IP 到达前端。**告警数据源的帧是同一浏览器面**：`pkg/ds/server/frames.go` 字段白名单 + `TestFramesOmitIPs` 守护，新字段必须走白名单评审。
3. **AK 严格对应**：资产只能来自该 AK 的全地域枚举且唯一命中；任一地域查询失败则整体失败，禁止部分结果。（多 AK 细化：本意是**单 AK 内**全地域完整性——跨 AK 是独立账号，单 AK 刷新失败独立降级、全部失败才报错，不违背本意。）
4. **凭证只进 `secureJsonData`**：AK ID 与 Secret 都属加密存储（`jsonData` 禁存敏感值，多 AK 的 `akList` 只放插槽 uuid + 标签这类非敏感值；旧格式 `accessKeyId/accessKeySecret` 键仅作升级回退，同步完成后清空）；多对凭证 = `jsonData.akList`（slot uuid 主键 + label）+ `secureJsonData` 的 `ak:<slot>:id/secret` 键，≤50 对（`guardrails.maxAKPairs`，读时兜底）；保存时只发被修改的键（空字符串也会覆盖旧值，删除插槽靠它清键）。Viewer 只能见脱敏 AK ID（前3+后3），完整值仅 `ecs:reveal` 持有者可得（`/ecs/ak` 端点按角色按对下发）。**告警数据源的凭证由 app 后端启动时自动同步**（`sync.go` 整体同步全部插槽：幂等 = ds `akList` 与 app 插槽表全等 + 各键已配置，删除的插槽空串清键；SA 走数据源 API），不要在前端搬运 Secret——本进程外的已存密文前端读不到。
5. **财务数据两道门**：UI 响应 `attachBilling()`（ecs:reveal）之外，告警数据源 `account` 帧同样仅 Editor/Admin（`authorize`），且 QueryData **先鉴权后读设置**。已知取舍（文档已明示）：告警实例的触发值对能看告警的人天然可见，财务数字进告警即广播。

## RBAC 权限矩阵（plugin.json roles[] + 后端 requireAction）

| action                     | grants              | 能做什么                                                                                 |
| -------------------------- | ------------------- | ---------------------------------------------------------------------------------------- |
| `local-ecs-app.ecs:read`   | Viewer/Editor/Admin | 资产对齐端点（enrich/resolve）、脱敏 AK、告警数据源 `assets` 帧                          |
| `local-ecs-app.ecs:reveal` | Editor/Admin        | 完整 AK（小眼睛）、连通测试、**账户概览（余额/代金券/月账单）**、告警数据源 `account` 帧 |
| `local-ecs-app.ecs:write`  | 仅 Admin            | 改写 AK ID/Secret、配置页保存                                                            |

**告警数据源的角色门禁走同一套 roleActions 回退映射**（`pkg/ds/server` 的 `authorize` 复用 `handler.RoleHas`；QueryData 走 gRPC 无 id token，与匿名降级同语义）。评估态（PluginContext.User=nil，即告警引擎）放行取数。

实现形态：`plugin.json` 声明 `roles[]` 三个自定义 action + `iam` 段（`users.permissions:read`, scope `users:*`；`datasources:read/write` 是 sync 用的 Grafana 核心 action），includes 用 `action` 替代 `role` 做页面门禁；前端 `hasPermission()`（`@grafana/runtime`）门禁 UI；后端 `github.com/grafana/authlib/authz` EnforcementClient + `requireAction` 中间件读 `X-Grafana-Id` 头校验。给特定用户赋自定义角色需 Enterprise/Cloud；`grants` 自动授予内置角色在 OSS 可用（本项目依赖的就是 grants）。

**常量单源生成（SSOT）**：`src/plugin.json` 是唯一源头，`npm run build` 前置运行 `scripts/gen-permissions.js` 生成 `pkg/app/handler/zz_generated.go`（PluginID、三个 action 常量、roleActions 回退映射）和 `src/permissions.gen.ts`。**任何地方不得手写权限字符串**；新增 action 必须先在生成器 `SEMANTIC` 和守护测试 `zz_generated_test.go` 同时登记（故意制造摩擦）。改 plugin.json 后忘重新生成会被 `go test` 抓住（守护测试独立重推导比对 + 硬编码语义锚点）。插件 ID 的源头是 `package.json` 的 `name`（webpack 与生成器同源；Go 侧引用 `handler.PluginID`）。

- 后端 `pkg/app/handler/auth.go`：有 `X-Grafana-Id` 走官方 authz client；authz 出错或匿名时降级为 `PluginContext.User.Role` 的 org 角色映射（与 grants 一致，不放大权限）；无用户信息一律 403。
- 前端用 `hasPermission()`（`@grafana/runtime`）门禁；`?tab=config` 路径会被 Grafana 按 include 的 action 门禁匹配到 write，Viewer 直达被挡属预期。
- **权限结论必须以真实登录用户为准**：匿名会话会被前端导航守卫重定向回首页，即使权限 API 显示有权限（已实测踩坑）。
- 生效前提（compose 已配）：`GF_FEATURE_TOGGLES_ENABLE=externalServiceAccounts` **且** `GF_AUTH_MANAGED_SERVICE_ACCOUNTS_ENABLED=true`（后者官方文档没写，缺了 Grafana 不给插件配 service account）。
- 11.6 的 RBAC 角色在新 authz 存储：legacy `role` 表为空、`/api/access-control/roles` 404；验证有效权限用 `GET /api/access-control/user/permissions`（返回 action 列表）。
- 插件角色在插件启动注册时经 `DeclarePluginRoles` 登记，改 `roles[]` 重启即生效。
- **authlib 版本配对约束**：`github.com/grafana/authlib` 主包与 `authlib/types` 子包必须同日期 pseudo-version 配对（当前均 20260814）；`go get -u` 一把就能拉散，症状是 `types.GetUserPermissionsResponse undefined` 编译错误。修复：两个包一起 `go get ...@latest`。

## 已知隐患

- `extensions.addedLinks[].title` 官方要求 ≥10 字符，当前 "ECS 资产信息" 8 字符；升级 Grafana 后若扩展失联，先查这里。

## 构建与验证

```bash
npm run build                          # 生成 RBAC 常量 + 前端(webpack 双配置：app + 捆绑 ds) + 后端双二进制（dist/gpx_ecs_linux_amd64 + dist/datasource/gpx_ecs_ds_linux_amd64）
npm run lint                           # ESLint（@grafana/eslint-config）+ Prettier；lint:fix 自动修（scripts/、webpack.config.js 不参与）
golangci-lint run ./...                # 后端 lint（v2.13.2，配置 .golangci.yml；旧版二进制无法分析 go 1.26）
go vet ./... && go test ./...          # 后端（含 zz_generated 守护测试 + pkg/ds 帧门禁/IP 守护测试）
npx tsc --noEmit                       # 前端类型（含 tests/ 与 src/datasource/）
npm run e2e                            # Playwright e2e（tests/，12 用例）：面板菜单扩展红线 + RBAC 拦截 + 页面导航 + 告警数据源
docker compose up -d                   # Grafana 11.6 @ :3000（dev 模式，允许未签名 local-ecs-app 与 local-ecs-app-ds）
```

- **CI（GitHub Actions，推到 origin 后自动跑）**：`.github/workflows/ci.yml` 双 job——build（prettier/eslint/tsc/golangci/go test/build）与 e2e（起主实例 + viewer 对照实例，浏览器本地启动跑全套 Playwright）；`is-compatible.yml` 用官方 levitate 查前端 API 弃用（PR 时跑）；`dependabot.yml` 周更依赖（gomod 只放行 plugin-sdk）。本地 e2e 是「CDP 容器浏览器 + 172.17.0.1」拓扑、CI 是「本地浏览器 + localhost」拓扑，靠 `PW_CDP_ENDPOINT`/`GRAFANA_*_URL` 环境变量切换（见 tests/fixtures.ts 头注），代码零改动。

- `grafana-data` 命名卷持久化 grafana.db：`--force-recreate`/`restart` 不丢库，`docker compose down -v` 才删。
- **加密密钥走 `.env`**（不入库，模板 `.env.example`）：两个 compose 都读 `GF_SECURITY_SECRET_KEY` 加密 grafana.db 里的 secureJsonData；留空则 Grafana 用公开默认密钥（CI 临时容器即此情形，无真实 AK 无风险）。**换密钥后旧密文作废，需在配置页重录 AK/SK**。
- 改 `plugin.json` 后必须重建 + 重启 Grafana。**UI 行为验证首选 `npm run e2e`**（红线回归已固化成正式套件，跑 `panelMenu.spec.ts` 即浏览器断言菜单）；探索性调试仍可用 skill（CDP :9222，容器内访问宿主机用 `http://172.17.0.1:3000`）与 `~/.zcode/tools/pw-browser/` 下的临时脚本。e2e 拓扑注意：浏览器跑在 headless-shell 容器里，Node 侧用 `localhost`、浏览器侧用 `172.17.0.1`（见 `tests/fixtures.ts` 头注；`PW_CDP_ENDPOINT`/`GRAFANA_URL`/`GRAFANA_BROWSER_URL` 可覆盖）。容器不在时启动：`docker run -d --name headless-chrome --restart unless-stopped -p 127.0.0.1:9222:9222 chromedp/headless-shell --no-sandbox`。
- 权限回归对照实例：`docker compose -f docker-compose.yaml -f docker-compose.viewer.yaml up -d grafana-viewer`（3001 端口，匿名 Viewer；其库故意不持久，测完 down 掉归零）。e2e 的 viewer 项目（`rbac.spec.ts`）依赖它在跑，实例不在时自动 skip。手工下结论时用 Admin API 建真实 Viewer 用户（basic auth + 建号即删）+ curl 对照 `/ecs/ak`（masked 无 full）与 `/ecs/test`（403）。
- **告警链路实证方法**：provisioning API 实建规则（folder → `POST /api/v1/provisioning/alert-rules`，condition 引 `__expr__` reduce(last)+threshold），`GET /api/prometheus/grafana/api/v1/alerts` 看实例状态与标签流转；改规则时 PUT 报 500 就 DELETE 重建。

## 架构分层

- `src/` 前端（module.tsx 注册 root page / config page / 面板菜单扩展；`src/datasource/` 是捆绑告警数据源的前端，独立 webpack 配置产出 `dist/datasource/`；import 分层有约定，勿破坏）
- `pkg/aliyun/` 阿里云 OpenAPI（RPC 签名、全地域枚举、唯一命中匹配；BSS 补充链路 `CreationTimes()`——`business.aliyuncs.com` 的 QueryAvailableInstances 取订单口径租赁开始时间 `leaseStart`（独立字段，ECS creationTime 不动），软失败置空）
- `pkg/app/` 插件后端（ServeMux + httpadapter；资源端点 `/ecs/enrich|resolve|test|ak`，多 AK 按对下发 `billings[]`/`pairs[]`/`results[]`；探活走 SDK 的 CheckHealth 通道并点名失败账号；`auth.go` RBAC 中间件；`guardrails.go` 请求守卫——body 1MB/413、identities 2000 条/400、AK 对 50/400、阿里云响应 4MB 拒读，调整限额只动这一个文件；`settings.go` 解析凭证插槽 `akPairViews`/`credentialsFrom`（legacy 单对回退），键名构造器 `SecureKeyID/SecureKeySecret` 是 app 与 ds 的唯一 scheme；`sync.go` 启动时把全部插槽同步到告警数据源，iam 的 `datasources:read/write` 是 Grafana 核心 action、不经生成器）
- `pkg/app/service/` 解析器（`resolver.go` 缓存按 `Credential.ID` 分片：每 AK 独立 SWR/单飞/30 分钟硬上限，跨 AK 有限并发扇出 `forEachLimited`（≤4）；单 AK 内唯一命中才 matched、跨 AK 歧义 matched=false，单 AK 刷新失败独立降级、全部失败才报错；快照/账单/账户概览经 `Snapshot`/`Billings` 供 app 与 ds 消费）
- `pkg/ds/` 捆绑告警数据源后端（独立进程/二进制；`server/frames.go` 把 `service.Resolver.Snapshot` 的多账号快照组织成告警可查的宽序列帧——字段白名单、IP 禁入，序列带 `ak`/`akLabel` 标注来源账号、account 帧每账号一组三字段；`authorize` 角色门禁复用 `handler.RoleHas`，评估态放行；`settings()` 读 `akList` 插槽 + `ak:<slot>:*` 键，legacy 单键回退覆盖同步未跑完的窗口期）
- `provisioning/` Grafana 部署配置（plugins 启用 app；datasources 预置告警数据源实例 uid `ecs-ds`）

## 官方规范速查（Grafana 11.6 时代文档提炼 + 本项目对照）

> 来源：grafana.com/developers/plugin-tools 文档站 + GitHub `grafana/plugin-tools` 仓库 `docusaurus/docs/` 原始 Markdown（2026-09 阅读）。升级排错时的第一参考资料。

### 我们踩过的坑（最高优先级记忆）

- **UI 扩展双登记契约**：官方原话 "You must update your `plugin.json` metadata to list any registered extensions. **In future versions of Grafana, this will fail.**" 缺声明时运行时**静默拒绝**注册，只有浏览器 console 报 `Could not register link extension...`。回归事故：commit df624f6 删掉了 `extensions.addedLinks`，面板菜单 "ECS 资产信息" 消失，靠无头浏览器抓 console 才定位。
- **targets 两种写法都合法**：文档示例带 `/v1`（`grafana/dashboard/panel/menu/v1`），枚举值不带；Grafana 注册表会归一化，**用 `PluginExtensionPoints` 枚举常量最稳**（本项目现状）。

### 生命周期与加载

- 加载 6 阶段：扫描 plugin.json → 校验（签名/Angular）→ 后端初始化 → 注册 → 启动后端进程（崩溃自动拉起）→ 浏览器拉 module.js。
- App 前端加载模式：`preload: false` 惰性（点进 App 页才加载）；`preload: true` 随 Grafana 启动加载。本项目 `preload: true`，目的是扩展随启可用。
- 插件只加载一次，实例可多次初始化。

### plugin.json 要点（App 相关）

| 字段                             | 说明                                                                                                                                       | 本项目             |
| -------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------ | ------------------ |
| `id`                             | 必须匹配 `^[0-9a-z]+-([0-9a-z]+-)?(app\|panel\|datasource)$`                                                                               | `local-ecs-app` ✅ |
| `preload` / `autoEnabled`        | 随启加载 / 所有 org 启用并固定到导航                                                                                                       | true / true ✅     |
| `info.version` / `info.updated`  | `%VERSION%` / `%TODAY%` 占位符是官方 create-plugin 做法，构建时替换                                                                        | ✅                 |
| `includes[]`                     | page 支持 `role` / `action` / `addToNav` / `defaultNav` / `icon`；dashboard 用 `path` 指向 src 下 JSON，启用 App 时自动导入到 General 目录 | ✅                 |
| `backend` + `executable`         | executable 是二进制前缀，实际找 `gpx_ecs_linux_amd64` 等                                                                                   | ✅                 |
| `dependencies.grafanaDependency` | 写真正支持的下限；`addLink` 11.1 引入                                                                                                      | `>=11.1.0` ✅      |
| `extensions.addedLinks[]`        | `targets` + `title`(≥10字符) + `description` 必填                                                                                          | ⚠️ title 长度      |
| `state`                          | alpha/beta/stable，可选                                                                                                                    | 未设置             |
| `info.links`                     | 留空发布时会被打回（内部使用无所谓）                                                                                                       | 未设置             |

### 后端 App 规范

- 官方推荐结构（原话标注 "ServeMux (recommended)"）：`NewApp(ctx, AppInstanceSettings)` + `http.ServeMux` + `httpadapter.New(mux)`，入口 `app.Manage(...)`。**本项目完全一致。**
- 请求内取设置：`backend.PluginConfigFromContext(req.Context())`；前端调用：`getBackendSrv()` 打 `/api/plugins/<id>/resources/...`。
- 日志：错误用 `error` 级；一般信息用 `debug`，**不要用 `info`**。
- 后端禁止：读写本地文件、依赖环境变量存配置、执行任意代码。
- 健康检查 `CheckHealth`：未配置时返回 Unknown 而非 Error 是合理姿态。

### Secrets 与安全

- **`jsonData` 禁存敏感信息**（官方警告原话）。secret 必须 `secureJsonData`（落库加密）。
- **加密强度取决于 `GF_SECURITY_SECRET_KEY`**：不设置时 Grafana 用公开的内置默认密钥，拿到 grafana.db 即可解出 AK/SK（2026-09-23 已实证走通完整信封解密链：外层 `#dataKeyId#` → `data_keys` 表 → 默认密钥解 data key → 内层 pbkdf2 AES；新写入密文外层 base64 无填充、算法名是 base64 编码（`*YWVzLWNmYg*`=aes-cfb）、双层 pbkdf2+CFB）。
- 前端判断 secret 是否已配置：只看 `secureJsonFields.<key> === true`，永远拿不到值。
- 本项目自有红线（IP、AK 对应性、凭证插槽）见顶部「红线」。

### UI 扩展 API（11.4+ reactive API）

- 注册：`addLink` / `addComponent`（11.1+）、`addFunction`（11.6+）；暴露：`exposeComponent`（id 必须以插件 id 为前缀，如 `local-ecs-app/xxx/v1`）。
- 消费：`usePluginLinks` / `usePluginComponents` / `usePluginFunctions`（hook，带 `isLoading`）。
- `addLink` 常用参数：`targets`、`title`、`description`、`path` **或** `onClick`（二选一）、`group`（替代已弃用的 `category`）、`icon`、`configure()`（按 context 动态隐藏/改写）。
- `onClick` + `openModal`（helpers）是面板菜单弹窗的官方示例模式——本项目用法一致。
- 可用扩展点：DashboardPanelMenu、CommandPalette、UserProfileTab、ExploreToolbarAction、Alerting 系列等（详见官方 reference/extension-points）。

### App 前端最佳实践

- 多页面必须指定 root page（`defaultNav: true`）✅。
- `preload: true` 的 app 建议 code split（单资源 >250kb webpack 会警告）；本项目自有代码量小，暂不需要。
- **root page 用 React.lazy 时不要自己包 Suspense**（Grafana 会包）；`addConfigPage` 的组件需要自己包。
- 样式：只用 `@grafana/ui` 组件 + `useStyles2`/`useTheme2` + `@emotion/css`，禁止硬编码颜色/间距，禁止全局样式。
- 避免向客户端 ship `console.log`。

### 签名 / 打包 / 发布

- 开发阶段无需签名：`GF_DEFAULT_APP_MODE=development` + `GF_PLUGINS_ALLOW_LOADING_UNSIGNED_PLUGINS=<id>` 是官方认可的 dev 姿态（本项目 docker-compose 现状）。
- 生产默认拒绝未签名插件。私有签名：`npm run sign -- --rootUrls <grafana地址>`，需 `GRAFANA_ACCESS_POLICY_TOKEN`，**token 云账号必须匹配 plugin id 第一段**——`local-ecs-app` 第一段是 `local`，若将来要私有签名需把 id 改成 `edward-ecs-app` 形式（大动作：牵动 provisioning、后端、导航 URL）。
- 打包：dist 重命名为插件 id 再 zip；二进制 0755；CHANGELOG.md 必备。
- 发布审查会跑 plugin-validator（github.com/grafana/plugin-validator），CI 可集成。

### 11.6 → 12 迁移检查点

- 已移除（用了会报错）：`configureExtensionLink/Component`、`get/usePluginExtensions`、`get/usePluginLinkExtensions`、`get/usePluginComponentExtensions`、类型 `PluginExtensionLinkConfig/ComponentConfig`。**本项目全部用的是新 API，无障碍。**
- `Select`/`MultiSelect` 弃用 → `Combobox`/`MultiCombobox`。本项目未用 Select，无障碍。
- 升级后必做回归：面板菜单扩展（双登记 + title 长度）+ 配置页保存/测试。

## 落地记录与坑位台账（持续追加）

1. ⚠️ 扩展 title 改为 ≥10 字符（"查看 ECS 资产信息"），消除升级隐患。
2. ~~补 `CHANGELOG.md`~~ ✅ 2026-09-21 已补（连同 Apache-2.0 LICENSE）。
3. ~~资源端点加角色保护~~ ✅ 2026-09 已完成（见「RBAC 权限矩阵」）。
4. 若走出本机：决定私有签名 + 是否改 plugin id（见「签名 / 打包 / 发布」）。
5. ~~E2E 固化~~ ✅ 2026-09-21 已固化为正式 Playwright 套件（`tests/` + `playwright.config.ts`，`npm run e2e`）：面板菜单扩展红线（panelMenu.spec.ts，含弹窗断言）、匿名 Viewer RBAC 拦截（rbac.spec.ts，:3001 对照实例，未起自动 skip）、根页/资产页导航（appNavigation.spec.ts）。与官方模板的差异：模板的 `@grafana/plugin-e2e` fixtures 强制 Node 侧与浏览器侧同源，本项目浏览器在 CDP 容器里（跨网段）用不了，故用纯 `@playwright/test` + 自建 `goto` fixture（Node 走 localhost、浏览器走 172.17.0.1，见 tests/fixtures.ts 头注）。`~/.zcode/tools/pw-browser/` 脚本保留作探索性调试。
6. ~~LICENSE~~ ✅ 2026-09-21 已补。其余锦上添花：Magefile 跨平台构建、screenshots、`state` 字段、React.lazy 分页。
7. ~~Resolver 缓存改 stale-while-revalidate~~ ✅ 2026-09-21 已实施（resolver.go）：三岔逻辑（新鲜纯内存 / 过期回旧值+单飞后台刷新 / 超 30 分钟硬上限退化为同步刷新保证错误可见）；后台刷新用 `context.Background`（不能用请求 ctx，请求返回即取消）；刷新失败保留旧快照、清单飞标记、下次请求重试；fetch 函数可注入（resolver_test.go 覆盖冷阻塞/新鲜命中/过期回旧/10 并发单飞/硬上限/失败重试六条路径）。实测：冷 ~3.8s（含建连），热 3.7ms。已否决项见前文（落盘缓存/心跳轮询/地域长缓存/跳地域）。**2026-09-21 追加强制刷新通路**：`POST /ecs/enrich` body 加 `refresh:true` → resolver `instances(force)` 无视新鲜度同步实拉并回写缓存（前端刷新键语义，仅点击置位、进页面/切 tab 不带）；真机实测强制 6.8s/缓存 5.6ms/再强制 1.2s；`TestForceRefreshBypassesFreshCache` 守护。
8. 工程化补齐（2026-09-21）：ESLint（`@grafana/eslint-config`，根 `eslint.config.mjs`，未迁 .config/ 脚手架）+ Prettier（`.prettierrc.js`/`.prettierignore`）已落地；`react-hooks/set-state-in-effect` 降为 warn（现有 fetch-on-effect 模式，重构另行处理）。
9. ~~CI workflow、.golangci.yml~~ ✅ 2026-09-21 已落地（参照 grafana-zabbix 的复用方式调研结论）：`ci.yml` 双 job（build：prettier/eslint/tsc/golangci-lint v2.13.2/go vet+test/build；e2e：compose 起主实例+viewer 对照实例，Playwright 本地浏览器跑全套，失败上传 report）+ `is-compatible.yml`（grafana/plugin-actions levitate，PR 时查前端 API 弃用）+ `dependabot.yml`（npm 分组周更 + github-actions + gomod 仅放行 plugin-sdk，5 天 cooldown）+ `.nvmrc`(22) + `.golangci.yml`。**复用路径说明**：zabbix 用的 `grafana/plugin-ci-workflows` 是 Grafana Labs 内部库（发布 Plugin Catalog/GCS、内部 bot），外部走 create-plugin 模板 `templates/github/` + `grafana/plugin-actions` 原子 action——本项目即此路径。e2e 拓扑：本地=CDP 容器浏览器+172.17.0.1，CI=本地浏览器+localhost，`PW_CDP_ENDPOINT=local` 切换。release.yml 暂不做（未签名、id 与签名 token 不匹配，见「签名 / 打包 / 发布」）。Jest 前端单测仍未做（第 5 批次）。
10. golangci-lint 首跑清了三笔旧账（2026-09-21）：`client.go` defer Body.Close 显式忽略错误、`auth.go` 弃用的 `backend.GrafanaConfigFromContext` 换公共 `config.GrafanaConfigFromContext`、`guardrails.go` 错误串首字母小写。
11. ~~BSS 升级路径（部分）~~ ✅ 2026-09-23 落地「租赁开始时间」独立字段（`leaseStart`）：`client.CreationTimes()` 走 `business.aliyuncs.com` 的 `QueryAvailableInstances`（注意 `bssopenapi.aliyuncs.com` 是 NXDOMAIN 死域名）；`resolver.applyBss` 在快照构建时写入 `list[i].LeaseStartTime`（**独立字段，ECS `creationTime` 保持原值不动，两者并列展示**——ECS 分钟精度 vs BSS 秒级精度，实测 `06:49Z` vs `06:49:48Z`），SWR/单飞/force/CheckHealth 四条路径全部自然复用；BSS 失败软降级置空（error 日志），前端列表"租赁开始"列 + 弹窗行。**初版曾把 BSS 值覆盖到 creationTime，用户拍板改为双字段并列（语义不同：实例创建时刻 vs 订单开通时刻）。** 两个坑已修：① `call()` 错误信封会把 BSS 成功响应（`Code:"Success"`+`Message`）误判为错——信封加 `Success *bool` 字段（ECS 无此字段维持原判断）；② BSS `Version:2017-12-14` 经 action map 覆写 `call()` 里 ECS 的默认版本（merge 顺序天然支持，零重构）；③ 服务端按 `ProductCode=ecs` 筛仍混入 sas 等产品，需客户端二次过滤；④ BSS 的按量付费 EndTime 哨兵是 **2999**（ECS 侧是 2099），本期只采 CreateTime 未踩到，将来若采 EndTime 注意。剩余未做：全产品到期预警、QueryInstanceBill 成本分摊。
12. 账户概览（2026-09-23，参照 cloudscope 落地）：`model.AccountOverview`（余额/现金/信用/**代金券**/当月账单聚合）随 SWR 快照缓存，enrich 响应以 `billing` 对象下发，前端资产页顶部概览栏（余额·代金券·当月实付·Top 产品）+ PostPaid 单元格"按量付费（余额 ¥x）"。API：QueryAccountBalance + **QueryCashCoupons**（代金券软失败记 0）+ **QueryInstanceBill**（BillingCycle=YYYY-MM、PageSize=300 分页、按产品聚合实付、降序；分页上限 20 页）。**金额形态坑（实测）**：QueryAccountBalance 金额是字符串（"7.36"，可能带千分位逗号"150,000.00"），QueryInstanceBill 的 PretaxAmount 却是裸数字（108.13）——`model.ParseMoneyAny` 兼容两种形态，千分位剥离记入 cloudscope 借鉴。事实澄清：按量付费没有实例级额度，消耗的是账户余额池（现金+信用）。软失败分级：代金券失败记 0、账单失败保留余额、整体失败 Billing() ok=false 省略下发。**权限档位（2026-09-23 用户拍板）：billing 仅 ecs:reveal（Editor/Admin）可见**——`attachBilling()`（enrich/resolve 共用）按 `hasAction(actionReveal)` 下发，Viewer 的响应无此对象、前端"有数据才显示"自动隐藏；已用主实例真实 Viewer 用户（basic auth + Admin API 建号即删）实证：Viewer enrich/resolve 均无 billing、资产本体照常、ak 无 full、/ecs/test 403。弹窗（resolve 链路）同样带 billing：PostPaid 到期行显示"按量付费（余额 ¥x）"（Billing/fmtMoney 移至 EcsInfo.tsx 共享）。
13. 算力包（毕设包）可达性调研（2026-09-23，实证）：控制台"算力包剩余额度 749.84/850 元"对应的资源**是省钱计划（SavingPlan）**——QueryAvailableInstances 全量列表可见 `ProductCode:"savingplan"` 条目（spn-1a5ecc28RkRI0k0h，2026-09-14→2027-03-14，6 个月期，RenewStatus ManualRenewal），即**存在性/有效期/续费状态 BSS 可查**（可零成本挂在现有 fetchBss 链路展示有效期）。但**剩余额度数字未打通**：对应公开 API `DescribeSavingsPlansUsageTotal/Detail`（节省计划使用率）AK 有权限（报错是参数校验非权限），PeriodType 合法枚举试遍 Month/Period/1/2/Day 均被拒（文档 JS 渲染拿不到枚举表），待 OpenAPI 门户在线调试确认后再接。`QueryResourcePackageInstances`（传统资源包）返回空——证实算力包不是资源包体系。**顺带挖出两个正式接入的实现坑**：① BSS 时间参数格式是 `yyyy-MM-dd HH:mm:ss`（含空格），RPC 签名时空格编码问题~~接入含空格参数前必须修~~ **已闭环（2026-10-03 核对代码 + git 考证）**：`rpc/sign.go` 的 `encode()` 自初版（68ac59d）即内置 `+`→`%20`，且 Go 的 QueryEscape 本就把 `*`→`%2A`、不转义 `~`——三行替换里仅第一行承重，后两行是对官方 Java URLEncoder 伪代码的照搬（在 Go 里冗余无害）；字面 `+` 先转 `%2B` 不被空格替换误伤；请求体 `form.Encode()` 产 `+` 属表单传输惯例（网关解码后才参与签名比对，与签名规范化两层各归各）。此前会话记的「必须修」系凭通用坑印象、未核对代码。`sign_test.go` 以编码表 + 已知答案（golden HMAC）双测试守护；② Grafana 新写入的 secureJsonData 密文形态见「Secrets 与安全」（离线解密探针已全链路打通，脚本已销毁）。
14. Grafana Alerting 集成（2026-09-24，用户拍板「引导式」：**规则由用户在 Alerting UI 自建，插件不碰 provisioning/ruler API**，插件负责让数据可查 + 引导卡/深链）。核心事实：① **Unified Alerting 只能查询已注册且有后端的数据源插件**——app 插件要让自己的数据可告警，唯一通道是捆绑 datasource（`includes: [{type:"datasource", path:"datasource/plugin.json"}]`，官方先例 grafana-iot-twinmaker），manifest 需 `backend:true` + `alerting:true`；② **SDK `backend.Manage` 单次只服务一个 pluginID，app+ds = 双可执行文件双进程**（`gpx_ecs` + `dist/datasource/gpx_ecs_ds_linux_amd64`，webpack 多配置数组分别产 module.js；`GF_PLUGINS_ALLOW_LOADING_UNSIGNED_PLUGINS` 要把 `-ds` 也加进去）；③ 数据源实例设置独立于 app 插件设置存储（`data_source` 表 vs `plugin_setting` 表）——**`POST /api/plugins/<ds-id>/settings` 写的是后者，ds 后端读不到**，同步凭证必须走数据源 API（`PUT /api/datasources/uid/:uid` 需完整主体 name/type/access...，只发 secureJsonData 会 400）。同步做在 app 后端启动时（`sync.go`，SA 需要 iam 声明 Grafana 核心 action `datasources:read`+`datasources:write`）；④ **SSE（服务端表达式）只吃「宽序列」**：帧必须 time 列 + 数值列，每序列一列——每台实例一个带 `data.Labels` 的 `daysToExpire` 字段（标签随告警实例带出，可直接用于通知路由），宽表多行形态会报 `input data must be a wide series but got type long`；**④a 数值列之外混入任何字符串列（初版 account 帧的 currency/billingCycle）同样被判 long 而拒收——附加信息一律走字段 labels**（`TestAccountFrameSchemaStable` 有"禁止非数值列"守护）；**④b 同帧多条序列的标签集必须唯一**：account 帧三条序列若只挂 currency/billingCycle 会因 reduce 结果 labels 撞车被整体拒绝（`frame cannot uniquely be identified by its labels`），须加 `metric` 标签（值=字段名）区分——"查询返回正确帧"与"告警引擎接受该帧"是两个独立断言，DS 查询测试覆盖不到后者，**必须实建规则走一遍评估**（rule editor Preview 即可，报错看 `[sse.readDataError]`/`invalid format of evaluation results`）；SSE reduce 的合法 reducer 是 `last` 不是 UI 显示的 `lastMin`（会报 not implemented）；告警规则 `relativeTimeRange` 不能 0/0（invalidRelativeTime）；④c **告警帧按计费方式分域**（2026-09-24 用户拍板）：assets 只含 PrePaid（按量付费无到期概念，null 序列只是图表噪音）；account 的 billTotal 只统计 PostPaid 当月实付——`client.InstanceBills` 解析 QueryInstanceBill 的实例级明细（补解析 InstanceID 字段），与快照计费方式求和；BSS 响应无 InstanceID（契约校验：有账单行却无一个 InstanceID 即报错）或实例账单失败时 billTotal 置 null 走 NoData，绝不发 0 冒充真实账单。**实测知识：按量付费实付可被省钱计划抵扣为 0**（spn 条目按摊销计 39.07，两条按量实例行 0.00）——产品聚合行「云服务器 ECS 107.93」= 包年包月续费 68.86 + 省钱计划 39.07，实例级与产品级口径不同；⑤ **告警引擎评估请求无用户上下文**（PluginContext.User=nil）——ds 门禁按「无用户=评估态放行、有用户=按 org 角色映射」设计（SDK `backend.User` 只有 Role 无 id token，QueryData 面不存在 authz 路径），已实建规则实证：到期实例 Alerting、长期实例 Normal、instanceId 标签流转；⑥ QueryData 先鉴权后读设置（未授权调用者不能借错误文案探测凭证配置状态）；`/api/ds/query` 对查询错误回 HTTP 400（错误体仍在 results）。**广播属性（文档明示）**：告警实例的触发值对所有能看告警的人（Viewer 默认可读告警列表）可见——财务数字进告警即广播，与插件 UI 的 ecs:reveal 门禁并存不冲突。已知取舍：ECS 数据不进 Prometheus（后端插件无自有监听端口，push 方案需引入 Pushgateway），故告警查询必须走捆绑 ds 这条路。
15. 多 AK/SK 并发查找（2026-10-03，用户拍板存储选型前先要方案，批准计划）：**存储走官方插件设置 API，不直写 grafana.db、不引入 MySQL**——直写 grafana.db 绕过官方契约（迁移/缓存/schema 都在 Grafana 手里）且 SQLite 单写锁，Grafana 换库即全废；MySQL 对「低频写的配置数据」是过度工程（部署依赖 + 插件自建加密 + DSN 引导悖论——DSN 本身还得存 secureJsonData）；`secureJsonData` 多键就是「数据分离」的官方形态（物理在 grafana.db 的 `plugin_setting` 表但那是插件隔离存储，自带 AES、零部署）。**键名 scheme 用前端生成的随机 slot uuid 而非 AK ID 哈希**（`jsonData.akList=[{slot,label}]` + `secureJsonData` 的 `ak:<slot>:id/secret`）——前端「加号新增插槽」无需在浏览器算 sha256，jsonData 零敏感物料，比承诺哈希更彻底；`handler.SecureKeyID/SecureKeySecret` 是唯一 scheme（app 读、sync 写、ds 读共用）。**legacy 兼容三层**：app `akPairViews` 对 slot=legacy 回退读旧键 `accessKeyId/accessKeySecret`（jsonData 明文是最老一层）、akList 缺失整个回退单 legacy 插槽、ds `settings()` 同样回退（覆盖「app 已升级、sync 未跑完」窗口期）——迁移期每个中间状态都可用，Secret 明文不可读所以旧 Secret 无法静默搬迁，用户补填一次即落位。**并发模型**：Resolver 缓存按 `Credential.ID` 分片，每 AK 独立 SWR/单飞/硬上限，`forEachLimited`（≤4，不引 x/sync）；缓存命中额外要求 `akID` 比对——同插槽换 AK 旧快照立即作废（否则新账号读到旧资产，重大事故点）。**跨 AK 匹配语义**：单 AK 内唯一命中才 matched（`MatchIdentity` 不变）；多 AK 各命中一台 = 歧义 matched=false + note 点名（绝不猜一个）；**拍板：单 AK 刷新失败独立降级**（snapshots 剔除失败者，帧里无该 AK 序列 → 规则评估 NoData；全部失败才整体报错保失败可见）；Ensure/CheckHealth 例外：全部 AK 都要点名报错（健康检查要的就是全量可用）。**帧形态**：assets 每序列加 `ak`/`akLabel` 标签；account 帧每账号一组三字段（availableAmount/couponAmount/billTotal），同名序列靠 labels 区分是 assets 帧既有先例（见台账 14④），跨 AK 标签集唯一性由 ak（uuid）保证；实例账单/概览按账号缓存随各自快照生灭。**响应形态 breaking（同仓同发无外部消费者）**：`/ecs/ak` → `{configured, pairs[]}`（含配置不全的插槽，secretConfigured 标记供 UI 呈现待补全态）；`/ecs/test` → `{ok, results[]}`（逐 AK count/regions/error 内联）；enrich/resolve 的 `billing` → `billings[]`（带 ak/akLabel），前端 `pickBilling` 按实例来源 AK 匹配、单份兜底；实例行/帧带 `ak`，资产列表多 AK 时才显示「AK 账号」列（单账号版式不变）。**sync 幂等升级**：ds `jsonData.akList` 与 app 插槽表 slot+label 全等 + 各键 secureJsonFields=true + legacy 键已清 → 跳过；删除插槽 = 空串覆盖清键；旧 `alertingDsAKHash` 标记退役。上限 50 对（`guardrails.maxAKPairs`，读时兜底——设置保存不经插件 handler，写入侧只能靠 UI 约束）。活实例实证：重启后 sync 把 ds 迁移到 `akList:[{slot:legacy,label:默认}]` + `ak:legacy:*` 键、健康 OK、e2e 通过。

## 附录：历史实施计划（原 .zcode/plans/，按时间序）

### 计划一（2026-09-21，✅ 已完成）：ECS 租期信息采集——CreationTime / ExpiredTime / 计费方式全链路透出

**口径（已确认）**

- **租赁开始 ≈ CreationTime**（实例创建即首次购买起点，续费不更新；不接 BSS 订单 API）
- **DescribeInstances 的 `StartTime` 弃用**——官方语义是"最近一次开机时间"，与租期无关，绝不采用
- **ExpiredTime 仅包年包月（PrePaid）有意义**；按量付费（PostPaid）无到期，显示"按量付费"
- 展示：资产列表加"创建时间/到期时间"两列（插在地域之后），面板弹窗加行；不做到期高亮

**关键事实：数据已在链路里，只是被丢弃。** 每 5 分钟的全地域扫描响应中本来就带这三个字段，`parse.go` 的 `rawInstance` 没声明它们而被 `json.Unmarshal` 静默丢弃。resolver/handlers/缓存零改动——`Public()` 白名单放行后字段自动流经 enrich/resolve 两个端点。

**改动**：`parse.go` rawInstance 加三字段原样透传（不解析时间，解析留给前端）；`model/instance.go` Instance/PublicAsset 加字段 + `Public()` 白名单拷贝；`EcsInfo.tsx` fmtTime 工具（无秒 ISO 归一化后转本地时区）+ 弹窗三行；`App.tsx` 表格两列 + 搜索过滤补字段。**不做**：BSS 订单 API（后续台账 11 部分落地）、`StartTime`、到期高亮/排序、AutoReleaseTime。

### 计划二（2026-09-24，✅ 已完成）：ECS 插件复用 Grafana Alerting——捆绑数据源 + 引导式建规则

**目标形态**：用户在 Grafana Alerting UI 里自己创建规则（插件不做一键建规则、不碰 provisioning API），插件负责让数据可查（捆绑 ds）+ 引导卡/深链。

**已核实的技术事实**（详细展开见台账 14）：Grafana 11.6 Alerting 只能查询已注册且有后端的 ds 插件，app 经 `includes` 捆绑 ds（twinmaker 先例）；SDK `backend.Manage` 单次只服务一个插件 → 双二进制方案（`gpx_ecs` + `gpx_ecs_ds` 双进程）；ds 实例设置独立存储 → 凭证同步走数据源 API（SA + sync.go）。

**五阶段**：P1 捆绑 ds 骨架（plugin.json/module.tsx/webpack 多配置/CopyWebpackPlugin）→ P2 数据面 pkg/ds（宽序列帧、红线复检、财务门禁）→ P3 凭证同步 → P4 引导卡 + `/alerting/list` 深链（卡内余额示例仅 ecs:reveal 可见）→ P5 provisioning/e2e/文档。**明确不做**：插件内一键创建规则、自定义联系人点类型、新增 RBAC action。

### 计划三（2026-10-03，✅ 已实现、随多 AK 改造待提交）：多 AK/SK 并发查找

**存储选型**（三路对比后拍板 secureJsonData 多键，完整论证见台账 15）：

| 方案                                      | 结论                | 理由                                                                                                                                                                    |
| ----------------------------------------- | ------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 直连 grafana.db（自建表/读写 Grafana 表） | ❌ 否决             | 绕过官方契约（迁移/schema/缓存全在 Grafana 手里）；SQLite 单写锁，并发写反而最差；Grafana 将来换库（MySQL/Postgres 后端）时全部作废                                     |
| 另起 MySQL 存插件数据                     | ❌ 否决（过度工程） | AK/SK 是**低频写的配置数据**（几十对以内）；真实成本：多一个部署依赖、插件要自建加密、DSN 引导悖论（DSN 本身还得存 secureJsonData）、后端插件规范禁止依赖环境变量存配置 |
| **secureJsonData 多键（采用）**           | ✅                  | 「数据分明」的官方形态：Grafana 划给插件的隔离存储 + 自带 AES + 零新增部署 + 与 ConfigPage 既有「只发被修改的键」模式无缝衔接                                           |

并发查找与存储正交：**并发发生在 Resolver 层**（多缓存分片 + goroutine 扇出），这才是改动主体。

**数据模型**：`jsonData.akList: [{slot: uuid, label}]`（jsonData 不存 AK 明文）+ `secureJsonData` 的 `ak:<slot>:id/secret`；现存单对 legacy 键视为隐式第一对；`maxAKPairs = 50`（guardrails，超出 400）。

**七步实施**：① 存储层（service.Credential、credentialsFrom 多键解析 + legacy 兜底、SecureKeyID/Secret 键名构造器）→ ② Resolver 多 AK 分片 + 并发扇出（缓存按 Credential.ID 分片、errgroup 有限并发 ≤4、跨 AK 命中语义、单 AK 独立降级）→ ③ 资源端点适配（billing→billings[]、/ecs/ak pairs[]、/ecs/test per-AK results[]、CheckHealth 点名失败 AK）→ ④ 凭证同步（遍历全部插槽、删除插槽清键）→ ⑤ 告警数据源（序列加 ak/akLabel 标签、account 帧每账号一组）→ ⑥ 前端（ConfigPage AK 列表管理、概览栏按 billings[] 渲染、列表 AK 列）→ ⑦ 验证与文档。

**影响面**：响应形态 breaking（同仓同发无外部消费者）；已建告警规则不破坏（只新增标签/序列）；RBAC 不变、plugin.json 不动。
