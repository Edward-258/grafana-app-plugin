import React, { useEffect, useMemo, useState } from 'react';
import { css } from '@emotion/css';
import { GrafanaTheme2 } from '@grafana/data';
import { PluginPage, getBackendSrv } from '@grafana/runtime';
import { Alert, FilterInput, LinkButton, LoadingPlaceholder, useStyles2 } from '@grafana/ui';
import pluginJson from './plugin.json';
import { EcsFields, EcsInstance } from './EcsInfo';

type ListResponse = {
  instances?: EcsInstance[];
  error?: string;
};

export default function App() {
  const styles = useStyles2(getStyles);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [instances, setInstances] = useState<EcsInstance[]>([]);
  const [filter, setFilter] = useState('');
  const [selected, setSelected] = useState<EcsInstance | null>(null);

  useEffect(() => {
    getBackendSrv()
      .get<ListResponse>(`/api/plugins/${pluginJson.id}/resources/ecs/instances`)
      .then((res) => setInstances(res.instances || []))
      .catch((e: Error) => setError(e.message || '加载失败，请先在配置页填写 AccessKey'))
      .finally(() => setLoading(false));
  }, []);

  const rows = useMemo(() => {
    const q = filter.trim().toLowerCase();
    if (!q) {
      return instances;
    }
    return instances.filter((i) =>
      [i.instanceId, i.instanceName, i.hostName, i.instanceType, ...(i.privateIps || [])]
        .join(' ')
        .toLowerCase()
        .includes(q)
    );
  }, [instances, filter]);

  return (
    <PluginPage>
      <div className={styles.toolbar}>
        <FilterInput placeholder="搜索 ECS ID / 名称 / IP" value={filter} onChange={setFilter} />
        <LinkButton variant="secondary" href={`/plugins/${pluginJson.id}`}>
          配置 AccessKey
        </LinkButton>
      </div>

      {loading && <LoadingPlaceholder text="正在拉取 ECS 列表..." />}
      {error && (
        <Alert title="无法读取 ECS" severity="error">
          {error}
        </Alert>
      )}
      {!loading && !error && instances.length === 0 && (
        <Alert title="当前地域没有实例" severity="info">
          检查配置页的 Region 与 AccessKey 是否属于这个账号。
        </Alert>
      )}

      {!loading && rows.length > 0 && (
        <table className={styles.table}>
          <thead>
            <tr>
              <th>ECS ID</th>
              <th>名称</th>
              <th>规格</th>
              <th>vCPU</th>
              <th>内存</th>
              <th>内网 IP</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => (
              <tr key={row.instanceId} onClick={() => setSelected(row)}>
                <td>{row.instanceId}</td>
                <td>{row.instanceName || row.hostName}</td>
                <td>{row.instanceType}</td>
                <td>{row.cpu}</td>
                <td>{row.memoryGiB} GiB</td>
                <td>{(row.privateIps || []).join(', ')}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {selected && (
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
