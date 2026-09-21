/*
 * 依官方 create-plugin 模板（grafana/plugin-tools templates/common/.config/eslint.config.mjs）适配：
 * 本项目未迁移到 .config/ 脚手架，故合并为根目录单文件。
 */

import { defineConfig } from 'eslint/config';
import grafanaConfig from '@grafana/eslint-config/flat.js';

export default defineConfig([
  {
    ignores: [
      '**/logs',
      '**/*.log',
      '**/npm-debug.log*',
      'node_modules/',
      'dist/',
      'test-results/',
      'playwright-report/',
      'blob-report/',
      'playwright/.cache/',
      'playwright/.auth/',
      '**/.eslintcache',
      '.zcode/',
      // CJS 基础设施文件与生成物，不参与前端 lint
      'webpack.config.js',
      'scripts/**',
      'provisioning/**',
      'src/permissions.gen.ts',
    ],
  },
  ...grafanaConfig,
  {
    rules: {
      'react/prop-types': 'off',
    },
  },
  {
    files: ['src/**/*.{ts,tsx}', 'tests/**/*.ts'],

    languageOptions: {
      parserOptions: {
        project: './tsconfig.json',
      },
    },

    rules: {
      '@typescript-eslint/no-deprecated': 'warn',
      // 现有「切 tab 触发异步加载」模式（App.tsx / EcsInfo.tsx）为刻意的 fetch-on-effect
      // 写法；迁移到 loader/惰性初始化属重构，另行处理。
      'react-hooks/set-state-in-effect': 'warn',
    },
  },
]);
