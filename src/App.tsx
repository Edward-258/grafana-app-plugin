import React, { useEffect, useMemo, useState } from 'react';
import { css } from '@emotion/css';
import { GrafanaTheme2 } from '@grafana/data';
import { PluginPage, getBackendSrv } from '@grafana/runtime';
import { Alert, FilterInput, LinkButton, LoadingPlaceholder, useStyles2 } from '@grafana/ui';
import pluginJson from './plugin.json';
import { EcsAsset, EcsFields } from './EcsInfo';
import { identitiesFromPrometheus, loadSettings } from './prom';

type ListResponse = {
  instances?: EcsAsset[];
  error?: string;
};

export default function App() {
  const styles = useStyles2(getStyles);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [instances, setInstances] = useState<EcsAsset[]>([]);
  const [filter, setFilter] = useState('');
  const [selected, setSelected] = useState<EcsAsset | null>(null);

  useEffect(() => {
    (async () => {
      try {
        const settings = await loadSettings();
        if (!settings.prometheusUid) {
          throw new Error('请先在配置页选择 Prometheus 数据源');
        }
        const identities = await identitiesFromPrometheus(settings.prometheusUid, settings.instanceLabel);
        if (identities.length === 0) {
          setInstances([]);
          return;
        }
        const res = await getBackendSrv().post<ListResponse>(`/api/plugins/${pluginJson.id}/resources/ecs/enrich`, {
          identities,
        });
        setInstances(res.instances || []);
      } catch (e) {
        setError(e instanceof Error ? e.message : '加载失败');
      } finally {
        setLoading(false);
      }
    })();
  }, []);

  const rows = useMemo(() => {
    const q = filter.trim().toLowerCase();
    if (!q) {
      return instances;
    }
    return instances.filter((i) =>
      [i.instanceId, i.instanceName, i.hostName, i.instanceType, i.monitorName].join(' ').toLowerCase().includes(q)
    );
  }, [instances, filter]);

  return (
    <PluginPage>
      <div className={styles.toolbar}>
        <FilterInput placeholder="搜索 ECS ID / 名称 / 规格" value={filter} onChange={setFilter} />
        <LinkButton variant="secondary" href={`/plugins/${pluginJson.id}`}>
          配置
        </LinkButton>
      </div>

      {loading && <LoadingPlaceholder text="正在从 Prometheus 对齐 ECS..." />}
      {error && (
        <Alert title="无法读取资产" severity="error">
          {error}
        </Alert>
      )}
      {!loading && !error && instances.length === 0 && (
        <Alert title="Prometheus 里没有可对齐的实例" severity="info">
          确认数据源有 `up` 或 `node_uname_info`，并在配置页选对 Prometheus。
        </Alert>
      )}

      {!loading && rows.length > 0 && (
        <table className={styles.table}>
          <thead>
            <tr>
              <th>监控标识</th>
              <th>ECS ID</th>
              <th>名称</th>
              <th>规格</th>
              <th>vCPU</th>
              <th>内存</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((row, idx) => (
              <tr key={row.instanceId || row.monitorName || String(idx)} onClick={() => setSelected(row)}>
                <td>{row.monitorName || '—'}</td>
                <td>{row.matched === false || !row.instanceId ? '未匹配' : row.instanceId}</td>
                <td>{row.instanceName || row.hostName || '—'}</td>
                <td>{row.instanceType || '—'}</td>
                <td>{row.cpu || '—'}</td>
                <td>{row.memoryGiB ? `${row.memoryGiB} GiB` : '—'}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {selected?.instanceId && (
        <div className={styles.detail}>
          <EcsFields instance={selected} />
        </div>
      )}
    </PluginPage>
  );
}

const getStyles = (theme: GrafanaTheme2) => ({
  toolbar: css({
    display: 'flex',
    gap: theme.spacing(2),
    marginBottom: theme.spacing(2),
    maxWidth: 720,
  }),
  table: css({
    width: '100%',
    borderCollapse: 'collapse',
    fontSize: theme.typography.bodySmall.fontSize,
    th: {
      textAlign: 'left',
      color: theme.colors.text.secondary,
      borderBottom: `1px solid ${theme.colors.border.weak}`,
      padding: theme.spacing(1),
    },
    td: {
      borderBottom: `1px solid ${theme.colors.border.weak}`,
      padding: theme.spacing(1),
      fontFamily: theme.typography.fontFamilyMonospace,
    },
    'tbody tr': { cursor: 'pointer' },
    'tbody tr:hover': { background: theme.colors.background.secondary },
  }),
  detail: css({
    marginTop: theme.spacing(2),
    padding: theme.spacing(2),
    border: `1px solid ${theme.colors.border.weak}`,
    borderRadius: theme.shape.radius.default,
    maxWidth: 480,
  }),
});
