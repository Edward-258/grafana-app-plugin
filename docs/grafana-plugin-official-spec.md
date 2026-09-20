# Grafana App Plugin 官方开发规范笔记（local-ecs-app 对照版）

> 来源：grafana.com/developers/plugin-tools 文档站 + GitHub `grafana/plugin-tools` 仓库 `docusaurus/docs/` 原始 Markdown（2026-09 阅读，对应 Grafana 11.6 时代文档）。
> 用途：本项目开发时的规范速查 + 升级排错时的第一参考资料。

## 0. 我们踩过的坑（最高优先级记忆）

### 0.1 UI 扩展双登记契约
代码里 `addLink()`/`addComponent()` 注册的扩展，**必须同时在 `plugin.json` 的 `extensions.addedLinks[]` / `addedComponents[]` 里声明**。官方原话：

> "You must update your `plugin.json` metadata to list any registered extensions. **In future versions of Grafana, this will fail.**"

- 缺声明时：运行时**静默拒绝**注册，只有浏览器 console 报 `Could not register link extension...`，Grafana server 日志干净。
- 我们的回归事故：commit df624f6 删掉了 `extensions.addedLinks`，面板菜单 "ECS 资产信息" 消失，靠无头浏览器抓 console 才定位。
- **任何改动 plugin.json 或 module.tsx 扩展注册的提交，必须跑浏览器断言验证菜单仍存在。**

### 0.2 addedLinks.title 最短 10 字符
官方 schema 要求 `addedLinks[]` 每项 `title` minLength 10。当前 title "ECS 资产信息" 只有 8 字符，11.6 dev 模式未拒绝（功能正常），但**升级 Grafana 后若扩展再失联，第一个查这里**。稳妥写法：改为 10 字符以上（如 "查看 ECS 资产信息"）。

### 0.3 targets 两种写法都合法
- 文档示例带 `/v1`：`grafana/dashboard/panel/menu/v1`
- `PluginExtensionPoints.DashboardPanelMenu` 枚举值不带 `/v1`
- Grafana 注册表会归一化，**用枚举常量最稳**（本项目现状）。

## 1. 生命周期与加载

- 加载 6 阶段：扫描 plugin.json → 校验（签名/Angular）→ 后端初始化 → 注册 → 启动后端进程（崩溃自动拉起）→ 浏览器拉 module.js。
- App 前端加载模式：`preload: false` 惰性（点进 App 页才加载）；`preload: true` 随 Grafana 启动加载。本项目 `preload: true`，目的是扩展随启可用。
- 插件只加载一次，实例可多次初始化。
- 改 plugin.json 后必须重建 + 重启 Grafana 才生效。

## 2. plugin.json 要点（App 相关）

| 字段 | 说明 | 本项目 |
|---|---|---|
| `id` | 必须匹配 `^[0-9a-z]+-([0-9a-z]+-)?(app\|panel\|datasource)$` | `local-ecs-app` ✅ |
| `preload` / `autoEnabled` | 随启加载 / 所有 org 启用并固定到导航 | true / true ✅ |
| `info.version` / `info.updated` | `%VERSION%` / `%TODAY%` 占位符是官方 create-plugin 做法，构建时替换 | ✅ |
| `includes[]` | page 支持 `role` / `action` / `addToNav` / `defaultNav` / `icon`；dashboard 用 `path` 指向 src 下 JSON，启用 App 时自动导入到 General 目录 | ✅ |
| `backend` + `executable` | executable 是二进制前缀，实际找 `gpx_ecs_linux_amd64` 等 | ✅ |
| `dependencies.grafanaDependency` | 写真正支持的下限；`addLink` 11.1 引入 | `>=11.1.0` ✅ |
| `extensions.addedLinks[]` | `targets` + `title`(≥10字符) + `description` 必填 | ⚠️ title 长度 |
| `state` | alpha/beta/stable，可选 | 未设置 |
| `info.links` | 留空发布时会被打回（内部使用无所谓） | 未设置 |

## 3. 后端 App 规范

- 官方推荐结构（原话标注 "ServeMux (recommended)"）：`NewApp(ctx, AppInstanceSettings)` + `http.ServeMux` + `httpadapter.New(mux)`，入口 `app.Manage(...)`。**本项目完全一致。**
- 请求内取设置：`backend.PluginConfigFromContext(req.Context())`。
- 前端调用：`getBackendSrv()` 打 `/api/plugins/<id>/resources/...`。
- 日志：错误用 `error` 级；一般信息用 `debug`，**不要用 `info`**。
- 后端禁止：读写本地文件、依赖环境变量存配置、执行任意代码。
- 健康检查 `CheckHealth`：未配置时返回 Unknown 而非 Error 是合理姿态。

## 4. Secrets 与安全红线

