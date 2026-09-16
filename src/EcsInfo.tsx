import React, { useEffect, useState } from 'react';
import { css } from '@emotion/css';
import { GrafanaTheme2 } from '@grafana/data';
import { getBackendSrv, getTemplateSrv } from '@grafana/runtime';
import { Alert, Button, LoadingPlaceholder, useStyles2 } from '@grafana/ui';
import pluginJson from './plugin.json';

export type EcsInstance = {
  instanceId: string;
  instanceName: string;
  hostName: string;
  instanceType: string;
  cpu: number;
  memoryGiB: number;
  privateIps: string[];
  zoneId: string;
  regionId: string;
};

type ResolveResponse = {
  query: string;
  matched: boolean;
  matchedBy?: string;
  instance?: EcsInstance;
  error?: string;
};

const VAR_NAMES = ['instanceId', 'instance_id', 'ecs_id', 'instance', 'node', 'host', 'ip'];

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

export function EcsFields({ instance }: { instance: EcsInstance }) {
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
      <dt>名称</dt>
      <dd>{instance.instanceName || instance.hostName || '—'}</dd>
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
    getBackendSrv()
      .get<ResolveResponse>(`/api/plugins/${pluginJson.id}/resources/ecs/resolve`, { q: query })
      .then(setData)
      .catch((e: Error) => setError(e.message || '查询失败'))
      .finally(() => setLoading(false));
  }, [query]);

  if (loading) {
    return <LoadingPlaceholder text="正在匹配 ECS..." />;
  }

  return (
    <div>
      {!query && (
        <Alert title="没有设备变量" severity="info">
          当前 Dashboard 里找不到 instance / node / host 等变量。请用内网 IP 或主机名作为 scrape target。
        </Alert>
      )}
      {error && (
        <Alert title="后端错误" severity="error">
          {error}
        </Alert>
      )}
      {data && !data.matched && (
        <Alert title="未匹配到 ECS" severity="warning">
          查询值「{data.query}」对不上已缓存的实例。请确认插件配置了正确地域，且 target 是内网 IP 或主机名。
        </Alert>
      )}
      {data?.instance && (
        <>
          <EcsFields instance={data.instance} />
          {data.matchedBy && <p className={styles.muted}>匹配字段：{data.matchedBy}</p>}
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
