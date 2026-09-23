import { DataSourceJsonData } from '@grafana/data';
import type { DataQuery } from '@grafana/schema';

/** 查询哪类帧：account=账户概览单行；assets=每台 ECS 一行。 */
export type EcsFrameKind = 'account' | 'assets';

export interface EcsQuery extends DataQuery {
  frame?: EcsFrameKind;
}

/** 凭证集中在 app 插件配置页管理，本数据源自身无配置项。 */
export interface EcsDataSourceOptions extends DataSourceJsonData {}
