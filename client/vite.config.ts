/// <reference types="vitest/config" />
import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import path from 'path';
import {defineConfig} from 'vite';

export default defineConfig(() => {
  return {
    plugins: [react(), tailwindcss()],
    test: {
      environment: 'jsdom',
      setupFiles: ['./src/test/setup.ts'],
      include: ['src/**/*.{test,spec}.{ts,tsx}'],
      // Pin NODE_ENV here rather than in the npm script: a shell prefix is not
      // portable, and with an ambient NODE_ENV=production React resolves its
      // production build, which has no React.act and breaks every render().
      env: { NODE_ENV: 'test' },
    },
    resolve: {
      alias: {
        '@': path.resolve(__dirname, '.'),
      },
    },
    server: {
      port: process.env.VITE_PORT ? Number(process.env.VITE_PORT) : 3000,
      // HMR is disabled in AI Studio via DISABLE_HMR env var.
      hmr: process.env.DISABLE_HMR !== 'true',
      watch: process.env.DISABLE_HMR === 'true' ? null : {},
      // Proxy API calls to the Go backend during dev.
      proxy: {
        '/api': {
          target: process.env.ADOBOFLIX_API || 'http://127.0.0.1:5656',
          changeOrigin: true,
        },
      },
    },
  };
});
