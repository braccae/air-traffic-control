import { describe, it, beforeEach, afterEach } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import {
  parseAnsibleCfg,
  serializeAnsibleCfg,
  loadAnsibleConfig,
  saveAnsibleConfig,
  type AnsibleConfigSettings,
  DEFAULT_ANSIBLE_CONFIG,
} from './ansibleConfigManager.ts';

const expect = (actual: any) => ({
  toBe: (expected: any) => assert.equal(actual, expected),
  toEqual: (expected: any) => assert.deepEqual(actual, expected),
  toContain: (expected: any) => assert.ok(actual.includes(expected)),
});

describe('ansibleConfigManager', () => {
  const sampleIni = `
[defaults]
stdout_callback = json
forks = 15
timeout = 45
host_key_checking = False
pipelining = True

[privilege_escalation]
become_method = run0
`;

  it('parses INI content into typed AnsibleConfigSettings', () => {
    const settings = parseAnsibleCfg(sampleIni);
    expect(settings.stdoutCallback).toBe('json');
    expect(settings.forks).toBe(15);
    expect(settings.timeout).toBe(45);
    expect(settings.hostKeyChecking).toBe(false);
    expect(settings.pipelining).toBe(true);
    expect(settings.becomeMethod).toBe('run0');
  });

  it('parses empty or missing keys into defaults', () => {
    const emptySettings = parseAnsibleCfg('');
    expect(emptySettings.stdoutCallback).toBe('json');
    expect(emptySettings.forks).toBe(10);
    expect(emptySettings.timeout).toBe(30);
    expect(emptySettings.hostKeyChecking).toBe(false);
    expect(emptySettings.pipelining).toBe(true);
    expect(emptySettings.becomeMethod).toBe('run0');
  });

  it('serializes updated settings back into compliant ansible.cfg INI format', () => {
    const updated: AnsibleConfigSettings = {
      stdoutCallback: 'json',
      forks: 20,
      timeout: 60,
      hostKeyChecking: false,
      pipelining: true,
      becomeMethod: 'sudo',
    };
    const out = serializeAnsibleCfg(updated, sampleIni);
    expect(out).toContain('stdout_callback = json');
    expect(out).toContain('forks = 20');
    expect(out).toContain('become_method = sudo');
  });

  it('serializes default settings when base content is omitted', () => {
    const out = serializeAnsibleCfg(DEFAULT_ANSIBLE_CONFIG);
    expect(out).toContain('[defaults]');
    expect(out).toContain('stdout_callback = json');
    expect(out).toContain('forks = 10');
    expect(out).toContain('timeout = 30');
    expect(out).toContain('host_key_checking = False');
    expect(out).toContain('pipelining = True');
    expect(out).toContain('[privilege_escalation]');
    expect(out).toContain('become_method = run0');
  });

  describe('file operations', () => {
    let tmpDir: string;
    let tmpCfgPath: string;

    beforeEach(() => {
      tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'ansible-cfg-test-'));
      tmpCfgPath = path.join(tmpDir, 'ansible.cfg');
    });

    afterEach(() => {
      if (fs.existsSync(tmpDir)) {
        fs.rmSync(tmpDir, { recursive: true, force: true });
      }
    });

    it('generates default ansible.cfg if file does not exist when loading', async () => {
      expect(fs.existsSync(tmpCfgPath)).toBe(false);
      const loaded = await loadAnsibleConfig(tmpCfgPath);
      expect(fs.existsSync(tmpCfgPath)).toBe(true);
      expect(loaded.stdoutCallback).toBe('json');
      expect(loaded.forks).toBe(10);
      expect(loaded.becomeMethod).toBe('run0');
    });

    it('saves updated config and reloads correctly', async () => {
      const customSettings: AnsibleConfigSettings = {
        stdoutCallback: 'yaml',
        forks: 25,
        timeout: 90,
        hostKeyChecking: true,
        pipelining: false,
        becomeMethod: 'doas',
      };

      const saved = await saveAnsibleConfig(customSettings, tmpCfgPath);
      expect(saved).toBe(true);

      const loaded = await loadAnsibleConfig(tmpCfgPath);
      expect(loaded.stdoutCallback).toBe('yaml');
      expect(loaded.forks).toBe(25);
      expect(loaded.timeout).toBe(90);
      expect(loaded.hostKeyChecking).toBe(true);
      expect(loaded.pipelining).toBe(false);
      expect(loaded.becomeMethod).toBe('doas');
    });
  });
});
