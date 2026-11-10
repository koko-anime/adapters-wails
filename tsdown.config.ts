import { defineConfig } from 'tsdown';

export default defineConfig({
  entry: { index: 'src/index.ts' },
  format: 'esm',
  dts: true,
  platform: 'neutral',
  target: 'es2022',
  clean: true,
  sourcemap: true,
  external: [/^@koko-anime\/formats/, /^@wailsio\//],
});