- **`jsonData` 禁存敏感信息**（官方警告原话）。secret 必须 `secureJsonData`（落库加密）。
- 前端判断 secret 是否已配置：只看 `secureJsonFields.<key> === true`，永远拿不到值。
- 保存时只发送**被用户修改的** secret 键；发空字符串也会覆盖旧值（本项目 ConfigPage 的 `secureJsonData: secret ? {...} : undefined` 模式正确）。
- **本项目自有红线（比官方更严）：任何 ECS IP（内网/公网）不得到达浏览器**。后端 `Public()` 裁剪 + `TestPublicAssetOmitsIPs` 守护，改动 model 时不可绕过。
- **AK 对应性红线**：资产列表只能展示这把 AK 枚举出来且能唯一命中的实例；全地域枚举任一地域失败则整体失败，不许部分结果。

## 5. UI 扩展 API（11.4+ reactive API）

- 注册：`addLink` / `addComponent`（11.1+）、`addFunction`（11.6+）；暴露：`exposeComponent`（id 必须以插件 id 为前缀，如 `local-ecs-app/xxx/v1`）。
- 消费：`usePluginLinks` / `usePluginComponents` / `usePluginFunctions`（hook，带 `isLoading`）。
- `addLink` 常用参数：`targets`、`title`、`description`、`path` **或** `onClick`（二选一）、`group`（替代已弃用的 `category`）、`icon`、`configure()`（按 context 动态隐藏/改写）。
- `onClick` + `openModal`（helpers）是面板菜单弹窗的官方示例模式——本项目用法一致。
- 可用扩展点：DashboardPanelMenu、CommandPalette、UserProfileTab、ExploreToolbarAction、Alerting 系列等（详见 reference/extension-points）。

## 6. App 前端最佳实践

- 多页面必须指定 root page（`defaultNav: true`）✅。
- `preload: true` 的 app 建议 code split（单资源 >250kb webpack 会警告）；本项目自有代码量小，暂不需要。
- **root page 用 React.lazy 时不要自己包 Suspense**（Grafana 会包）；`addConfigPage` 的组件需要自己包。
- 样式：只用 `@grafana/ui` 组件 + `useStyles2`/`useTheme2` + `@emotion/css`，禁止硬编码颜色/间距，禁止全局样式。
- 避免向客户端 ship `console.log`。

## 7. 签名 / 打包 / 发布

- 开发阶段无需签名：`GF_DEFAULT_APP_MODE=development` + `GF_PLUGINS_ALLOW_LOADING_UNSIGNED_PLUGINS=<id>` 是官方认可的 dev 姿态（本项目 docker-compose 现状）。
- 生产默认拒绝未签名插件。私有签名：`npm run sign -- --rootUrls <grafana地址>`，需 `GRAFANA_ACCESS_POLICY_TOKEN`，**token 云账号必须匹配 plugin id 第一段**——`local-ecs-app` 第一段是 `local`，若将来要私有签名需把 id 改成 `edward-ecs-app` 形式（大动作：牵动 provisioning、后端、导航 URL）。
- 打包：dist 重命名为插件 id 再 zip；二进制 0755；CHANGELOG.md 必备。
- 发布审查会跑 plugin-validator（github.com/grafana/plugin-validator），CI 可集成。

## 8. RBAC（11.6+，本项目已实现并验证）

官方三件套 + 后端 action 校验，2026-09 落地：

- `plugin.json`：`roles[]` 三个自定义 action（read/reveal/write，grants 矩阵见 AGENTS.md）+ `iam` 段（`users.permissions:read`, scope `users:*`）+ includes 用 `action` 替代 `role` 做页面门禁。
- 前端：`hasPermission()`（`@grafana/runtime`）门禁 UI（小眼睛/保存/更换/测试按钮）。
- 后端：`github.com/grafana/authlib/authz` EnforcementClient + `requireAction` 中间件，读 `X-Grafana-Id` 头校验 action。
- 给特定用户赋自定义角色需 Enterprise/Cloud；`grants` 自动授予内置角色在 OSS 可用（本项目依赖的就是 grants）。

实战踩坑记录（排错先看）：

