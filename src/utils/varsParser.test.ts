import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import {
  parseYamlToVars,
  varsToYaml,
  varsToObject,
  objectToVars,
  parseExtraVars,
  formatExtraVars,
  detectVarType,
  coerceValueByType,
} from './varsParser.ts';
import type { VarEntry } from './varsParser.ts';

describe('varsParser - Ansible & YAML Variables Engine', () => {
  describe('detectVarType', () => {
    it('detects secret keys based on naming conventions', () => {
      assert.equal(detectVarType('restic_password', 'secret123'), 'secret');
      assert.equal(detectVarType('ansible_ssh_pass', 'foobar'), 'secret');
      assert.equal(detectVarType('vault_token', 'xyz'), 'secret');
      assert.equal(detectVarType('api_key', '12345'), 'secret');
      assert.equal(detectVarType('db_secret_key', 'pass'), 'secret');
      assert.equal(detectVarType('aws_secret_access_key', 'val'), 'secret');
      assert.equal(detectVarType('auth_token', 'jwt'), 'secret');
    });

    it('detects vault block or encrypted tags as secret', () => {
      assert.equal(detectVarType('custom_val', '!vault |\n  $ANSIBLE_VAULT;1.1;AES256'), 'secret');
      assert.equal(detectVarType('custom_val', '$ANSIBLE_VAULT;1.1;AES256;something'), 'secret');
    });

    it('detects booleans accurately', () => {
      assert.equal(detectVarType('podman_rootless', true), 'boolean');
      assert.equal(detectVarType('podman_rootless', false), 'boolean');
      assert.equal(detectVarType('podman_rootless', 'true'), 'boolean');
      assert.equal(detectVarType('podman_rootless', 'false'), 'boolean');
      assert.equal(detectVarType('flag', 'yes'), 'boolean');
      assert.equal(detectVarType('flag', 'no'), 'boolean');
    });

    it('detects numbers accurately', () => {
      assert.equal(detectVarType('port', 22), 'number');
      assert.equal(detectVarType('port', '22'), 'number');
      assert.equal(detectVarType('retention_days', '7'), 'number');
      assert.equal(detectVarType('ratio', '3.14'), 'number');
      assert.equal(detectVarType('ratio', 3.14), 'number');
    });

    it('defaults standard identifiers and text to string', () => {
      assert.equal(detectVarType('datacenter_name', 'leo-banjo.ts.net'), 'string');
      assert.equal(detectVarType('ssh_user', 'core'), 'string');
      assert.equal(detectVarType('path', '/run/user/1000/ssh-agent.socket'), 'string');
    });
  });

  describe('parseYamlToVars', () => {
    it('parses basic scalar variables from group_vars YAML', () => {
      const yaml = `---
datacenter_name: leo-banjo.ts.net
default_ssh_user: core
ansible_port: 22
podman_rootless_default: true
`;
      const res = parseYamlToVars(yaml);
      assert.equal(res.hasErrors, false);
      assert.equal(res.vars.length, 4);

      const dc = res.vars.find(v => v.key === 'datacenter_name');
      assert.ok(dc);
      assert.equal(dc.type, 'string');
      assert.equal(dc.value, 'leo-banjo.ts.net');

      const port = res.vars.find(v => v.key === 'ansible_port');
      assert.ok(port);
      assert.equal(port.type, 'number');
      assert.equal(port.value, 22);

      const rootless = res.vars.find(v => v.key === 'podman_rootless_default');
      assert.ok(rootless);
      assert.equal(rootless.type, 'boolean');
      assert.equal(rootless.value, true);
    });

    it('identifies secrets and passwords automatically', () => {
      const yaml = `---
restic_password: "super-secure-vault-pass"
vault_encryption_key: "AES-256-GCM-KEY"
regular_setting: "normal"
`;
      const res = parseYamlToVars(yaml);
      assert.equal(res.hasErrors, false);

      const pass = res.vars.find(v => v.key === 'restic_password');
      assert.ok(pass);
      assert.equal(pass.type, 'secret');
      assert.equal(pass.value, 'super-secure-vault-pass');

      const key = res.vars.find(v => v.key === 'vault_encryption_key');
      assert.ok(key);
      assert.equal(key.type, 'secret');
    });

    it('handles Ansible Vault !vault block scalars', () => {
      const yaml = `---
vault_secret: !vault |
          $ANSIBLE_VAULT;1.1;AES256
          3634323238623438363737303138373739343336336531393633333336313764
          3132333435363738
normal_var: value123
`;
      const res = parseYamlToVars(yaml);
      assert.equal(res.hasErrors, false);

      const vaultVar = res.vars.find(v => v.key === 'vault_secret');
      assert.ok(vaultVar);
      assert.equal(vaultVar.type, 'secret');
      assert.equal(vaultVar.isVault, true);
      assert.ok(String(vaultVar.value).includes('$ANSIBLE_VAULT;1.1;AES256'));
    });

    it('parses nested mappings with dotted keys and lists', () => {
      const yaml = `---
security:
  selinux_mode: enforcing
  tailscale_ssh_enabled: true
host_members:
  - likes-it-cold.leo-banjo.ts.net
  - ping.leo-banjo.ts.net
`;
      const res = parseYamlToVars(yaml);
      assert.equal(res.hasErrors, false);

      const selinux = res.vars.find(v => v.key === 'security.selinux_mode');
      assert.ok(selinux);
      assert.equal(selinux.value, 'enforcing');

      const sshEnabled = res.vars.find(v => v.key === 'security.tailscale_ssh_enabled');
      assert.ok(sshEnabled);
      assert.equal(sshEnabled.type, 'boolean');
      assert.equal(sshEnabled.value, true);

      const members = res.vars.find(v => v.key === 'host_members');
      assert.ok(members);
      assert.ok(Array.isArray(members.value));
      assert.equal(members.value.length, 2);
      assert.equal(members.value[0], 'likes-it-cold.leo-banjo.ts.net');
    });

    it('handles quoted strings with special characters and colons', () => {
      const yaml = `---
restic_repository: "s3://restic-homelab-vault/leo-banjo"
url: "https://leo-banjo.ts.net:8443/api/v1"
message: 'Hello, Homelab: Ready'
`;
      const res = parseYamlToVars(yaml);
      assert.equal(res.hasErrors, false);
      assert.equal(res.vars.find(v => v.key === 'restic_repository')?.value, 's3://restic-homelab-vault/leo-banjo');
      assert.equal(res.vars.find(v => v.key === 'url')?.value, 'https://leo-banjo.ts.net:8443/api/v1');
      assert.equal(res.vars.find(v => v.key === 'message')?.value, 'Hello, Homelab: Ready');
    });

    it('handles empty input and comments-only input gracefully', () => {
      const res1 = parseYamlToVars('');
      assert.equal(res1.hasErrors, false);
      assert.equal(res1.vars.length, 0);

      const res2 = parseYamlToVars('# Just a comment\n# Another comment\n');
      assert.equal(res2.hasErrors, false);
      assert.equal(res2.vars.length, 0);
    });

    it('parses JSON format seamlessly in parseYamlToVars', () => {
      const json = '{"force_reload": true, "ansible_port": 2222}';
      const res = parseYamlToVars(json);
      assert.equal(res.hasErrors, false);
      assert.equal(res.vars.length, 2);
      assert.equal(res.vars.find(v => v.key === 'force_reload')?.value, true);
      assert.equal(res.vars.find(v => v.key === 'ansible_port')?.value, 2222);
    });
  });

  describe('varsToYaml', () => {
    it('serializes typed variables back into clean Ansible YAML', () => {
      const vars: VarEntry[] = [
        { id: '1', key: 'datacenter_name', type: 'string', value: 'leo-banjo.ts.net' },
        { id: '2', key: 'ansible_port', type: 'number', value: 22 },
        { id: '3', key: 'podman_rootless_default', type: 'boolean', value: true },
        { id: '4', key: 'restic_password', type: 'secret', value: 'mypassword' },
      ];

      const yaml = varsToYaml(vars);
      assert.ok(yaml.startsWith('---'));
      assert.ok(yaml.includes('datacenter_name: leo-banjo.ts.net'));
      assert.ok(yaml.includes('ansible_port: 22'));
      assert.ok(yaml.includes('podman_rootless_default: true'));
      assert.ok(yaml.includes('restic_password: "mypassword"'));

      // Round-trip verification
      const reparsed = parseYamlToVars(yaml);
      assert.equal(reparsed.hasErrors, false);
      assert.equal(reparsed.vars.length, 4);
      assert.equal(reparsed.vars.find(v => v.key === 'ansible_port')?.value, 22);
      assert.equal(reparsed.vars.find(v => v.key === 'podman_rootless_default')?.value, true);
    });

    it('unflattens dotted keys into nested YAML mappings', () => {
      const vars: VarEntry[] = [
        { id: '1', key: 'security.selinux_mode', type: 'string', value: 'enforcing' },
        { id: '2', key: 'security.tailscale_ssh_enabled', type: 'boolean', value: true },
      ];

      const yaml = varsToYaml(vars);
      assert.ok(yaml.includes('security:'));
      assert.ok(yaml.includes('  selinux_mode: enforcing'));
      assert.ok(yaml.includes('  tailscale_ssh_enabled: true'));

      // Reparse
      const reparsed = parseYamlToVars(yaml);
      assert.equal(reparsed.vars.find(v => v.key === 'security.selinux_mode')?.value, 'enforcing');
      assert.equal(reparsed.vars.find(v => v.key === 'security.tailscale_ssh_enabled')?.value, true);
    });

    it('formats arrays properly as YAML bullet points', () => {
      const vars: VarEntry[] = [
        {
          id: '1',
          key: 'host_members',
          type: 'string',
          value: ['likes-it-cold.leo-banjo.ts.net', 'ping.leo-banjo.ts.net'],
        },
      ];

      const yaml = varsToYaml(vars);
      assert.ok(yaml.includes('host_members:'));
      assert.ok(yaml.includes('  - likes-it-cold.leo-banjo.ts.net'));
      assert.ok(yaml.includes('  - ping.leo-banjo.ts.net'));
    });

    it('serializes vault blocks with !vault tag', () => {
      const vars: VarEntry[] = [
        {
          id: '1',
          key: 'vault_creds',
          type: 'secret',
          isVault: true,
          value: '$ANSIBLE_VAULT;1.1;AES256\n3132333435363738',
        },
      ];

      const yaml = varsToYaml(vars);
      assert.ok(yaml.includes('vault_creds: !vault |'));
      assert.ok(yaml.includes('  $ANSIBLE_VAULT;1.1;AES256'));
    });

    it('returns empty string when vars list is empty or whitespace-only', () => {
      assert.equal(varsToYaml([]), '');
      assert.equal(varsToYaml([{ id: '1', key: '', type: 'string', value: '' }]), '');
      assert.equal(varsToYaml([{ id: '2', key: '   ', type: 'number', value: 42 }]), '');
    });
  });

  describe('coerceValueByType', () => {
    it('allows empty string or null for numbers without premature 0 coercion', () => {
      assert.equal(coerceValueByType('number', ''), '');
      assert.equal(coerceValueByType('number', null), '');
      assert.equal(coerceValueByType('number', undefined), '');
      assert.equal(coerceValueByType('number', 42), 42);
      assert.equal(coerceValueByType('number', '100'), 100);
    });
  });

  describe('varsToObject & objectToVars', () => {
    it('converts between Record<string, any> and VarEntry[]', () => {
      const raw = {
        ansible_user: 'core',
        ansible_port: 22,
        restic_password: 'secret_string',
        is_cluster: true,
      };

      const vars = objectToVars(raw);
      assert.equal(vars.length, 4);

      const pass = vars.find(v => v.key === 'restic_password');
      assert.ok(pass);
      assert.equal(pass.type, 'secret');

      const port = vars.find(v => v.key === 'ansible_port');
      assert.ok(port);
      assert.equal(port.type, 'number');

      const obj = varsToObject(vars);
      assert.deepEqual(obj, raw);
    });

    it('generates deterministic IDs and preserves existing IDs', () => {
      const raw = { port: 22, host: 'localhost' };
      const v1 = objectToVars(raw);
      assert.equal(v1.find(v => v.key === 'port')?.id, 'var_port');
      assert.equal(v1.find(v => v.key === 'host')?.id, 'var_host');

      // Preserving existing IDs
      const existing: VarEntry[] = [{ id: 'custom_id_123', key: 'port', type: 'number', value: 22 }];
      const v2 = objectToVars(raw, '', existing);
      assert.equal(v2.find(v => v.key === 'port')?.id, 'custom_id_123');
      assert.equal(v2.find(v => v.key === 'host')?.id, 'var_host');
    });
  });

  describe('parseExtraVars & formatExtraVars', () => {
    it('parses JSON formatted extra vars', () => {
      const json = '{"force_reload": true, "max_retries": 3, "deploy_target": "node1"}';
      const vars = parseExtraVars(json);
      assert.equal(vars.length, 3);
      assert.equal(vars.find(v => v.key === 'force_reload')?.value, true);
      assert.equal(vars.find(v => v.key === 'max_retries')?.value, 3);
      assert.equal(vars.find(v => v.key === 'deploy_target')?.value, 'node1');
    });

    it('parses YAML formatted extra vars', () => {
      const yaml = `force_reload: true
target_host: dev-pc.leo-banjo.ts.net
secret_key: supersecret
`;
      const vars = parseExtraVars(yaml);
      assert.equal(vars.length, 3);
      assert.equal(vars.find(v => v.key === 'force_reload')?.value, true);
      assert.equal(vars.find(v => v.key === 'secret_key')?.type, 'secret');
    });

    it('parses key=value extra vars pairs', () => {
      const kv = 'target=production debug=true retries=5';
      const vars = parseExtraVars(kv);
      assert.equal(vars.length, 3);
      assert.equal(vars.find(v => v.key === 'target')?.value, 'production');
      assert.equal(vars.find(v => v.key === 'debug')?.value, true);
      assert.equal(vars.find(v => v.key === 'retries')?.value, 5);
    });

    it('formats extra vars to JSON string for Ansible CLI execution', () => {
      const vars: VarEntry[] = [
        { id: '1', key: 'force_reload', type: 'boolean', value: true },
        { id: '2', key: 'ansible_port', type: 'number', value: 22 },
        { id: '3', key: 'env', type: 'string', value: 'prod' },
      ];

      const jsonStr = formatExtraVars(vars, 'json');
      const parsed = JSON.parse(jsonStr);
      assert.deepEqual(parsed, {
        force_reload: true,
        ansible_port: 22,
        env: 'prod',
      });
    });

    it('formats empty extra vars gracefully', () => {
      assert.equal(formatExtraVars([], 'json'), '');
      assert.deepEqual(parseExtraVars(''), []);
      assert.deepEqual(parseExtraVars('   '), []);
    });
  });
});
