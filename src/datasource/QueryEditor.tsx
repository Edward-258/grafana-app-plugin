import React from 'react';
import { QueryEditorProps, SelectableValue } from '@grafana/data';
import { Select } from '@grafana/ui';

import { DataSource } from './datasource';
import { EcsDataSourceOptions, EcsFrameKind, EcsQuery } from './types';

const frameOptions: Array<SelectableValue<EcsFrameKind>> = [
  {
    label: '资产到期（包年包月实例，daysToExpire）',
    value: 'assets',
    description: '每台包年包月 ECS 一个序列，daysToExpire=距到期天数；实例身份在标签里（按量付费不在此帧）',
  },
  {
    label: '账户概览（按量付费余额池：余额 / 代金券 / 当月按量实付）',
    value: 'account',
    description:
      'availableAmount / couponAmount 为账户级；billTotal 仅统计按量付费实例当月实付。仅 Editor 及以上可查询',
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
