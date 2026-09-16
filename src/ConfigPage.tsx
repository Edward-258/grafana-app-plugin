import React, { ChangeEvent, useState } from 'react';
import { lastValueFrom } from 'rxjs';
import { css } from '@emotion/css';
import { AppPluginMeta, GrafanaTheme2, PluginConfigPageProps, PluginMeta } from '@grafana/data';
import { getBackendSrv } from '@grafana/runtime';
import { Alert, Button, Field, FieldSet, Input, SecretInput, useStyles2 } from '@grafana/ui';
import pluginJson from './plugin.json';

export type AppSettings = {
  region?: string;
  accessKeyId?: string;
};

type Props = PluginConfigPageProps<AppPluginMeta<AppSettings>>;

export default function ConfigPage({ plugin }: Props) {
  const styles = useStyles2(getStyles);
  const { enabled, pinned, jsonData, secureJsonFields } = plugin.meta;
  const [region, setRegion] = useState(jsonData?.region || 'cn-hangzhou');
  const [accessKeyId, setAccessKeyId] = useState(jsonData?.accessKeyId || '');
  const [accessKeySecret, setAccessKeySecret] = useState('');
  const [secretConfigured, setSecretConfigured] = useState(Boolean(secureJsonFields?.accessKeySecret));
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [message, setMessage] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const onSave = async () => {
    setSaving(true);
    setError(null);
    setMessage(null);
    try {
      await updatePlugin(plugin.meta.id, {
        enabled,
        pinned,
        jsonData: { region: region.trim(), accessKeyId: accessKeyId.trim() },
        secureJsonData: secretConfigured ? undefined : { accessKeySecret },
      });
      setMessage('已保存。Grafana 会重启插件后端进程。');
      window.location.reload();
    } catch (e) {
      setError(e instanceof Error ? e.message : '保存失败');
    } finally {
      setSaving(false);
    }
  };

  const onTest = async () => {
    setTesting(true);
    setError(null);
    setMessage(null);
    try {
      const res = await getBackendSrv().post<{ ok: boolean; count?: number; error?: string }>(
        `/api/plugins/${pluginJson.id}/resources/ecs/test`,
        {}
      );
      if (res.ok) {
        setMessage(`连通正常，当前地域可见 ${res.count ?? 0} 台 ECS。`);
      } else {
        setError(res.error || '测试失败');
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : '测试失败，请先保存配置');
    } finally {
      setTesting(false);
    }
  };

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        void onSave();
      }}
    >
      <FieldSet label="阿里云 ECS（只读）">
        <Field label="Region" description="例如 cn-hangzhou、cn-beijing">
          <Input width={60} value={region} onChange={(e: ChangeEvent<HTMLInputElement>) => setRegion(e.target.value)} />
        </Field>
        <Field label="AccessKey ID" className={styles.gap}>
          <Input
            width={60}
            value={accessKeyId}
            onChange={(e: ChangeEvent<HTMLInputElement>) => setAccessKeyId(e.target.value)}
          />
        </Field>
        <Field label="AccessKey Secret" description="保存后不会回显明文">
          <SecretInput
            width={60}
            value={accessKeySecret}
            isConfigured={secretConfigured}
            onChange={(e: ChangeEvent<HTMLInputElement>) => setAccessKeySecret(e.target.value)}
            onReset={() => {
              setAccessKeySecret('');
              setSecretConfigured(false);
            }}
          />
        </Field>
        {message && (
          <Alert title="成功" severity="success">
            {message}
          </Alert>
        )}
        {error && (
          <Alert title="失败" severity="error">
            {error}
          </Alert>
        )}
        <div className={styles.gap}>
          <Button type="submit" disabled={saving || !region || !accessKeyId || (!secretConfigured && !accessKeySecret)}>
            保存
          </Button>
          <Button type="button" variant="secondary" className={styles.btn} onClick={() => void onTest()} disabled={testing}>
            测试连接
          </Button>
        </div>
      </FieldSet>
    </form>
  );
}

const updatePlugin = async (pluginId: string, data: Partial<PluginMeta<AppSettings>>) => {
  const response = await getBackendSrv().fetch({
    url: `/api/plugins/${pluginId}/settings`,
    method: 'POST',
    data,
  });
  return lastValueFrom(response);
};

const getStyles = (theme: GrafanaTheme2) => ({
  gap: css({ marginTop: theme.spacing(2) }),
  btn: css({ marginLeft: theme.spacing(1) }),
});
