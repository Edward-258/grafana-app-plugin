# AGENTS.md — local-ecs-app 开发须知

Grafana App 插件 `local-ecs-app`：阿里云 ECS 资产（ID/规格/地域）对齐已有 Prometheus dashboard。

**详细规范笔记：[docs/grafana-plugin-official-spec.md](docs/grafana-plugin-official-spec.md)**（官方文档提炼 + 本项目对照，改动前先读）。

## 红线（违反即事故）

1. **UI 扩展双登记**：代码 `addLink()` 注册的扩展必须同时在 `src/plugin.json` 的 `extensions.addedLinks[]` 声明，缺一会静默消失（无 server 日志，只有浏览器 console 报错）。改动 plugin.json 或扩展注册后必须浏览器验证菜单仍存在。
2. **ECS IP 永不进浏览器**：后端 `Public()` 裁剪 + `TestPublicAssetOmitsIPs` 守护，任何 model/接口改动不得让 IP 到达前端。
3. **AK 严格对应**：资产只能来自该 AK 的全地域枚举且唯一命中；任一地域查询失败则整体失败，禁止部分结果。
4. **凭证只进 `secureJsonData`**：AK ID 与 Secret 都属加密存储（`jsonData` 禁存敏感值）；保存时只发被修改的键（空字符串也会覆盖旧值）。Viewer 只能见脱敏 AK ID（前3+后3），完整值仅 `ecs:reveal` 持有者可得（`/ecs/ak` 端点按角色下发）。

## RBAC 权限矩阵（plugin.json roles[] + 后端 requireAction）

| action                     | grants              | 能做什么                                |
| -------------------------- | ------------------- | --------------------------------------- |
| `local-ecs-app.ecs:read`   | Viewer/Editor/Admin | 资产对齐端点（enrich/resolve）、脱敏 AK |
| `local-ecs-app.ecs:reveal` | Editor/Admin        | 完整 AK（小眼睛）、连通测试             |
| `local-ecs-app.ecs:write`  | 仅 Admin            | 改写 AK ID/Secret、配置页保存           |

**常量单源生成（SSOT）**：`src/plugin.json` 是唯一源头，`npm run build` 前置运行 `scripts/gen-permissions.js` 生成 `pkg/app/handler/zz_generated.go`（PluginID、三个 action 常量、roleActions 回退映射）和 `src/permissions.gen.ts`。**任何地方不得手写权限字符串**；新增 action 必须先在生成器 `SEMANTIC` 和守护测试 `zz_generated_test.go` 同时登记（故意制造摩擦）。改 plugin.json 后忘重新生成会被 `go test` 抓住（守护测试独立重推导比对 + 硬编码语义锚点）。插件 ID 的源头是 `package.json` 的 `name`（webpack 与生成器同源；Go 侧引用 `handler.PluginID`）。

- 后端 `pkg/app/handler/auth.go`：有 `X-Grafana-Id` 走官方 authz client；authz 出错或匿名时降级为 `PluginContext.User.Role` 的 org 角色映射（与 grants 一致，不放大权限）；无用户信息一律 403。
- 前端用 `hasPermission()`（`@grafana/runtime`）门禁；`?tab=config` 路径会被 Grafana 按 include 的 action 门禁匹配到 write，Viewer 直达被挡属预期。
- **权限结论必须以真实登录用户为准**：匿名会话会被前端导航守卫重定向回首页，即使权限 API 显示有权限（已实测踩坑）。
- 生效前提（compose 已配）：`GF_FEATURE_TOGGLES_ENABLE=externalServiceAccounts` **且** `GF_AUTH_MANAGED_SERVICE_ACCOUNTS_ENABLED=true`（后者官方文档没写，缺了 Grafana 不给插件配 service account）。

## 已知隐患

- `extensions.addedLinks[].title` 官方要求 ≥10 字符，当前 "ECS 资产信息" 8 字符；升级 Grafana 后若扩展失联，先查这里。

## 构建与验证

