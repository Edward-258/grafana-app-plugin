import React, { useEffect, useState } from 'react';
import { css } from '@emotion/css';
import { GrafanaTheme2 } from '@grafana/data';
import { getBackendSrv, getTemplateSrv } from '@grafana/runtime';
import { Alert, Button, LoadingPlaceholder, useStyles2 } from '@grafana/ui';
import pluginJson from './plugin.json';
import { identityForDashboard, loadSettings } from './prom';

export type EcsAsset = {
  instanceId: string;
  instanceName: string;
  hostName: string;
  instanceType: string;
  cpu: number;
  memoryGiB: number;
  zoneId?: string;
  regionId?: string;
  creationTime?: string;
  expiredTime?: string;
  chargeType?: string;
  monitorName?: string;
  matched?: boolean;
  note?: string;
};

// 阿里云 ECS 时间为分钟精度 UTC（yyyy-MM-ddTHH:mmZ，无秒），
// 补秒归一后转本地时区展示；解析失败原样返回便于排查。
export function fmtTime(iso?: string): string {
  if (!iso) {
    return '—';
  }
  const normalized = /T\d{2}:\d{2}Z$/.test(iso) ? iso.replace(/Z$/, ':00Z') : iso;
  const d = new Date(normalized);
  if (isNaN(d.getTime())) {
    return iso;
  }
  const p = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}

export function chargeLabel(t?: string): string {
  if (t === 'PrePaid') {
    return '包年包月';
  }
  if (t === 'PostPaid') {
    return '按量付费';
  }
  return t || '—';
}

type ResolveResponse = {
  matched: boolean;
  monitorName?: string;
  note?: string;
  instance?: EcsAsset;
  error?: string;
};

const VAR_NAMES = ['instance', 'node', 'host', 'nodename', 'instanceId', 'instance_id', 'ecs_id'];

export function currentDashboardQuery(): string {
  const srv = getTemplateSrv();
  const vars = srv.getVariables();
  for (const name of VAR_NAMES) {
    const found = vars.find((v) => v.name === name);
    if (!found) {
      continue;
    }
    const current = (found as { current?: { value?: unknown } }).current?.value;
    const raw = Array.isArray(current) ? current[0] : current;
    if (raw != null && raw !== '' && raw !== '$__all') {
      return String(raw);
    }
  }
  const params = new URLSearchParams(window.location.search);
  for (const name of VAR_NAMES) {
    const q = params.get(`var-${name}`);
    if (q) {
      return q;
    }
  }
  return '';
}

export function EcsFields({ instance }: { instance: EcsAsset }) {
  const styles = useStyles2(getStyles);
  return (
    <dl className={styles.fields}>
      <dt>ECS ID</dt>
      <dd>{instance.instanceId}</dd>
      <dt>规格</dt>
      <dd>
        {instance.instanceType || '—'}
        <span className={styles.muted}>
          {' '}
          / {instance.cpu} 核 / {instance.memoryGiB} GiB
        </span>
      </dd>
      <dt>地域</dt>
      <dd>
        {instance.regionId || '—'}
        {instance.zoneId ? <span className={styles.muted}> / {instance.zoneId}</span> : null}
      </dd>
      <dt>名称</dt>
      <dd>{instance.instanceName || instance.hostName || instance.monitorName || '—'}</dd>
      <dt>计费方式</dt>
      <dd>{chargeLabel(instance.chargeType)}</dd>
      <dt>创建时间</dt>
      <dd>{fmtTime(instance.creationTime)}</dd>
      <dt>到期时间</dt>
      <dd>
        {instance.chargeType === 'PostPaid' ? (
          <span className={styles.muted}>按量付费无固定到期</span>
        ) : (
          fmtTime(instance.expiredTime)
        )}
      </dd>
    </dl>
  );
}

export function EcsModalBody({ onDismiss }: { onDismiss?: () => void }) {
  const styles = useStyles2(getStyles);
  const [query] = useState(currentDashboardQuery);
  const [loading, setLoading] = useState(true);
  const [data, setData] = useState<ResolveResponse | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!query) {
      setLoading(false);
      return;
    }
    (async () => {
      try {
        const settings = await loadSettings();
        if (!settings.prometheusUid) {
          throw new Error('请先在插件配置页选择 Prometheus 数据源');
        }
        const ident = await identityForDashboard(settings.prometheusUid, query, settings.instanceLabel);
        const res = await getBackendSrv().post<ResolveResponse>(
          `/api/plugins/${pluginJson.id}/resources/ecs/resolve`,
          ident
        );
        setData(res);
      } catch (e) {
        setError(e instanceof Error ? e.message : '查询失败');
      } finally {
        setLoading(false);
      }
    })();
  }, [query]);

  if (loading) {
    return <LoadingPlaceholder text="正在通过 Prometheus 对齐 ECS..." />;
  }

  return (
    <div>
      {!query && (
        <Alert title="没有设备变量" severity="info">
          当前 Dashboard 里找不到 instance / node / host 等变量。插件用 Prometheus 的实例标识对齐 ECS，不需要把 IP 写进
          scrape target。
        </Alert>
      )}
      {error && (
        <Alert title="无法对齐" severity="error">
          {error}
        </Alert>
      )}
      {data?.error && (
        <Alert title="后端错误" severity="error">
          {data.error}
        </Alert>
      )}
      {data && !data.matched && !data.error && (
        <Alert title="未匹配到 ECS" severity="warning">
          Prometheus 已定位到当前 Dashboard 的监控实例，但阿里云侧没有唯一对应的主机。
          {data.note ? `（${data.note}）` : ''}请确认 ECS 主机名或实例名与 node_exporter 的 nodename / instance 一致。
        </Alert>
      )}
      {data?.instance && (
        <>
          <EcsFields instance={data.instance} />
          <p className={styles.muted}>已与当前 Dashboard 的 Prometheus 实例对齐</p>
        </>
      )}
      {onDismiss && (
        <div className={styles.actions}>
          <Button variant="secondary" onClick={onDismiss}>
            关闭
          </Button>
        </div>
      )}
    </div>
  );
}

const getStyles = (theme: GrafanaTheme2) => ({
  fields: css({
    display: 'grid',
    gridTemplateColumns: '96px 1fr',
    gap: theme.spacing(1, 2),
    margin: 0,
    dt: { color: theme.colors.text.secondary, fontWeight: 500 },
    dd: { margin: 0, fontFamily: theme.typography.fontFamilyMonospace },
  }),
  muted: css({
    color: theme.colors.text.secondary,
    fontSize: theme.typography.bodySmall.fontSize,
  }),
  actions: css({
    marginTop: theme.spacing(2),
    display: 'flex',
    justifyContent: 'flex-end',
  }),
});
