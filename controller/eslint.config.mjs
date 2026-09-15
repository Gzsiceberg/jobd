import eslint from '@eslint/js';
import globals from 'globals';
import tseslint from 'typescript-eslint';
import { createNodeTypeCheckedConfig } from '../tooling/eslint/base.mjs';

export default createNodeTypeCheckedConfig({
  eslint,
  globals,
  tseslint,
  tsconfigRootDir: import.meta.dirname,
  sourceFiles: ['src/**/*.ts', 'test/**/*.ts'],
  testFiles: ['test/**/*.ts'],
  baseGlobals: globals.serviceworker,
});
