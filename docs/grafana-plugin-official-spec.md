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

## 8. RBAC（11.6+）

- OSS 可用：`plugin.json roles[]` 定义 + `grants` 自动授予；前端 `hasPermission()`（`@grafana/runtime`）；includes 用 `action` 控制页面可见性。
- **后端资源端点 action 校验**需开 `externalServiceAccounts` feature toggle + authlib/authz client（仅支持单 org）；给用户赋自定义角色需 Enterprise/Cloud。
- 现状：本项目资源端点只要求登录，无角色检查。单机自用可接受；多用户需加固（轻量做法：handler 里查调用者 org role）。

## 9. 11.6 → 12 迁移检查点

- 已移除（用了会报错）：`configureExtensionLink/Component`、`get/usePluginExtensions`、`get/usePluginLinkExtensions`、`get/usePluginComponentExtensions`、类型 `PluginExtensionLinkConfig/ComponentConfig`。**本项目全部用的是新 API，无障碍。**
- `Select`/`MultiSelect` 弃用 → `Combobox`/`MultiCombobox`。本项目未用 Select，无障碍。
- 升级后必做回归：面板菜单扩展（双登记 + title 长度）+ 配置页保存/测试。

## 10. 差距清单（按处理优先级）

1. ⚠️ 扩展 title 改为 ≥10 字符（"查看 ECS 资产信息"），消除升级隐患。
2. 补 `CHANGELOG.md`（官方 Required，成本最低）。
3. 多用户场景：资源端点加角色保护（见 §8）。
4. 若走出本机：决定私有签名 + 是否改 plugin id（见 §7）。
5. 可选：把"面板菜单出现 ECS 入口"固化成 Playwright 断言脚本（官方 @grafana/plugin-e2e 同思路）。
6. 可选锦上添花：Magefile 跨平台构建、LICENSE、screenshots、`state` 字段、React.lazy 分页。

## 11. 项目验证策略（本次会话确立）

- Go：`go vet ./... && go test ./...`
- 前端：`npx tsc --noEmit && npm run build`
- **声明式契约（plugin.json、扩展注册）改动后：必须无头浏览器断言**（headless-shell 容器 CDP :9222 + playwright-core，见 ~/.agents/skills/playwright-browser/）。容器启动：`docker run -d --name headless-chrome --restart unless-stopped -p 127.0.0.1:9222:9222 chromedp/headless-shell --no-sandbox`；容器内访问宿主机 Grafana 用 `http://172.17.0.1:3000`。
