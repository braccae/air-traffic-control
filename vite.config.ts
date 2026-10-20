import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import path from 'path';
import {defineConfig} from 'vite';

export default defineConfig(() => {
  return {
    plugins: [react(), tailwindcss()],
    resolve: {
      alias: {
        '@': path.resolve(import.meta.dirname, '.'),
      },
    },
    server: {
      // HMR is disabled in AI Studio via DISABLE_HMR env var.
      // Do not modify—file watching is disabled to prevent flickering during agent edits.
      hmr: process.env.DISABLE_HMR !== 'true',
      // Disable file watching when DISABLE_HMR is true to save CPU during agent edits.
      watch: process.env.DISABLE_HMR === 'true' ? null : {
        ignored: ['**/data/**', '**/*.sqlite*', '**/backend-go/**'],
      },
      // Dev-only: when Vite runs standalone (see justfile `dev-frontend`), forward API + WS
      // traffic to the Go daemon. In Express middlewareMode these options are ignored.
      proxy: {
        '/api': { target: 'http://127.0.0.1:18090', changeOrigin: true },
        '/ws': { target: 'ws://127.0.0.1:18090', ws: true },
      },
    },
  };
});
