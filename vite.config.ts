import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import fs from 'node:fs';
import path from 'path';
import {defineConfig, loadEnv, type Plugin} from 'vite';

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

export default defineConfig(({mode}) => {
  // Vite rejects any Host header it does not recognise (DNS-rebinding guard), so
  // every dev hostname — container bridge names, tailnet names, homelab domains,
  // reverse-proxied names — must be listed. Extend without editing code via
  // ATC_ALLOWED_HOSTS (comma-separated) in the shell or .env, e.g.
  //   ATC_ALLOWED_HOSTS=atc.leo-banjo.ts.net,atc.lan just dev
  const env = loadEnv(mode, import.meta.dirname, '');
  const extraAllowedHosts = (env.ATC_ALLOWED_HOSTS ?? '')
    .split(',')
    .map((host) => host.trim())
    .filter(Boolean);
  const allowedHosts = [
    // Containerized environments (e.g. host.containers.internal)
    'host.containers.internal',
    'hermes',
    'develop.homelabb.cc',
    ...extraAllowedHosts,
  ];

  // Port of the Go daemon the dev supervisor started (`scripts/dev.sh` exports
  // ATC_API_PORT). Falls back to the justfile default.
  const apiPort = process.env.ATC_API_PORT || env.ATC_API_PORT || '18090';
  const apiTarget = `http://127.0.0.1:${apiPort}`;
  const apiWsTarget = `ws://127.0.0.1:${apiPort}`;

  return {
    plugins: [react(), tailwindcss(), ghosttyWasmPlugin()],
    resolve: {
      alias: {
        '@': path.resolve(import.meta.dirname, '.'),
      },
    },
    server: {
      allowedHosts,
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
      // The port follows the daemon the dev supervisor actually started, so
      // `API_PORT=9090 just dev` keeps /api and /ws on the same backend.
      proxy: {
        '/api': { target: apiTarget, changeOrigin: true },
        '/ws': { target: apiWsTarget, ws: true },
      },
    },
    // `vite preview` keeps its own allowlist; mirror the dev one so the same
    // hostnames work against a built bundle.
    preview: {
      allowedHosts,
    },
  };
});
