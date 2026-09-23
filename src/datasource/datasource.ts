import {
  CoreApp,
  DataQueryRequest,
  DataQueryResponse,
  DataSourceApi,
  DataSourceInstanceSettings,
  TestDataSourceResponse,
} from '@grafana/data';
import { getBackendSrv } from '@grafana/runtime';
import { lastValueFrom, Observable } from 'rxjs';
import { map } from 'rxjs/operators';

import { EcsDataSourceOptions, EcsQuery } from './types';

/**
 * 后端型数据源：查询体原样转发 /api/ds/query，由 gpx_ecs_ds 后端进程执行
 * （告警规则评估同样走这条链路）。帧数据的裁剪与角色门禁都在后端做；
 * 连通性测试打后端 CheckHealth（/health 端点）。
 */
export class DataSource extends DataSourceApi<EcsQuery, EcsDataSourceOptions> {
  private readonly dsSettings: DataSourceInstanceSettings<EcsDataSourceOptions>;

  constructor(instanceSettings: DataSourceInstanceSettings<EcsDataSourceOptions>) {
    super(instanceSettings);
    this.dsSettings = instanceSettings;
  }

  getDefaultQuery(_: CoreApp): Partial<EcsQuery> {
    return { frame: 'assets' };
  }

  query(options: DataQueryRequest<EcsQuery>): Observable<DataQueryResponse> {
    const range = options.range;
    return getBackendSrv()
      .fetch<DataQueryResponse>({
        url: '/api/ds/query',
        method: 'POST',
        data: {
          from: String(range?.from.valueOf() ?? 0),
          to: String(range?.to.valueOf() ?? 0),
          queries: options.targets.map((t) => ({
            ...t,
            datasource: this.getRef(),
            maxDataPoints: options.maxDataPoints,
            intervalMs: options.intervalMs,
          })),
        },
      })
      .pipe(map((rsp) => rsp.data));
  }

  // 与基类运行时默认行为一致：打后端 CheckHealth。非 2xx 会抛错，
  // 由数据源测试页统一呈现。
  async testDatasource(): Promise<TestDataSourceResponse> {
    const rsp = await lastValueFrom(
      getBackendSrv().fetch<{ status: string; message?: string }>({
        url: `/api/datasources/uid/${this.dsSettings.uid}/health`,
      })
    );
    return { status: rsp.data.status, message: rsp.data.message ?? '' };
  }

  // 帧由后端组织，前端不做模板变量展开。
  applyTemplateVariables(query: EcsQuery): EcsQuery {
    return query;
  }

  getRef(): { uid: string; type: string } {
    return { uid: this.dsSettings.uid, type: this.dsSettings.type };
  }
}
