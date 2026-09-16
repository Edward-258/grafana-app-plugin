import { getBackendSrv } from '@grafana/runtime';
import pluginJson from './plugin.json';
import type { AppSettings } from './ConfigPage';

export type PromIdentity = {
  instance: string;
  nodename?: string;
  ip?: string;
};

type PromInstant = {
  status?: string;
  data?: {
    result?: Array<{
      metric?: Record<string, string>;
    }>;
  };
  error?: string;
};

const IP_LABELS = ['ip', 'private_ip', 'internal_ip', 'privateip'];

function escapeLabel(value: string): string {
  return value.replace(/\\/g, '\\\\').replace(/"/g, '\\"');
}

function pickIP(metric: Record<string, string> | undefined): string | undefined {
  if (!metric) {
    return undefined;
  }
  for (const key of IP_LABELS) {
    const v = metric[key];
    if (v) {
      return v;
    }
  }
  return undefined;
}

export async function loadSettings(): Promise<AppSettings> {
  const res = await getBackendSrv().get<{ jsonData?: AppSettings }>(`/api/plugins/${pluginJson.id}/settings`);
  return res.jsonData || {};
}

export async function queryPrometheus(uid: string, expr: string): Promise<PromInstant> {
  return getBackendSrv().get<PromInstant>(`/api/datasources/proxy/uid/${uid}/api/v1/query`, {
    query: expr,
  });
}

export async function identitiesFromPrometheus(uid: string, instanceLabel = 'instance'): Promise<PromIdentity[]> {
  const label = instanceLabel || 'instance';
  const uname = await queryPrometheus(uid, `node_uname_info`);
  const rows = uname.data?.result?.length ? uname.data.result : (await queryPrometheus(uid, `up`)).data?.result || [];
  const seen = new Set<string>();
  const out: PromIdentity[] = [];
  for (const row of rows) {
    const instance = row.metric?.[label] || row.metric?.instance || '';
    const nodename = row.metric?.nodename || '';
    const key = `${instance}|${nodename}`;
    if (!instance && !nodename) {
      continue;
    }
    if (seen.has(key)) {
      continue;
    }
    seen.add(key);
    const ident: PromIdentity = { instance, nodename };
    const ip = pickIP(row.metric);
    if (ip) {
      ident.ip = ip;
    }
    out.push(ident);
  }
  return out;
}

export async function identityForDashboard(
  uid: string,
  dashboardValue: string,
  instanceLabel = 'instance'
): Promise<PromIdentity> {
  const label = instanceLabel || 'instance';
  const escaped = escapeLabel(dashboardValue);
  const exprs = [`node_uname_info{${label}="${escaped}"}`, `up{${label}="${escaped}"}`];
  for (const expr of exprs) {
    const res = await queryPrometheus(uid, expr);
    const metric = res.data?.result?.[0]?.metric;
    if (!metric) {
      continue;
    }
    const ident: PromIdentity = {
      instance: metric[label] || metric.instance || dashboardValue,
      nodename: metric.nodename,
    };
    const ip = pickIP(metric);
    if (ip) {
      ident.ip = ip;
    }
    return ident;
  }
  return { instance: dashboardValue };
}
