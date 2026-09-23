import React from 'react';
import { QueryEditorProps, SelectableValue } from '@grafana/data';
import { Select } from '@grafana/ui';

import { DataSource } from './datasource';
import { EcsDataSourceOptions, EcsFrameKind, EcsQuery } from './types';

const frameOptions: Array<SelectableValue<EcsFrameKind>> = [
  {
    label: '资产到期（每台 ECS 一个序列，daysToExpire）',
    value: 'assets',
    description: 'daysToExpire=距到期天数；实例身份在标签里（instanceId 等）；按量付费为空值',
  },
  {
    label: '账户概览（余额 / 代金券 / 当月账单）',
    value: 'account',
    description: 'availableAmount / couponAmount / billTotal；仅 Editor 及以上角色可查询',
  },
];

export function QueryEditor({ query, onChange }: QueryEditorProps<DataSource, EcsQuery, EcsDataSourceOptions>) {
  return (
    <Select<EcsFrameKind>
      width={44}
      value={query.frame ?? 'assets'}
      options={frameOptions}
      onChange={(v) => onChange({ ...query, frame: v.value ?? 'assets' })}
    />
  );
}
