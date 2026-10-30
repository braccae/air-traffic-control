import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import fs from 'node:fs';
import path from 'path';
import {defineConfig, type Plugin} from 'vite';

// Dev-only: the legacy Express workflow serves /ghostty-vt.wasm straight out of
// node_modules. When Vite runs standalone (`just dev-split`) nothing else serves
// it, so expose it here. In production the same file is copied into the Go embed
// tree by `just build-all`.
function ghosttyWasmPlugin(): Plugin {
  const wasmPath = path.resolve(import.meta.dirname, 'node_modules/ghostty-web/ghostty-vt.wasm');
  return {
    name: 'ghostty-wasm',
    apply: 'serve',
    configureServer(server) {
      server.middlewares.use('/ghostty-vt.wasm', (_req, res, next) => {
        if (!fs.existsSync(wasmPath)) {
          next();
          return;
        }
        res.setHeader('Content-Type', 'application/wasm');
        fs.createReadStream(wasmPath).pipe(res);
      });
    },
  };
}

export default defineConfig(() => {
  return {
    plugins: [react(), tailwindcss(), ghosttyWasmPlugin()],
    resolve: {
      alias: {
        '@': path.resolve(import.meta.dirname, '.'),
      },
    },
    server: {
      // Allow connections from containerized environments (e.g. host.containers.internal)
      allowedHosts: ['host.containers.internal', 'hermes'],
      // HMR is disabled in AI Studio via DISABLE_HMR env var.
      // Do not modify—file watching is disabled to prevent flickering during agent edits.
      hmr: process.env.DISABLE_HMR !== 'true',
      // Disable file watching when DISABLE_HMR is true to save CPU during agent edits.
      watch: process.env.DISABLE_HMR === 'true' ? null : {
        // e2e-results is rewritten by Playwright (.last-run.json) while the dev
        // server is up; watching it triggers full page reloads that wipe
        // in-progress form input.
        ignored: ['**/data/**', '**/*.sqlite*', '**/backend-go/**', '**/e2e-results/**', '**/test-results/**'],
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
