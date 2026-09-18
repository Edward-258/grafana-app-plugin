# AGENTS.md — local-ecs-app 开发须知

Grafana App 插件 `local-ecs-app`：阿里云 ECS 资产（ID/规格/地域）对齐已有 Prometheus dashboard。

**详细规范笔记：[docs/grafana-plugin-official-spec.md](docs/grafana-plugin-official-spec.md)**（官方文档提炼 + 本项目对照，改动前先读）。

## 红线（违反即事故）

1. **UI 扩展双登记**：代码 `addLink()` 注册的扩展必须同时在 `src/plugin.json` 的 `extensions.addedLinks[]` 声明，缺一会静默消失（无 server 日志，只有浏览器 console 报错）。改动 plugin.json 或扩展注册后必须浏览器验证菜单仍存在。
2. **ECS IP 永不进浏览器**：后端 `Public()` 裁剪 + `TestPublicAssetOmitsIPs` 守护，任何 model/接口改动不得让 IP 到达前端。
3. **AK 严格对应**：资产只能来自该 AK 的全地域枚举且唯一命中；任一地域查询失败则整体失败，禁止部分结果。
4. **secret 只进 `secureJsonData`**：`jsonData` 禁存敏感值；保存时只发被修改的键（空字符串也会覆盖旧值）。

## 已知隐患

- `extensions.addedLinks[].title` 官方要求 ≥10 字符，当前 "ECS 资产信息" 8 字符；升级 Grafana 后若扩展失联，先查这里。

## 构建与验证

```bash
npm run build                          # 前端(webpack) + 后端(gox linux/amd64 → dist/gpx_ecs_linux_amd64)
go vet ./... && go test ./...          # 后端
npx tsc --noEmit                       # 前端类型
docker compose up -d                   # Grafana 11.6 @ :3000（dev 模式，允许未签名）
```

改 `plugin.json` 后必须重建 + 重启 Grafana。UI 行为验证用无头浏览器（CDP :9222，容器内访问宿主机用 `http://172.17.0.1:3000`），skill 见 `~/.agents/skills/playwright-browser/`。

## 架构分层

- `src/` 前端（module.tsx 注册 root page / config page / 面板菜单扩展；import 分层有约定，勿破坏）
- `pkg/aliyun/` 阿里云 OpenAPI（RPC 签名、全地域枚举、唯一命中匹配）
- `pkg/app/` 插件后端（ServeMux + httpadapter，资源端点 `/ecs/enrich|resolve|test`、`/health`）
- `provisioning/` Grafana 部署配置
