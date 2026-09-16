# ECS 资产 · Grafana App Plugin

在已有 Prometheus + Grafana CPU/内存看板旁补两件事：**ECS ID** 和 **规格（几核几 G）**。

Prometheus 继续管占用率，并告诉插件「这张 dashboard 对应哪台被监控机器」。插件拿到对齐结果后，在后端用 ECS 地址查阿里云，**前端不展示内网/公网 IP**。

插件 ID：`local-ecs-app`（未签名，开发环境需允许 unsigned）。

后端单向依赖：`pkg/main.go` → `app/handler` → `app/service` → `aliyun/client` → `aliyun/model` 与 `aliyun/rpc`。上层只 import 下一层。

## 怎么走数据

```
已有 CPU dashboard（$instance 等）
        │
        ▼
插件前端 → Grafana 数据源代理 → Prometheus（up / node_uname_info）
        │   只取 instance、nodename 等监控标识
        ▼
插件 Go 后端（IP 只留在这里）
        │   若标识里能解析出地址，或用主机名对上 ECS 后得到地址
        ▼
阿里云 DescribeInstances（按地址查）
        │
        ▼
浏览器只渲染 ECS ID / 规格 / 名称
```

scrape target **不必**填 ECS 内网或公网 IP。对齐优先用主机名（`nodename` / `instance`）。

## 怎么用

1. 配置页选择 Grafana 里已有的 **Prometheus 数据源**，填 Region 与只读 AccessKey（`ecs:DescribeInstances`）。
2. 左侧 Apps → **ECS 资产**：列出 Prometheus 里出现过的机器，以及匹配到的 ECS ID / 规格。
3. 打开现有利用率 Dashboard，面板菜单 → **ECS 资产信息**：用当前变量走 Prometheus 再查阿里云。

## 本地构建

```bash
export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH"
npm install
npm run build
docker compose up
```

打开 http://localhost:3000。改 `plugin.json` 后重启 Grafana。
