# ECS 资产 · Grafana App Plugin

在已有 Prometheus + Grafana CPU/内存看板旁补两件事：**ECS ID** 和 **规格（几核几 G）**。

Prometheus 继续管占用率，并告诉插件「这张 dashboard 对应哪台被监控机器」。插件拿到对齐结果后，在后端用 AccessKey 自动枚举**全部地域**的 ECS（一把 AK 可见多少地域就查多少），**前端不展示内网/公网 IP**。

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
        │   DescribeRegions → 逐地域 DescribeInstances（并发枚举，缓存 5 分钟）
        ▼
阿里云全地域实例（按 instanceId / IP / 主机名 / 实例名唯一命中）
        │
        ▼
浏览器只渲染 ECS ID / 规格 / 名称 / 所属地域
```

scrape target **不必**填 ECS 内网或公网 IP。对齐优先用主机名（`nodename` / `instance`）。

对齐是严格的：弱标识（IP / 主机名 / 实例名）必须在全地域实例中**唯一命中**，跨地域撞名一律记为「未匹配」并注明原因；任何一个地域查询失败就整体报错，绝不静默少列资产。

## 怎么用

1. 配置页选择 Grafana 里已有的 **Prometheus 数据源**，填只读 AccessKey（需要 `ecs:DescribeRegions`、`ecs:DescribeInstances`，`AliyunECSReadOnlyAccess` 已覆盖）。
2. 左侧 Apps → **ECS 资产**：列出 Prometheus 里出现过的机器，以及匹配到的 ECS ID / 规格 / 所属地域。
3. 打开现有利用率 Dashboard，面板菜单 → **ECS 资产信息**：用当前变量走 Prometheus 再查阿里云。

## Grafana 告警（Alerting）

插件捆绑一个后端数据源 **ECS 资产（告警）**（`local-ecs-app-ds`，provisioning 自动预置为 uid `ecs-ds`），把后端快照暴露成告警规则可查询的数据帧。规则在 **Grafana Alerting UI 里自己创建**（资产页顶部有引导卡与跳转按钮），阈值随意调节：

| 查询帧   | 字段                                                 | 示例                             |
| -------- | ---------------------------------------------------- | -------------------------------- |
| 资产到期 | `daysToExpire`（每台实例一个序列，实例身份在标签里） | `daysToExpire < 7` 到期提醒      |
| 账户概览 | `availableAmount` / `couponAmount` / `billTotal`     | `availableAmount < 100` 余额不足 |

- 快照缓存 5 分钟（SWR），规则评估间隔建议 ≥ 5m。
- 凭证不用重录：app 后端启动时用 service account 把配置页保存的 AK 自动同步到数据源（两端均加密存储）。
- 权限：`assets` 帧 `ecs:read`（Viewer 可读）；`account` 帧与插件 UI 同档，仅 Editor/Admin。注意告警实例的触发值对「能看告警的人」天然可见（财务数字进告警即具广播属性）。
- 通知复用 Grafana 已有 contact point：规则打 labels → 通知策略路由，无需插件侧配置。

## 本地构建

```bash
export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH"
npm install
npm run build
docker compose up
```

打开 http://localhost:3000。改 `plugin.json` 后重启 Grafana。