1. **service account 配不出来**：光开 `externalServiceAccounts` toggle 不够，还要 `GF_AUTH_MANAGED_SERVICE_ACCOUNTS_ENABLED=true`（即 `[auth] managed_service_accounts_enabled`，默认 false）。官方 RBAC 指南没提这个开关；缺了 SDK 报 "PluginAppClientSecret not set in config"（v11.6 源码 `serviceregistration.go:25` 是 toggle AND 配置）。
2. **匿名请求也带 `X-Grafana-Id`**，但授权服务器拒绝查询匿名 token（"can only query server for users and service-accounts"）。对策：authz 出错时降级到 `PluginContext.User.Role` 的 org 角色映射（与 grants 保持一致，不放大权限），而非一律 403。
3. **匿名会话的前端页面守卫不可信**：权限 API 显示有 `ecs:read`，匿名访问 `/a/<id>` 仍被重定向回首页；真实登录用户正常。**权限结论必须以真实登录用户为准**。
4. **页面 URL 按 include 的 action 匹配**：`?tab=config` 匹配到配置 include（action=write），Viewer 直达被挡是预期语义，不是 bug。
5. 11.6 的 RBAC 角色在新 authz 存储：legacy `role` 表为空、`/api/access-control/roles` 404；验证有效权限用 `GET /api/access-control/user/permissions`（返回 action 列表）。
6. 插件角色在插件启动注册时经 `DeclarePluginRoles` 登记，改 `roles[]` 重启即生效。
7. **authlib 版本配对约束**：`github.com/grafana/authlib` 主包与 `authlib/types` 子包必须同日期 pseudo-version 配对（当前均 20260814）；`go get -u` 一把就能拉散，症状是 `types.GetUserPermissionsResponse undefined` 编译错误。修复：两个包一起 `go get ...@latest`。

### 常量单源生成（本项目约定）

RBAC action 字符串天然要出现在 plugin.json（声明）、Go（执行）、TS（展示）三处。本项目以 `src/plugin.json` 为唯一源头，`scripts/gen-permissions.js` 编译期生成 Go/TS 常量与回退映射，`zz_generated_test.go` 用独立实现的推导逻辑 + 硬编码语义锚点守护（改源头不重新生成 → go test 红）。新增 action 的显式摩擦点：生成器 `SEMANTIC` 表 + 守护测试的 suffix switch，两处都要登记。

## 9. 11.6 → 12 迁移检查点

- 已移除（用了会报错）：`configureExtensionLink/Component`、`get/usePluginExtensions`、`get/usePluginLinkExtensions`、`get/usePluginComponentExtensions`、类型 `PluginExtensionLinkConfig/ComponentConfig`。**本项目全部用的是新 API，无障碍。**
- `Select`/`MultiSelect` 弃用 → `Combobox`/`MultiCombobox`。本项目未用 Select，无障碍。
- 升级后必做回归：面板菜单扩展（双登记 + title 长度）+ 配置页保存/测试。

## 10. 差距清单（按处理优先级）

1. ⚠️ 扩展 title 改为 ≥10 字符（"查看 ECS 资产信息"），消除升级隐患。
2. 补 `CHANGELOG.md`（官方 Required，成本最低）。
3. ~~资源端点加角色保护~~ ✅ 2026-09 已完成（见 §8）。
4. 若走出本机：决定私有签名 + 是否改 plugin id（见 §7）。
5. ~~E2E 固化~~ 部分完成：`~/.zcode/tools/pw-browser/` 已有 rbac-regression / rbac-panel-menu / grafana-menu-check 回归脚本，可按需扩展成正式套件。
6. 可选锦上添花：Magefile 跨平台构建、LICENSE、screenshots、`state` 字段、React.lazy 分页。
7. ~~Resolver 缓存改 stale-while-revalidate~~ ✅ 2026-09-21 已实施（resolver.go）：三岔逻辑（新鲜纯内存 / 过期回旧值+单飞后台刷新 / 超 30 分钟硬上限退化为同步刷新保证错误可见）；后台刷新用 `context.Background`（不能用请求 ctx，请求返回即取消）；刷新失败保留旧快照、清单飞标记、下次请求重试；fetch 函数可注入（resolver_test.go 覆盖冷阻塞/新鲜命中/过期回旧/10 并发单飞/硬上限/失败重试六条路径）。实测：冷 ~3.8s（含建连），热 3.7ms。已否决项见前文（落盘缓存/心跳轮询/地域长缓存/跳地域）。

## 11. 项目验证策略（持续更新）

- Go：`go vet ./... && go test ./...`
- 前端：`npx tsc --noEmit && npm run build`
- **声明式契约（plugin.json、扩展注册）改动后：必须无头浏览器断言**（headless-shell 容器 CDP :9222 + playwright-core，见 ~/.agents/skills/playwright-browser/）。容器启动：`docker run -d --name headless-chrome --restart unless-stopped -p 127.0.0.1:9222:9222 chromedp/headless-shell --no-sandbox`；容器内访问宿主机 Grafana 用 `http://172.17.0.1:3000`。
- **RBAC/权限改动回归**：起 viewer 对照实例（`docker-compose.viewer.yaml`，3001 匿名 Viewer + admin 登录），用 Admin API 建真实 Viewer 用户下结论；curl 对照 `/ecs/ak`（masked 无 full）与 `/ecs/test`（403）。
- grafana.db 在 `grafana-data` 命名卷里，`--force-recreate` 不丢库（`down -v` 才删）——重建后无需重录 AK/数据源。
