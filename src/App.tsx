import React, { useEffect, useMemo, useRef, useState } from 'react';
import { css } from '@emotion/css';
import { GrafanaTheme2 } from '@grafana/data';
import { PluginPage, getBackendSrv, locationService } from '@grafana/runtime';
import { Alert, Button, FilterInput, LoadingPlaceholder, useStyles2 } from '@grafana/ui';
import pluginJson from './plugin.json';
import ConfigPage from './ConfigPage';
import { Billing, EcsAsset, EcsFields, fmtMoney, fmtTime } from './EcsInfo';
import { identitiesFromPrometheus, loadSettings } from './prom';

type ListResponse = {
  instances?: EcsAsset[];
  // 账户概览（BSS：余额/代金券/当月账单聚合）；仅 ecs:reveal 会话下发，整体软失败时缺省
  billing?: Billing;
  error?: string;
};

type AppProps = {
  query?: Record<string, unknown>;
};

function tabFromQuery(query?: Record<string, unknown>): 'config' | 'assets' {
  const raw = query?.tab;
  const value = Array.isArray(raw) ? raw[0] : raw;
  return value === 'assets' ? 'assets' : 'config';
}

export default function App({ query }: AppProps) {
  const styles = useStyles2(getStyles);
  const tab = tabFromQuery(query);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [instances, setInstances] = useState<EcsAsset[]>([]);
  const [billing, setBilling] = useState<Billing | null>(null);
  const [filter, setFilter] = useState('');
  const [selected, setSelected] = useState<EcsAsset | null>(null);
  // 手动刷新计数器：+1 让下面的 effect 重跑一次查询链路；forceRef 标记"本次
  // 是否绕过缓存"——仅点击刷新键置位，消费即复位，进页面/切 tab 仍走缓存。
  const [reloadSeq, setReloadSeq] = useState(0);
  const forceRef = useRef(false);

  useEffect(() => {
    if (tab !== 'assets') {
      return;
    }
    const force = forceRef.current;
    forceRef.current = false;
    setLoading(true);
    setError(null);
    (async () => {
      try {
        const settings = await loadSettings();
        if (!settings.prometheusUid) {
          throw new Error('请先在「配置」页选择 Prometheus 数据源并保存');
        }
        const identities = await identitiesFromPrometheus(settings.prometheusUid, settings.instanceLabel);
        if (identities.length === 0) {
          setInstances([]);
          return;
        }
        const res = await getBackendSrv().post<ListResponse>(`/api/plugins/${pluginJson.id}/resources/ecs/enrich`, {
          identities,
          refresh: force,
        });
        setInstances(res.instances || []);
        setBilling(res.billing ?? null);
      } catch (e) {
        setError(e instanceof Error ? e.message : '加载失败');
      } finally {
        setLoading(false);
      }
    })();
  }, [tab, reloadSeq]);

  const rows = useMemo(() => {
    const q = filter.trim().toLowerCase();
    if (!q) {
      return instances;
    }
    return instances.filter((i) =>
      [
        i.instanceId,
        i.instanceName,
        i.hostName,
        i.instanceType,
        i.regionId,
        i.monitorName,
        i.creationTime,
        i.expiredTime,
        i.leaseStart,
      ]
        .join(' ')
        .toLowerCase()
        .includes(q)
    );
  }, [instances, filter]);

  return (
    <PluginPage
      pageNav={{
        text: 'ECS 资产',
        url: `/a/${pluginJson.id}`,
        children: [
          { text: '配置', url: `/a/${pluginJson.id}?tab=config`, active: tab === 'config', icon: 'cog' },
          { text: '资产列表', url: `/a/${pluginJson.id}?tab=assets`, active: tab === 'assets', icon: 'cloud' },
        ],
      }}
    >
      {tab === 'config' && <ConfigPage />}

      {tab === 'assets' && (
        <>
          <Alert title="接入 Grafana 告警" severity="info">
            在 Alerting 里用数据源「ECS 资产（告警）」创建规则即可设阈值：包年包月实例用{' '}
            <code>daysToExpire &lt; 7</code>（到期提醒）；按量付费实例看余额池， <code>availableAmount &lt; 100</code>
            （余额不足）或 <code>billTotal &gt; 500</code>（当月按量实付）。 快照缓存 5 分钟，评估间隔建议 ≥ 5m。
            <Button
              size="sm"
              variant="secondary"
              icon="bell"
              className={styles.alertBtn}
              onClick={() => locationService.push('/alerting/list')}
            >
              创建告警规则
            </Button>
          </Alert>
          {billing && (
            <div className={styles.overview}>
              <span>
                账户余额 <b>{fmtMoney(billing.available, billing.currency)}</b>
              </span>
              {billing.coupon ? (
                <span>
                  代金券 <b>{fmtMoney(billing.coupon, billing.currency)}</b>
                </span>
              ) : null}
              {billing.billTotal ? (
                <span>
                  {billing.billingCycle} 消费（实付） <b>{fmtMoney(billing.billTotal, billing.currency)}</b>
                </span>
              ) : null}
              {billing.billItems && billing.billItems.length > 0 && (
                <span className={styles.overviewItems}>
                  {billing.billItems
                    .slice(0, 4)
                    .map((it) => `${it.product} ${fmtMoney(it.amount, billing.currency)}`)
                    .join(' · ')}
                  {billing.billItems.length > 4 ? ` 等 ${billing.billItems.length} 项` : ''}
                </span>
              )}
            </div>
          )}
          <div className={styles.toolbar}>
            <FilterInput placeholder="搜索 ECS ID / 名称 / 规格 / 地域" value={filter} onChange={setFilter} />
            <Button
              variant="secondary"
              icon="sync"
              aria-label="刷新资产列表"
              tooltip="强制实时查询：绕过缓存直接请求阿里云全地域"
              disabled={loading}
              className={loading ? styles.spin : undefined}
              onClick={() => {
                forceRef.current = true;
                setReloadSeq((s) => s + 1);
              }}
            />
          </div>

          {loading && <LoadingPlaceholder text="正在从 Prometheus 对齐 ECS..." />}
          {error && (
            <Alert title="无法读取资产" severity="error">
              {error}{' '}
              <a className={styles.linkish} href={`/a/${pluginJson.id}?tab=config`}>
                去配置
              </a>
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
                  <th>地域</th>
                  <th>创建时间</th>
                  <th>租赁开始</th>
                  <th>到期时间</th>
                  <th>vCPU</th>
                  <th>内存</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((row, idx) => (
                  <tr key={row.instanceId || row.monitorName || String(idx)} onClick={() => setSelected(row)}>
                    <td>{row.monitorName || '—'}</td>
                    <td>
                      {row.matched === false || !row.instanceId ? '未匹配' : row.instanceId}
                      {row.matched === false && row.note && <div className={styles.note}>{row.note}</div>}
                    </td>
                    <td>{row.instanceName || row.hostName || '—'}</td>
                    <td>{row.instanceType || '—'}</td>
                    <td>{row.regionId || '—'}</td>
                    <td>{fmtTime(row.creationTime)}</td>
                    <td>{row.leaseStart ? fmtTime(row.leaseStart) : <span className={styles.note}>—</span>}</td>
                    <td>
                      {row.chargeType === 'PostPaid'
                        ? billing && billing.available > 0
                          ? `按量付费（余额 ${fmtMoney(billing.available, billing.currency)}）`
                          : '按量付费'
                        : fmtTime(row.expiredTime)}
                    </td>
                    <td>{row.cpu || '—'}</td>
                    <td>{row.memoryGiB ? `${row.memoryGiB} GiB` : '—'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}

          {selected?.instanceId && (
            <div className={styles.detail}>
              <EcsFields instance={selected} billing={billing} />
            </div>
          )}
        </>
      )}
    </PluginPage>
  );
}

const getStyles = (theme: GrafanaTheme2) => ({
  overview: css({
    display: 'flex',
    flexWrap: 'wrap',
    gap: theme.spacing(2),
    alignItems: 'baseline',
    marginBottom: theme.spacing(2),
    padding: theme.spacing(1, 2),
    background: theme.colors.background.secondary,
    borderRadius: theme.shape.radius.default,
    fontSize: theme.typography.bodySmall.fontSize,
    color: theme.colors.text.secondary,
    b: { color: theme.colors.text.primary, fontFamily: theme.typography.fontFamilyMonospace },
  }),
  overviewItems: css({
    color: theme.colors.text.secondary,
    basis: '100%',
    fontSize: theme.typography.bodySmall.fontSize,
  }),
  toolbar: css({
    display: 'flex',
    gap: theme.spacing(2),
    marginBottom: theme.spacing(2),
    maxWidth: 720,
  }),
  // 加载中让刷新按钮的图标转起来（disabled 只是变灰，状态不够显性）
  spin: css({
    svg: { animation: 'ecs-refresh-spin 1s linear infinite' },
    '@keyframes ecs-refresh-spin': {
      from: { transform: 'rotate(0deg)' },
      to: { transform: 'rotate(360deg)' },
    },
  }),
  linkish: css({
    border: 'none',
    background: 'none',
    color: theme.colors.text.link,
    cursor: 'pointer',
    padding: 0,
  }),
  alertBtn: css({
    marginLeft: theme.spacing(2),
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
  note: css({
    color: theme.colors.text.secondary,
    fontSize: theme.typography.bodySmall.fontSize,
    fontFamily: theme.typography.fontFamily,
  }),
});