```bash
npm run build                          # 生成 RBAC 常量 + 前端(webpack) + 后端(gox linux/amd64 → dist/gpx_ecs_linux_amd64)
npm run lint                           # ESLint（@grafana/eslint-config）+ Prettier；lint:fix 自动修（scripts/、webpack.config.js 不参与）
golangci-lint run ./...                # 后端 lint（v2.13.2，配置 .golangci.yml；旧版二进制无法分析 go 1.26）
go vet ./... && go test ./...          # 后端（含 zz_generated 守护测试）
npx tsc --noEmit                       # 前端类型（含 tests/）
npm run e2e                            # Playwright e2e（tests/）：面板菜单扩展红线 + RBAC 拦截 + 页面导航
docker compose up -d                   # Grafana 11.6 @ :3000（dev 模式，允许未签名）
```

- **CI（GitHub Actions，推到 origin 后自动跑）**：`.github/workflows/ci.yml` 双 job——build（prettier/eslint/tsc/golangci/go test/build）与 e2e（起主实例 + viewer 对照实例，浏览器本地启动跑全套 Playwright）；`is-compatible.yml` 用官方 levitate 查前端 API 弃用（PR 时跑）；`dependabot.yml` 周更依赖（gomod 只放行 plugin-sdk）。本地 e2e 是「CDP 容器浏览器 + 172.17.0.1」拓扑、CI 是「本地浏览器 + localhost」拓扑，靠 `PW_CDP_ENDPOINT`/`GRAFANA_*_URL` 环境变量切换（见 tests/fixtures.ts 头注），代码零改动。

- `grafana-data` 命名卷持久化 grafana.db：`--force-recreate`/`restart` 不丢库，`docker compose down -v` 才删。
- **加密密钥走 `.env`**（不入库，模板 `.env.example`）：两个 compose 都读 `GF_SECURITY_SECRET_KEY` 加密 grafana.db 里的 secureJsonData；留空则 Grafana 用公开默认密钥（CI 临时容器即此情形，无真实 AK 无风险）。**换密钥后旧密文作废，需在配置页重录 AK/SK**。
- 改 `plugin.json` 后必须重建 + 重启 Grafana。**UI 行为验证首选 `npm run e2e`**（红线回归已固化成正式套件，跑 `panelMenu.spec.ts` 即浏览器断言菜单）；探索性调试仍可用 skill（CDP :9222，容器内访问宿主机用 `http://172.17.0.1:3000`）与 `~/.zcode/tools/pw-browser/` 下的临时脚本。e2e 拓扑注意：浏览器跑在 headless-shell 容器里，Node 侧用 `localhost`、浏览器侧用 `172.17.0.1`（见 `tests/fixtures.ts` 头注；`PW_CDP_ENDPOINT`/`GRAFANA_URL`/`GRAFANA_BROWSER_URL` 可覆盖）。
- 权限回归对照实例：`docker compose -f docker-compose.yaml -f docker-compose.viewer.yaml up -d grafana-viewer`（3001 端口，匿名 Viewer；其库故意不持久，测完 down 掉归零）。e2e 的 viewer 项目（`rbac.spec.ts`）依赖它在跑，实例不在时自动 skip。

## 架构分层

- `src/` 前端（module.tsx 注册 root page / config page / 面板菜单扩展；import 分层有约定，勿破坏）
- `pkg/aliyun/` 阿里云 OpenAPI（RPC 签名、全地域枚举、唯一命中匹配；BSS 补充链路 `CreationTimes()`——`business.aliyuncs.com` 的 QueryAvailableInstances 取订单口径租赁开始时间 `leaseStart`（独立字段，ECS creationTime 不动），软失败置空）
- `pkg/app/` 插件后端（ServeMux + httpadapter；资源端点 `/ecs/enrich|resolve|test|ak`；探活走 SDK 的 CheckHealth 通道；`auth.go` RBAC 中间件；`guardrails.go` 请求守卫——body 1MB/413、identities 2000 条/400、阿里云响应 4MB 拒读，调整限额只动这一个文件）
- `provisioning/` Grafana 部署配置
