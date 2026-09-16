# ECS 资产 · Grafana App Plugin

在现有 Node Overview 上补两件事：**ECS ID** 和 **规格（几核几 G）**。Prometheus 继续管利用率，本插件只读阿里云 ECS。

插件 ID：`local-ecs-app`（未签名，开发环境需允许 unsigned）。

## 目录（刻意比官方模板少）

官方 `@grafana/create-plugin` 会带上 `.config/`、`.github/`、Jest、Playwright、ESLint、四页示例、Mage、CI。Grafana 真正加载插件只需要 `plugin.json` + `module.js` + 后端二进制。本仓库只留需求用得到的文件：

```
src/plugin.json          Grafana 元数据（必填）
src/module.tsx           前端入口：总览页 + 配置页 + 面板菜单
src/App.tsx              侧边栏「ECS 资产」总览表
src/ConfigPage.tsx       AccessKey / Region
src/EcsInfo.tsx          面板菜单弹窗：当前设备 ID + 规格
src/img/logo.svg
pkg/*.go                 Go 后端，调 DescribeInstances
webpack.config.js        最小 AMD 打包（无 .config）
docker-compose.yaml      本地 Grafana
provisioning/plugins/    自动启用 App
```

## 怎么用

1. 配置页填 Region、AccessKey ID、Secret（只要 `ecs:DescribeInstances`）。密钥进 Grafana `secureJsonData`，前端读不到明文。
2. 左侧 Apps → **ECS 资产**：列出当前地域全部实例的 ID / 规格 / 核数 / 内存。
3. 打开任意 Dashboard，点面板标题菜单 → **ECS 资产信息**：用当前 `instance` / `node` / `host` 等变量匹配那一台。

变量匹配顺序：InstanceId → 内网 IP → hostname → instanceName。`node_exporter:9100` 这种进程名对不上 ECS，scrape target 请用内网 IP 或真实主机名。

## 本地构建

```bash
export PATH="$HOME/.local/go/bin:$PATH"   # 若 Go 装在用户目录
# /mnt/e 上 npm install 很慢：在 Linux 盘装再软链
#   mkdir -p ~/ecs-app-nm && cp package.json ~/ecs-app-nm && (cd ~/ecs-app-nm && npm install)
#   ln -sfn ~/ecs-app-nm/node_modules ./node_modules
npm install
npm run build          # 前端 dist/module.js + 后端 dist/gpx_ecs_linux_amd64
docker.exe compose up  # WSL 里没有 docker 命令时用 Docker Desktop 的 docker.exe
```

打开 http://localhost:3000（匿名 Admin）。开发时改前端：`npm run dev`。改 `plugin.json` 后要重启 Grafana。
