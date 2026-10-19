import test, { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import {
  parseGlancesRestData,
  parseGlancesCliJson,
  parseSysfsTelemetry,
  fetchNodeTelemetryMultiTier,
  type GlancesMetricPayload,
} from './telemetryCollector.ts';
import {
  DEFAULT_TELEMETRY_POLL_INTERVAL_MS,
  DEFAULT_POLLING_INTERVAL_MS,
} from '../hooks/useHostPolling.ts';
import type { NodeItem } from '../types/index.ts';

const mockNode: NodeItem = {
  id: 'node-test-1',
  name: 'prod-app-01',
  shortName: 'app01',
  group: 'production',
  datacenter: 'dc-primary',
  nodeType: 'linux',
  ip: '192.168.1.100',
  port: 22,
  tailscaleIp: '100.64.0.10',
  tailscaleHost: 'prod-app-01.ts.net',
  osDescription: 'Ubuntu 24.04 LTS',
  status: 'online',
  uptime: '10 days',
  hasGlances: true,
  hasCockpit: false,
  hasPodman: true,
  hasRestic: true,
  specs: {
    cpu: 'AMD EPYC',
    vcpus: 8,
    ramGiB: 32,
    motherboard: 'Supermicro',
    bios: '1.2',
    ipmi: 'none',
    ecc: 'yes',
    secureBoot: 'yes',
    iommu: 'yes',
  },
};

describe('Glances Telemetry Collector - Unit Tests', () => {
  describe('parseGlancesRestData', () => {
    it('parses valid Glances REST JSON object and sets source to http_rest', () => {
      const restData = {
        cpu: { total: 42.5, user: 30.0, system: 12.5 },
        mem: { percent: 65.4, used: 21474836480, total: 34359738368 },
        load: { min1: 1.45, min5: 2.10, min15: 1.95 },
      };

      const result = parseGlancesRestData(restData);
      assert.deepEqual(result, {
        cpu: { total: 42.5 },
        mem: { percent: 65.4, used: 21474836480, total: 34359738368 },
        load: { min1: 1.45, min5: 2.1, min15: 1.95 },
        source: 'http_rest',
      });
    });

    it('parses valid Glances REST JSON string and handles defaults', () => {
      const rawString = JSON.stringify({
        cpu: { total: 12.0 },
        mem: { percent: 45.0, used: 4000, total: 8000 },
        load: { min1: 0.5, min5: 0.8, min15: 0.6 },
      });

      const result = parseGlancesRestData(rawString);
      assert.equal(result.cpu.total, 12.0);
      assert.equal(result.mem.percent, 45.0);
      assert.equal(result.source, 'http_rest');
    });

    it('handles missing fields gracefully without throwing', () => {
      const partial = {};
      const result = parseGlancesRestData(partial);
      assert.equal(result.cpu.total, 0);
      assert.equal(result.mem.percent, 0);
      assert.equal(result.mem.used, 0);
      assert.equal(result.load.min1, 0);
      assert.equal(result.source, 'http_rest');
    });
  });

  describe('parseGlancesCliJson', () => {
    it('parses glances --stdout-json output and sets source to cli_stdout_json', () => {
      const stdout = JSON.stringify({
        now: { iso: '2026-10-19T06:32:07-04:00' },
        cpu: { total: 34.2, user: 20.6, system: 14.4 },
        mem: { total: 33562943488, percent: 67.8, used: 22765879320 },
        load: { min1: 2.71, min5: 2.97, min15: 3.07, cpucore: 12 },
      });

      const result = parseGlancesCliJson(stdout);
      assert.deepEqual(result, {
        cpu: { total: 34.2 },
        mem: { percent: 67.8, used: 22765879320, total: 33562943488 },
        load: { min1: 2.71, min5: 2.97, min15: 3.07 },
        source: 'cli_stdout_json',
      });
    });

    it('extracts JSON surrounded by terminal log text', () => {
      const stdout = `
[INFO] Initializing glances collector...
{"cpu":{"total":18.4},"mem":{"percent":32.1,"used":1000000,"total":3000000},"load":{"min1":0.3,"min5":0.4,"min15":0.5}}
Glances finished cleanly.
`;
      const result = parseGlancesCliJson(stdout);
      assert.equal(result.cpu.total, 18.4);
      assert.equal(result.mem.percent, 32.1);
      assert.equal(result.source, 'cli_stdout_json');
    });

    it('throws error on invalid JSON string', () => {
      assert.throws(() => parseGlancesCliJson('glances: command not found'));
    });
  });

  describe('parseSysfsTelemetry', () => {
    it('parses loadavg, meminfo, and stat output and sets source to pcp_fallback', () => {
      const rawOutput = `
0.85 1.20 1.05 3/980 45671
MemTotal:       32776312 kB
MemFree:         5845692 kB
MemAvailable:   10509188 kB
Buffers:           28000 kB
Cached:          4414844 kB
cpu  2000 500 1500 6000 100 50 20 0 0 0
`;
      const result = parseSysfsTelemetry(rawOutput);
      assert.equal(result.source, 'pcp_fallback');
      assert.equal(result.load.min1, 0.85);
      assert.equal(result.load.min5, 1.2);
      assert.equal(result.load.min15, 1.05);

      // Memory: total 32776312 * 1024, avail 10509188 * 1024
      // Used = (32776312 - 10509188) * 1024 = 22267124 * 1024 = 22801534976
      // Percent = (22267124 / 32776312) * 100 ≈ 67.9%
      assert.equal(result.mem.total, 32776312 * 1024);
      assert.equal(result.mem.used, 22267124 * 1024);
      assert.ok(Math.abs(result.mem.percent - 67.9) < 0.2);

      // CPU: total ticks = 2000 + 500 + 1500 + 6000 + 100 + 50 + 20 = 10170
      // idle = 6000 + 100 = 6100, busy = 4070
      // busy % = (4070 / 10170) * 100 ≈ 40.0%
      assert.ok(Math.abs(result.cpu.total - 40.0) < 0.2);
    });

    it('falls back to MemFree + Buffers + Cached if MemAvailable is absent', () => {
      const rawOutput = `
1.50 1.25 1.10 2/500 1234
MemTotal:       1000000 kB
MemFree:         200000 kB
Buffers:          50000 kB
Cached:          150000 kB
`;
      const result = parseSysfsTelemetry(rawOutput);
      assert.equal(result.mem.total, 1000000 * 1024);
      assert.equal(result.mem.used, 600000 * 1024);
      assert.equal(result.mem.percent, 60.0);
    });
  });

  describe('fetchNodeTelemetryMultiTier', () => {
    it('Tier 1: succeeds directly via HTTP REST without calling remote command', async () => {
      let fetchCalled = false;
      let fetchedUrl = '';
      const mockFetch = async (url: string) => {
        fetchCalled = true;
        fetchedUrl = url;
        return {
          ok: true,
          json: async () => ({
            cpu: { total: 25.0 },
            mem: { percent: 50.0, used: 16000000000, total: 32000000000 },
            load: { min1: 1.0, min5: 1.2, min15: 1.1 },
          }),
        } as any;
      };
      let execCalled = false;
      const mockExec = async () => {
        execCalled = true;
        return '';
      };

      const result = await fetchNodeTelemetryMultiTier(mockNode, {
        httpFetch: mockFetch as any,
        execRemoteCommand: mockExec,
      });

      assert.equal(result.source, 'http_rest');
      assert.equal(result.cpu.total, 25.0);
      assert.equal(fetchCalled, true);
      assert.ok(fetchedUrl.includes('http://100.64.0.10:61208/api/3/all'));
      assert.equal(execCalled, false);
    });

    it('Tier 2: falls back to SSH localhost curl when HTTP REST fails', async () => {
      const mockFetch = async () => {
        throw new Error('Connection refused');
      };
      let executedCmd = '';
      const mockExec = async (_node: NodeItem, cmd: string) => {
        executedCmd = cmd;
        if (cmd.includes('curl -s --connect-timeout 2 http://127.0.0.1:61208/api/3/all')) {
          return JSON.stringify({
            cpu: { total: 31.0 },
            mem: { percent: 55.0, used: 11000000000, total: 20000000000 },
            load: { min1: 2.0, min5: 2.1, min15: 1.8 },
          });
        }
        throw new Error(`Unexpected command: ${cmd}`);
      };

      const result = await fetchNodeTelemetryMultiTier(mockNode, {
        httpFetch: mockFetch as any,
        execRemoteCommand: mockExec,
      });

      assert.equal(result.source, 'ssh_localhost_curl');
      assert.equal(result.cpu.total, 31.0);
      assert.ok(executedCmd.includes('curl -s --connect-timeout 2 http://127.0.0.1:61208/api/3/all'));
    });

    it('Tier 3: falls back to CLI glances --stdout-json when HTTP and SSH curl fail', async () => {
      const mockFetch = async () => {
        throw new Error('HTTP timeout');
      };
      const mockExec = async (_node: NodeItem, cmd: string) => {
        if (cmd.includes('curl')) {
          throw new Error('curl: (7) Failed to connect to 127.0.0.1 port 61208: Connection refused');
        }
        if (cmd.includes('glances --stdout-json')) {
          return JSON.stringify({
            cpu: { total: 19.5 },
            mem: { percent: 40.0, used: 8000000000, total: 20000000000 },
            load: { min1: 0.8, min5: 0.9, min15: 1.0 },
          });
        }
        throw new Error(`Unexpected command: ${cmd}`);
      };

      const result = await fetchNodeTelemetryMultiTier(mockNode, {
        httpFetch: mockFetch as any,
        execRemoteCommand: mockExec,
      });

      assert.equal(result.source, 'cli_stdout_json');
      assert.equal(result.cpu.total, 19.5);
    });

    it('Tier 4: falls back to proc/sysfs when Glances CLI is not available', async () => {
      const mockFetch = async () => {
        throw new Error('HTTP timeout');
      };
      const mockExec = async (_node: NodeItem, cmd: string) => {
        if (cmd.includes('curl')) {
          throw new Error('curl connection failed');
        }
        if (cmd.includes('glances --stdout-json')) {
          throw new Error('sh: glances: command not found');
        }
        if (cmd.includes('/proc/loadavg')) {
          return `
0.50 0.60 0.70 1/300 9999
MemTotal:       16384000 kB
MemFree:         4096000 kB
MemAvailable:    8192000 kB
cpu  1000 200 800 8000 0 0 0 0 0 0
`;
        }
        throw new Error(`Unexpected command: ${cmd}`);
      };

      const result = await fetchNodeTelemetryMultiTier(mockNode, {
        httpFetch: mockFetch as any,
        execRemoteCommand: mockExec,
      });

      assert.equal(result.source, 'pcp_fallback');
      assert.equal(result.load.min1, 0.5);
      assert.equal(result.mem.percent, 50.0);
    });

    it('adheres to Zero-Mock Policy: throws error when all tiers fail', async () => {
      const mockFetch = async () => {
        throw new Error('Network unreachable');
      };
      const mockExec = async () => {
        throw new Error('SSH connection refused');
      };

      await assert.rejects(
        async () => {
          await fetchNodeTelemetryMultiTier(mockNode, {
            httpFetch: mockFetch as any,
            execRemoteCommand: mockExec,
          });
        },
        /Failed to collect telemetry.*all probe tiers exhausted/i
      );
    });
  });

  describe('useHostPolling conservative cadence', () => {
    it('sets default polling interval to 45 seconds (within conservative 30-60s range)', () => {
      assert.equal(DEFAULT_TELEMETRY_POLL_INTERVAL_MS, 45000);
      assert.equal(DEFAULT_POLLING_INTERVAL_MS, 45000);
      assert.ok(DEFAULT_TELEMETRY_POLL_INTERVAL_MS >= 30000 && DEFAULT_TELEMETRY_POLL_INTERVAL_MS <= 60000);
    });
  });
});
