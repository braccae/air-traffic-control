import React, { useState, useEffect, useMemo } from 'react';
import type { NodeItem, NodeType } from '../../types/index.ts';
import { buildProvisionedNode } from '../../utils/navigationViewModel.ts';
import type { ProbedHardware } from '../../utils/hardwareProbe.ts';
export function normalizeTag(tag: string): string {
  if (!tag || typeof tag !== 'string') return '';
  return tag.trim().toLowerCase().replace(/^tag:/, '');
}

export function matchesTailscaleTag(peerTags: string[] | undefined, targetTag: string): boolean {
  if (!peerTags || !Array.isArray(peerTags) || !targetTag || typeof targetTag !== 'string') {
    return false;
  }
  const normalizedTarget = normalizeTag(targetTag);
  if (!normalizedTarget) return false;
  return peerTags.some(t => typeof t === 'string' && normalizeTag(t) === normalizedTarget);
}

export interface TailscalePeerOption {
  hostName: string;
  dnsName?: string;
  ip: string;
  os?: string;
  online?: boolean;
  tags?: string[];
  Tags?: string[];
}

export interface AddNodeModalProps {
  isOpen: boolean;
  isMinimized?: boolean;
  onMinimize?: () => void;
  onClose: () => void;
  onAddNode: (node: Partial<NodeItem>) => void;
  initialTab?: 'tailscale' | 'manual_ssh' | 'winrm' | 'follow_tag';
  peers?: TailscalePeerOption[];
  onFollowTag?: (chunk: { tag: string; authMethod: string; sshUser?: string; vaultSecretId?: string }) => void;
  nodes?: NodeItem[];
}

export interface VaultVariable {
  name: string;
  createdAt: number;
  updatedAt: number;
  maskedValue: string;
}

export const AddNodeModal: React.FC<AddNodeModalProps> = ({
  isOpen,
  isMinimized = false,
  onMinimize,
  onClose,
  onAddNode,
  initialTab = 'tailscale',
  peers,
  onFollowTag,
  nodes = [],
}) => {
  const [activeTab, setActiveTab] = useState<'tailscale' | 'manual_ssh' | 'winrm' | 'follow_tag'>(initialTab);

  // Common host state
  const [name, setName] = useState('');
  const [group, setGroup] = useState('tag:host');
  const [nodeType, setNodeType] = useState<NodeType>('linux');
  const [ip, setIp] = useState('');
  const [port, setPort] = useState(22);
  const [sshUser, setSshUser] = useState('core');
  const [tailscaleIp, setTailscaleIp] = useState('');
  const [tailscaleHost, setTailscaleHost] = useState('');

  // Tailscale peers
  const [tsPeers, setTsPeers] = useState<TailscalePeerOption[]>(peers || []);
  const [selectedPeer, setSelectedPeer] = useState<string>('');

  // Probing state
  const [probing, setProbing] = useState(false);
  const [probeError, setProbeError] = useState<string | null>(null);
  const [probedData, setProbedData] = useState<ProbedHardware | null>(null);

  // Manual SSH / Secret / Vault state
  const [useSshAgent, setUseSshAgent] = useState(false);
  const [secretType, setSecretType] = useState<'password' | 'sshkey'>('password');
  const [credentialMode, setCredentialMode] = useState<'existing' | 'new'>('existing');
  const [newSecretName, setNewSecretName] = useState('');
  const [secretText, setSecretText] = useState('');
  const [masterVaultPassword, setMasterVaultPassword] = useState('');
  const [vaultStatus, setVaultStatus] = useState<{ hasPasswordSource: boolean; source: string } | null>(null);
  const [vaultError, setVaultError] = useState<string | null>(null);
  const [testingConnection, setTestingConnection] = useState(false);
  const [connectionResult, setConnectionResult] = useState<{ ok: boolean; message: string } | null>(null);

  // WinRM state
  const [winrmPort, setWinrmPort] = useState(5985);
  const [winrmHttps, setWinrmHttps] = useState(false);
  const [winrmTransport, setWinrmTransport] = useState<'ntlm' | 'basic' | 'kerberos'>('ntlm');
  const [winrmUser, setWinrmUser] = useState('Administrator');

  // Follow Tag state
  const [followTag, setFollowTag] = useState('');
  const [followSshUser, setFollowSshUser] = useState('core');
  const [followAuthMethod, setFollowAuthMethod] = useState<'tailscale-ssh' | 'vault-password' | 'vault-sshkey' | 'ssh-agent'>('tailscale-ssh');
  const [followCredentialMode, setFollowCredentialMode] = useState<'existing' | 'new'>('existing');
  const [followSecretName, setFollowSecretName] = useState('');
  const [followSecret, setFollowSecret] = useState('');
  const [followMasterPassword, setFollowMasterPassword] = useState('');
  const [tagSubmitting, setTagSubmitting] = useState(false);
  const [tagError, setTagError] = useState<string | null>(null);

  // Vault variables for dropdown selection
  const [vaultVariables, setVaultVariables] = useState<VaultVariable[]>([]);
  const [selectedVaultSecretId, setSelectedVaultSecretId] = useState<string>('');

  // Sync peers prop if provided
  useEffect(() => {
    if (peers && peers.length > 0) {
      setTsPeers(peers);
    }
  }, [peers]);

  const refreshVaultVariables = async () => {
    try {
      const res = await fetch('/api/vault/variables');
      const data = await res.json();
      if (data.success && Array.isArray(data.variables)) {
        setVaultVariables(data.variables);
      }
    } catch {
      // Non-blocking; the operator can still type a secret name manually.
    }
  };

  // Fetch Tailscale peers and Vault status on open
  useEffect(() => {
    if (!isOpen) return;
    setProbeError(null);
    setVaultError(null);

    if (!peers || peers.length === 0) {
      fetch('/api/tailscale/peers')
        .then(res => res.json())
        .then(data => {
          if (data.success && Array.isArray(data.peers)) {
            const peersList: TailscalePeerOption[] = data.peers.map((p: any) => ({
              hostName: p.HostName || p.DNSName?.split('.')[0] || 'unknown',
              dnsName: p.DNSName ? p.DNSName.replace(/\.$/, '') : undefined,
              ip: p.TailscaleIPs?.[0] || '',
              os: p.OS || '',
              online: p.Online ?? true,
              tags: p.Tags || p.tags || [],
              Tags: p.Tags || p.tags || [],
            }));
            setTsPeers(peersList);
          }
        })
        .catch(() => {});
    }

    // Fetch vault variables for dropdown
    refreshVaultVariables();

    fetch('/api/vault/status')
      .then(res => res.json())
      .then(data => {
        if (data.success) {
          setVaultStatus({
            hasPasswordSource: Boolean(data.hasPasswordSource),
            source: data.source || 'ask_password',
          });
        }
      })
      .catch(() => {});
  }, [isOpen, peers]);

  // Matching peers for Follow Tag
  const matchingTagPeers = useMemo(() => {
    if (!followTag.trim()) return [];
    return tsPeers.filter(p => matchesTailscaleTag(p.Tags || p.tags, followTag));
  }, [tsPeers, followTag]);

  if (!isOpen) return null;

  const handleSelectPeer = (peerHostName: string) => {
    setSelectedPeer(peerHostName);
    const peer = tsPeers.find(p => p.hostName === peerHostName);
    if (peer) {
      setName(peer.hostName);
      if (peer.ip) {
        setTailscaleIp(peer.ip);
        if (!ip) setIp(peer.ip);
      }
      if (peer.dnsName) {
        setTailscaleHost(peer.dnsName);
      }
      if (peer.os?.toLowerCase().includes('windows')) {
        setNodeType('windows');
      } else {
        setNodeType('linux');
      }
    }
  };

  // Create a named Ansible Vault variable and return its NAME. The node/chunk
  // stores only this name; plaintext never reaches the node payload and the
  // raw ciphertext is never used as an identifier.
  const createVaultSecret = async (
    varName: string,
    plaintext: string,
    passwordOverride?: string
  ): Promise<string> => {
    const name = varName.trim();
    if (!name) {
      throw new Error('Secret name is required (e.g. ssh_key)');
    }
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(name)) {
      throw new Error('Secret name must match [A-Za-z_][A-Za-z0-9_]* (letters, digits, underscore)');
    }
    const resp = await fetch('/api/vault/variables', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        name,
        value: plaintext,
        password: passwordOverride || undefined,
      }),
    });
    const data = await resp.json().catch(() => ({}));
    if (!resp.ok || !data.success) {
      throw new Error(data.error || `Failed to create vault secret "${name}" (HTTP ${resp.status})`);
    }
    await refreshVaultVariables();
    return name;
  };

  // Resolve the credential reference for a submission: an existing vault
  // variable name, or a newly created one. Returns undefined when the operator
  // chose neither (caller decides whether that is acceptable).
  const ensureVaultSecret = async (opts: {
    mode: 'existing' | 'new';
    existingName: string;
    newName: string;
    value: string;
    password: string;
  }): Promise<string | undefined> => {
    if (opts.mode === 'existing') {
      return opts.existingName.trim() || undefined;
    }
    const value = opts.value.trim();
    const name = opts.newName.trim();
    if (!name && !value) return undefined;
    if (!name) throw new Error('Secret name is required (e.g. ssh_key)');
    if (!value) throw new Error(`Secret value is required for "${name}"`);
    return createVaultSecret(name, value, opts.password.trim() || undefined);
  };

  // Real connectivity check against the host using POST /api/ssh/test. The
  // response comes from the remote shell; nothing is simulated.
  const handleTestConnection = async () => {
    setConnectionResult(null);
    setVaultError(null);
    const host = (ip || tailscaleIp || tailscaleHost || name).trim();
    if (!host) {
      setConnectionResult({ ok: false, message: 'Enter a host address first' });
      return;
    }
    setTestingConnection(true);
    try {
      let vaultSecretId: string | undefined;
      if (!useSshAgent) {
        try {
          vaultSecretId = await ensureVaultSecret({
            mode: credentialMode,
            existingName: selectedVaultSecretId,
            newName: newSecretName,
            value: secretText,
            password: masterVaultPassword,
          });
        } catch (err: any) {
          setConnectionResult({ ok: false, message: err?.message || 'Credential resolution failed' });
          return;
        }
      }
      const resp = await fetch('/api/ssh/test', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          host,
          port: port || 22,
          sshUser: sshUser || 'core',
          vaultSecretId,
          authMethod: useSshAgent ? 'ssh-agent' : undefined,
        }),
      });
      const data = await resp.json().catch(() => ({}));
      if (resp.ok && data.success) {
        const identity = String(data.output || '')
          .split('\n')
          .map((line: string) => line.trim())
          .filter(Boolean)
          .join(', ');
        setConnectionResult({
          ok: true,
          message: `Connected${identity ? ` — ${identity}` : ''} (${data.latencyMs ?? 0} ms)`,
        });
      } else {
        setConnectionResult({
          ok: false,
          message: data.error || `SSH test failed (HTTP ${resp.status})`,
        });
      }
    } catch (err: any) {
      setConnectionResult({ ok: false, message: err?.message || 'SSH test failed' });
    } finally {
      setTestingConnection(false);
    }
  };

  // Submit Handler for Tailscale Mesh & Manual SSH & WinRM
  const handleFormSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!name.trim()) return;

    setVaultError(null);
    setProbeError(null);

    // WinRM onboarding is not implemented. Refuse to write a node that would
    // look managed while no WinRM transport exists.
    if (activeTab === 'winrm') {
      setVaultError('WinRM onboarding is not implemented yet. No node was created.');
      return;
    }

    let vaultSecretId: string | undefined = undefined;
    let effectiveAuthMethod = 'tailscale-ssh';
    let effectiveProtocol: 'ssh' | 'winrm' | 'tailscale_ssh' = 'tailscale_ssh';
    let effectivePort = port;
    let effectiveUser = sshUser;
    let effectiveNodeType: NodeType = nodeType;

    if (activeTab === 'tailscale') {
      effectiveProtocol = 'tailscale_ssh';
      effectiveAuthMethod = 'tailscale-ssh';
    } else if (activeTab === 'manual_ssh') {
      effectiveProtocol = 'ssh';
      effectivePort = port || 22;
      effectiveUser = sshUser || 'core';
      effectiveNodeType = 'linux';

      if (useSshAgent) {
        effectiveAuthMethod = 'ssh-agent';
      } else {
        try {
          vaultSecretId = await ensureVaultSecret({
            mode: credentialMode,
            existingName: selectedVaultSecretId,
            newName: newSecretName,
            value: secretText,
            password: masterVaultPassword,
          });
        } catch (err: any) {
          setVaultError(err?.message || 'Failed to save the vault secret');
          return;
        }
        if (!vaultSecretId) {
          setVaultError(
            'Select an existing vault secret, create a new one, or enable ssh-agent forwarding.'
          );
          return;
        }
        effectiveAuthMethod = secretType === 'password' ? 'vault-password' : 'vault-sshkey';
      }
    }

    const targetAddress = tailscaleIp || ip || name.trim();
    let hardware: ProbedHardware | null = probedData;

    // Optional hardware probe for Linux SSH / Tailscale
    if (activeTab === 'tailscale' || activeTab === 'manual_ssh') {
      setProbing(true);
      try {
        const resp = await fetch('/api/nodes/probe-host', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            host: targetAddress,
            port: effectivePort,
            sshUser: effectiveUser,
            authMethod: effectiveAuthMethod,
            vaultSecretId,
          }),
        });
        const data = await resp.json();
        if (data.success && data.hardware) {
          hardware = data.hardware;
          setProbedData(data.hardware);
        }
      } catch {
        // Non-blocking probe failure
      } finally {
        setProbing(false);
      }
    }

    // Zero-Mock: keep specs empty when the probe did not return real hardware.
    const nodeSpecs = hardware
      ? {
          cpu: hardware.cpu,
          vcpus: hardware.vcpus,
          ramGiB: hardware.ramGiB,
          motherboard: hardware.motherboard,
          bios: hardware.bios,
          ipmi: 'DHCP',
          ecc: 'Active',
          secureBoot: hardware.secureBoot || 'Enabled',
          iommu: hardware.iommu || 'Enabled',
        }
      : undefined;

    const osDesc = hardware?.osDescription || 'Pending probe';

    // Dynamic tailnet detection from existing nodes. Empty when unknown.
    const detectedTailnet = nodes.find(n => n.tailscaleHost)?.tailscaleHost?.split('.').slice(1).join('.') || '';

    onAddNode(
      buildProvisionedNode({
        name: name.trim(),
        group,
        datacenter: detectedTailnet,
        nodeType: effectiveNodeType,
        ip: ip.trim() || (activeTab === 'manual_ssh' && !tailscaleIp ? name.trim() : ''),
        port: effectivePort,
        sshUser: effectiveUser,
        authMethod: effectiveAuthMethod,
        connectionProtocol: effectiveProtocol,
        vaultSecretId,
        tailscaleIp,
        tailscaleHost,
        osDescription: osDesc,
        specs: nodeSpecs,
      })
    );

    onClose();
  };

  // Submit Handler for Follow Tag (Dynamic Inventory Chunk)
  const handleFollowTagSubmit = async () => {
    const tagInput = normalizeTag(followTag);
    if (!tagInput) {
      setTagError('Enter a tag name, e.g. pve or host');
      return;
    }

    setTagSubmitting(true);
    setTagError(null);

    let vaultSecretId: string | undefined = undefined;

    if (followAuthMethod === 'vault-password' || followAuthMethod === 'vault-sshkey') {
      try {
        vaultSecretId = await ensureVaultSecret({
          mode: followCredentialMode,
          existingName: selectedVaultSecretId,
          newName: followSecretName,
          value: followSecret,
          password: followMasterPassword,
        });
      } catch (err: any) {
        setTagError(err?.message || 'Failed to save the shared tag credential');
        setTagSubmitting(false);
        return;
      }
      if (!vaultSecretId) {
        setTagError('Select an existing vault secret or create a new one for this tag.');
        setTagSubmitting(false);
        return;
      }
    }

    try {
      const resp = await fetch('/api/inventory/chunks', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          tag: tagInput,
          authMethod: followAuthMethod,
          sshUser: followSshUser.trim() || undefined,
          vaultSecretId,
        }),
      });

      const data = await resp.json();
      if (!data.success) {
        throw new Error(data.error || 'Failed to follow tag inventory chunk');
      }

      if (onFollowTag) {
        onFollowTag({
          tag: tagInput,
          authMethod: followAuthMethod,
          sshUser: followSshUser.trim() || undefined,
          vaultSecretId,
        });
      }

      onClose();
    } catch (err: any) {
      setTagError(err?.message || 'Failed to stage tag inventory chunk');
    } finally {
      setTagSubmitting(false);
    }
  };

  return (
    <div
      style={{ display: isMinimized ? 'none' : 'flex' }}
      className="fixed inset-0 z-50 items-center justify-center bg-black/70 backdrop-blur-xs p-4"
    >
      <div className="w-full max-w-xl bg-[#0a0d1f] rounded-lg shadow-2xl border border-[#313447] overflow-hidden">
        {/* Modal Header */}
        <div className="h-10 bg-[#171a2d] px-3 flex items-center justify-between border-b border-[#26293c]">
          <div className="flex items-center gap-2">
            <span className="material-symbols-outlined text-[#8cd6cb] text-[18px]">add_circle</span>
            <span className="font-mono text-xs text-[#dfe1fa] font-bold">
              Add Node &amp; Inventory Onboarding
            </span>
          </div>
          <div className="flex items-center gap-1">
            {onMinimize && (
              <button
                type="button"
                onClick={onMinimize}
                className="text-[#8d909b] hover:text-[#dfe1fa] p-1 cursor-pointer transition-colors"
                title="Minimize"
              >
                <span className="material-symbols-outlined text-[16px]">remove</span>
              </button>
            )}
            <button
              type="button"
              onClick={onClose}
              className="text-[#8d909b] hover:text-[#dfe1fa] p-1 cursor-pointer transition-colors"
              title="Close"
            >
              <span className="material-symbols-outlined text-[16px]">close</span>
            </button>
          </div>
        </div>

        {/* 4 Multi-Path Tabs Navigation Bar */}
        <div className="grid grid-cols-4 bg-[#121528] border-b border-[#26293c] p-1 gap-1 font-mono text-[11px]">
          <button
            type="button"
            onClick={() => {
              setActiveTab('tailscale');
              setNodeType('linux');
              setPort(22);
            }}
            className={`py-1.5 px-2 rounded flex items-center justify-center gap-1.5 cursor-pointer transition-all ${
              activeTab === 'tailscale'
                ? 'bg-[#26293c] text-[#8cd6cb] font-bold border border-[#8cd6cb]/30 shadow-xs'
                : 'text-[#8d909b] hover:text-[#dfe1fa] hover:bg-[#1b1e31]'
            }`}
          >
            <span className="material-symbols-outlined text-[14px]">hub</span>
            <span>Tailscale Mesh</span>
          </button>

          <button
            type="button"
            onClick={() => {
              setActiveTab('manual_ssh');
              setNodeType('linux');
              setPort(22);
            }}
            className={`py-1.5 px-2 rounded flex items-center justify-center gap-1.5 cursor-pointer transition-all ${
              activeTab === 'manual_ssh'
                ? 'bg-[#26293c] text-[#b0c9ff] font-bold border border-[#b0c9ff]/30 shadow-xs'
                : 'text-[#8d909b] hover:text-[#dfe1fa] hover:bg-[#1b1e31]'
            }`}
          >
            <span className="material-symbols-outlined text-[14px]">terminal</span>
            <span>Manual SSH</span>
          </button>

          <button
            type="button"
            onClick={() => {
              setActiveTab('winrm');
              setNodeType('windows');
              setPort(winrmPort);
            }}
            className={`py-1.5 px-2 rounded flex items-center justify-center gap-1.5 cursor-pointer transition-all ${
              activeTab === 'winrm'
                ? 'bg-[#26293c] text-[#ffd580] font-bold border border-[#ffd580]/30 shadow-xs'
                : 'text-[#8d909b] hover:text-[#dfe1fa] hover:bg-[#1b1e31]'
            }`}
          >
            <span className="material-symbols-outlined text-[14px]">desktop_windows</span>
            <span>WinRM</span>
          </button>

          <button
            type="button"
            onClick={() => setActiveTab('follow_tag')}
            className={`py-1.5 px-2 rounded flex items-center justify-center gap-1.5 cursor-pointer transition-all ${
              activeTab === 'follow_tag'
                ? 'bg-[#26293c] text-[#d0bcff] font-bold border border-[#d0bcff]/30 shadow-xs'
                : 'text-[#8d909b] hover:text-[#dfe1fa] hover:bg-[#1b1e31]'
            }`}
          >
            <span className="material-symbols-outlined text-[14px]">sync</span>
            <span>Follow Tag</span>
          </button>
        </div>

        {/* Tab 1, 2, 3: Form Formats */}
        {activeTab !== 'follow_tag' ? (
          <form onSubmit={handleFormSubmit} className="p-4 space-y-3 font-mono text-xs">
            {/* TAB 1: TAILSCALE MESH */}
            {activeTab === 'tailscale' && (
              <div className="space-y-3">
                <div className="p-2.5 rounded bg-[#121528] border border-[#26293c] flex items-start gap-2">
                  <span className="material-symbols-outlined text-[#8cd6cb] text-[16px] shrink-0 mt-0.5">
                    verified_user
                  </span>
                  <div className="text-[11px] text-[#8d909b]">
                    <span className="text-[#dfe1fa] font-semibold">Keyless Tailscale SSH Active:</span>
                    <p className="mt-0.5 text-[10px]">
                      Peers onboarded via mesh utilize Tailscale SSH (Keyless / PAM). Authentication is cryptographically verified by the Tailscale daemon with zero host credentials stored.
                    </p>
                  </div>
                </div>

                {tsPeers.length > 0 && (
                  <div>
                    <label className="block text-[#8d909b] mb-1">SELECT UNMANAGED TAILSCALE PEER</label>
                    <select
                      value={selectedPeer}
                      onChange={e => handleSelectPeer(e.target.value)}
                      className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#8cd6cb] outline-none"
                    >
                      <option value="">-- Choose from mesh peers or type manually --</option>
                      {tsPeers.map(p => (
                        <option key={p.hostName} value={p.hostName}>
                          {p.hostName} ({p.ip || 'no-ip'}) {p.os ? `[${p.os}]` : ''}
                        </option>
                      ))}
                    </select>
                  </div>
                )}

                <div className="grid grid-cols-3 gap-2">
                  <div className="col-span-2">
                    <label className="block text-[#8d909b] mb-1">HOST NAME / ADDRESS</label>
                    <input
                      type="text"
                      required
                      value={name}
                      onChange={e => setName(e.target.value)}
                      placeholder="e.g. likes-it-cold"
                      className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#8cd6cb] outline-none"
                    />
                  </div>
                  <div>
                    <label className="block text-[#8d909b] mb-1">GROUP / TAG</label>
                    <select
                      value={group}
                      onChange={e => setGroup(e.target.value)}
                      className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#8cd6cb] outline-none"
                    >
                      <option value="tag:host">tag:host (Container Hosts)</option>
                      <option value="tag:pve">tag:pve (Proxmox Hypervisors)</option>
                      <option value="tag:service">tag:service (Mesh Services)</option>
                      <option value="tag:admin">tag:admin (Workstations)</option>
                      <option value="tag:worker">tag:worker (Workers)</option>
                    </select>
                  </div>
                </div>

                <div className="grid grid-cols-2 gap-2">
                  <div>
                    <label className="block text-[#8d909b] mb-1">LAN IP ADDRESS</label>
                    <input
                      type="text"
                      value={ip}
                      onChange={e => setIp(e.target.value)}
                      placeholder="192.168.1.180"
                      className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2 py-1.5 text-[#dfe1fa] outline-none"
                    />
                  </div>
                  <div>
                    <label className="block text-[#8d909b] mb-1">TAILSCALE MESH IP</label>
                    <input
                      type="text"
                      value={tailscaleIp}
                      onChange={e => setTailscaleIp(e.target.value)}
                      placeholder="100.105.71.49"
                      className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2 py-1.5 text-[#dfe1fa] outline-none"
                    />
                  </div>
                </div>
              </div>
            )}

            {/* TAB 2: MANUAL SSH */}
            {activeTab === 'manual_ssh' && (
              <div className="space-y-3">
                <div className="grid grid-cols-3 gap-2">
                  <div className="col-span-2">
                    <label className="block text-[#8d909b] mb-1">HOST NAME / ADDRESS</label>
                    <input
                      type="text"
                      required
                      value={name}
                      onChange={e => setName(e.target.value)}
                      placeholder="e.g. storage-san-01"
                      className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#b0c9ff] outline-none"
                    />
                  </div>
                  <div>
                    <label className="block text-[#8d909b] mb-1">SSH PORT</label>
                    <input
                      type="number"
                      value={port}
                      onChange={e => setPort(Number(e.target.value))}
                      className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#b0c9ff] outline-none"
                    />
                  </div>
                </div>

                <div className="grid grid-cols-2 gap-2">
                  <div>
                    <label className="block text-[#8d909b] mb-1">SSH USER</label>
                    <input
                      type="text"
                      value={sshUser}
                      onChange={e => setSshUser(e.target.value)}
                      placeholder="core"
                      className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#8cd6cb] font-bold focus:border-[#b0c9ff] outline-none"
                    />
                  </div>
                  <div>
                    <label className="block text-[#8d909b] mb-1">GROUP / TAG</label>
                    <select
                      value={group}
                      onChange={e => setGroup(e.target.value)}
                      className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#b0c9ff] outline-none"
                    >
                      <option value="tag:host">tag:host</option>
                      <option value="tag:pve">tag:pve</option>
                      <option value="tag:service">tag:service</option>
                      <option value="tag:worker">tag:worker</option>
                    </select>
                  </div>
                </div>

                <div className="pt-1">
                  <label className="flex items-center gap-2 cursor-pointer text-[#dfe1fa] text-[11px]">
                    <input
                      type="checkbox"
                      checked={useSshAgent}
                      onChange={e => setUseSshAgent(e.target.checked)}
                      className="rounded border-[#26293c] bg-[#1b1e31]"
                    />
                    <span>
                      Use ssh-agent forwarding <span className="text-[#ffd580] font-bold">(Local testing only)</span>
                    </span>
                  </label>
                </div>

                {!useSshAgent && (
                  <div className="p-3 rounded bg-[#121528] border border-[#26293c] space-y-2.5">
                    <div className="flex items-center justify-between">
                      <div className="flex items-center gap-1.5">
                        <span className="material-symbols-outlined text-[#8cd6cb] text-[16px]">lock</span>
                        <span className="text-[#dfe1fa] font-bold text-[11px]">SSH Vault Secrets</span>
                      </div>
                      <div className="flex gap-2">
                        <label className="flex items-center gap-1 cursor-pointer text-[10px] text-[#c3c6d2]">
                          <input
                            type="radio"
                            name="credentialMode"
                            checked={credentialMode === 'existing'}
                            onChange={() => setCredentialMode('existing')}
                          />
                          <span>Existing secret</span>
                        </label>
                        <label className="flex items-center gap-1 cursor-pointer text-[10px] text-[#c3c6d2]">
                          <input
                            type="radio"
                            name="credentialMode"
                            checked={credentialMode === 'new'}
                            onChange={() => setCredentialMode('new')}
                          />
                          <span>New secret</span>
                        </label>
                      </div>
                    </div>

                    {credentialMode === 'existing' ? (
                      <div>
                        <label className="block text-[#8d909b] text-[10px] mb-1">
                          SELECT AN EXISTING VAULT VARIABLE
                        </label>
                        <select
                          value={selectedVaultSecretId}
                          onChange={e => setSelectedVaultSecretId(e.target.value)}
                          className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#b0c9ff] outline-none"
                        >
                          <option value="">
                            {vaultVariables.length === 0
                              ? '-- No vault variables yet: switch to New secret --'
                              : '-- Choose a vault variable --'}
                          </option>
                          {vaultVariables.map(v => (
                            <option key={v.name} value={v.name}>
                              {v.name} {v.maskedValue ? `(${v.maskedValue})` : ''}
                            </option>
                          ))}
                        </select>
                        <p className="text-[9px] text-[#8d909b] mt-1">
                          Only the variable name is stored on the node. The value is decrypted on the
                          coordinator at connection time.
                        </p>
                      </div>
                    ) : (
                      <div className="space-y-2">
                        <div>
                          <label className="block text-[#8d909b] text-[10px] mb-1">
                            SECRET NAME (E.G. ssh_key)
                          </label>
                          <input
                            type="text"
                            value={newSecretName}
                            onChange={e => setNewSecretName(e.target.value)}
                            placeholder="ssh_key"
                            className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#8cd6cb] font-bold focus:border-[#b0c9ff] outline-none"
                          />
                          <p className="text-[9px] text-[#8d909b] mt-1">
                            Letters, digits and underscores. SSH keys are secrets too — this name is
                            yours to choose.
                          </p>
                        </div>

                        <div className="flex items-center justify-between">
                          <label className="block text-[#8d909b] text-[10px]">SECRET VALUE</label>
                          <div className="flex gap-2">
                            <label className="flex items-center gap-1 cursor-pointer text-[10px] text-[#c3c6d2]">
                              <input
                                type="radio"
                                name="secretType"
                                checked={secretType === 'password'}
                                onChange={() => setSecretType('password')}
                              />
                              <span>Password</span>
                            </label>
                            <label className="flex items-center gap-1 cursor-pointer text-[10px] text-[#c3c6d2]">
                              <input
                                type="radio"
                                name="secretType"
                                checked={secretType === 'sshkey'}
                                onChange={() => setSecretType('sshkey')}
                              />
                              <span>Private SSH Key</span>
                            </label>
                          </div>
                        </div>
                        {secretType === 'password' ? (
                          <input
                            type="password"
                            value={secretText}
                            onChange={e => setSecretText(e.target.value)}
                            placeholder="Host SSH password..."
                            className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#b0c9ff] outline-none"
                          />
                        ) : (
                          <textarea
                            rows={3}
                            value={secretText}
                            onChange={e => setSecretText(e.target.value)}
                            placeholder="-----BEGIN OPENSSH PRIVATE KEY-----..."
                            className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#b0c9ff] outline-none font-mono text-[10px]"
                          />
                        )}
                      </div>
                    )}

                    {/* Master Vault Password when ask_password is required */}
                    {vaultStatus?.source === 'ask_password' && credentialMode === 'new' && (
                      <div>
                        <label className="block text-[#ffd580] text-[10px] mb-1">
                          MASTER VAULT PASSWORD (REQUIRED FOR ENCRYPTION)
                        </label>
                        <input
                          type="password"
                          value={masterVaultPassword}
                          onChange={e => setMasterVaultPassword(e.target.value)}
                          placeholder="Enter master vault password..."
                          className="w-full bg-[#1b1e31] border border-[#ffd580]/40 rounded px-2.5 py-1.5 text-[#dfe1fa] outline-none"
                        />
                      </div>
                    )}

                    <div className="text-[10px] text-[#8d909b]">
                      Credentials are encrypted with <code className="text-[#8cd6cb]">ansible-vault encrypt_string</code> on
                      the coordinator. Plaintext is never written to SQLite or sent to the node.
                    </div>
                  </div>
                )}

                <div className="flex items-center gap-2">
                  <button
                    type="button"
                    onClick={handleTestConnection}
                    disabled={testingConnection || !name.trim()}
                    className="px-3 py-1.5 bg-[#1b1e31] hover:bg-[#26293c] text-[#8cd6cb] border border-[#8cd6cb]/30 rounded cursor-pointer flex items-center gap-1 disabled:opacity-50"
                  >
                    <span className={`material-symbols-outlined text-[15px] ${testingConnection ? 'animate-spin' : ''}`}>
                      {testingConnection ? 'sync' : 'network_check'}
                    </span>
                    <span>{testingConnection ? 'Testing...' : 'Test Connection'}</span>
                  </button>
                  {connectionResult && (
                    <span
                      className={`text-[10px] font-mono truncate ${
                        connectionResult.ok ? 'text-[#8cd6cb]' : 'text-[#ffb4ab]'
                      }`}
                      title={connectionResult.message}
                    >
                      {connectionResult.message}
                    </span>
                  )}
                </div>
              </div>
            )}

            {/* TAB 3: WINRM */}
            {activeTab === 'winrm' && (
              <div className="space-y-3">
                <div className="grid grid-cols-3 gap-2">
                  <div className="col-span-2">
                    <label className="block text-[#8d909b] mb-1">HOST NAME / ADDRESS</label>
                    <input
                      type="text"
                      required
                      value={name}
                      onChange={e => setName(e.target.value)}
                      placeholder="e.g. win-server-01"
                      className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#ffd580] outline-none"
                    />
                  </div>
                  <div>
                    <label className="block text-[#8d909b] mb-1">WINRM PORT</label>
                    <input
                      type="number"
                      value={winrmPort}
                      onChange={e => setWinrmPort(Number(e.target.value))}
                      className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#ffd580] outline-none"
                    />
                  </div>
                </div>

                <div className="flex items-center gap-4 text-[11px] text-[#dfe1fa]">
                  <label className="flex items-center gap-1.5 cursor-pointer">
                    <input
                      type="checkbox"
                      checked={winrmHttps}
                      onChange={e => {
                        const checked = e.target.checked;
                        setWinrmHttps(checked);
                        setWinrmPort(checked ? 5986 : 5985);
                      }}
                      className="rounded border-[#26293c] bg-[#1b1e31]"
                    />
                    <span>Use HTTPS (Port 5986)</span>
                  </label>
                </div>

                <div className="grid grid-cols-3 gap-2">
                  <div>
                    <label className="block text-[#8d909b] mb-1">WINRM USER</label>
                    <input
                      type="text"
                      value={winrmUser}
                      onChange={e => setWinrmUser(e.target.value)}
                      placeholder="Administrator"
                      className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#ffd580] font-bold focus:border-[#ffd580] outline-none"
                    />
                  </div>
                  <div>
                    <label className="block text-[#8d909b] mb-1">TRANSPORT</label>
                    <select
                      value={winrmTransport}
                      onChange={e => setWinrmTransport(e.target.value as any)}
                      className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#ffd580] outline-none"
                    >
                      <option value="ntlm">NTLM (Standard / Default)</option>
                      <option value="basic">Basic (Testing / Insecure)</option>
                      <option value="kerberos">Kerberos (Active Directory)</option>
                    </select>
                  </div>
                  <div>
                    <label className="block text-[#8d909b] mb-1">GROUP / TAG</label>
                    <select
                      value={group}
                      onChange={e => setGroup(e.target.value)}
                      className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#ffd580] outline-none"
                    >
                      <option value="tag:host">tag:host (Container Hosts)</option>
                      <option value="tag:pve">tag:pve (Proxmox Hypervisors)</option>
                      <option value="tag:service">tag:service (Mesh Services)</option>
                      <option value="tag:admin">tag:admin (Workstations)</option>
                    </select>
                  </div>
                </div>

                <div className="p-3 rounded bg-[#2d281b] border border-[#ffd580]/40 space-y-1.5">
                  <div className="flex items-center gap-1.5">
                    <span className="material-symbols-outlined text-[#ffd580] text-[16px]">construction</span>
                    <span className="text-[#ffd580] font-bold text-[11px]">WinRM onboarding is not implemented</span>
                  </div>
                  <p className="text-[10px] text-[#c3c6d2]">
                    No WinRM transport exists in the daemon yet, so this form cannot verify or save a
                    Windows host. Add Node is disabled for this tab to avoid recording metadata for a
                    host that is not actually managed.
                  </p>
                </div>
              </div>
            )}

            {/* Error notifications */}
            {vaultError && (
              <div className="flex items-center gap-2 text-[#ffb4ab] bg-[#2d161a] p-2 rounded border border-[#ffb4ab]/30 text-[11px]">
                <span className="material-symbols-outlined text-[16px] shrink-0">error</span>
                <span>{vaultError}</span>
              </div>
            )}

            {probeError && (
              <div className="flex items-center gap-2 text-[#ffb4ab] bg-[#2d161a] p-2 rounded border border-[#ffb4ab]/30 text-[11px]">
                <span className="material-symbols-outlined text-[16px] shrink-0">warning</span>
                <span>{probeError}</span>
              </div>
            )}

            {probing && (
              <div className="flex items-center gap-2 text-[#8cd6cb] bg-[#121528] p-2 rounded border border-[#8cd6cb]/30">
                <span className="material-symbols-outlined text-[16px] animate-spin">sync</span>
                <span>Probing host hardware...</span>
              </div>
            )}

            {/* Action Buttons */}
            <div className="pt-2 flex items-center justify-end gap-2 border-t border-[#26293c]">
              <button
                type="button"
                onClick={onClose}
                disabled={probing}
                className="px-3 py-1.5 bg-[#1b1e31] hover:bg-[#26293c] text-[#c3c6d2] rounded cursor-pointer disabled:opacity-50"
              >
                Cancel
              </button>
              <button
                type="submit"
                disabled={probing || !name.trim() || activeTab === 'winrm'}
                title={activeTab === 'winrm' ? 'WinRM onboarding is not implemented' : undefined}
                className="px-3 py-1.5 bg-[#b0c9ff] hover:bg-[#d8e2ff] text-[#002e68] font-bold rounded cursor-pointer flex items-center gap-1 shadow-sm disabled:opacity-50 disabled:cursor-not-allowed"
              >
                <span className="material-symbols-outlined text-[15px]">
                  {probing ? 'sync' : 'add'}
                </span>
                <span>{probing ? 'Probing...' : 'Add Node'}</span>
              </button>
            </div>
          </form>
        ) : (
          /* TAB 4: FOLLOW TAG (DYNAMIC INVENTORY CHUNK) */
          <div className="p-4 space-y-3 font-mono text-xs">
            <div className="p-2.5 rounded bg-[#121528] border border-[#26293c] flex items-start gap-2">
              <span className="material-symbols-outlined text-[#d0bcff] text-[16px] shrink-0 mt-0.5">
                dynamic_feed
              </span>
              <div className="text-[11px] text-[#8d909b]">
                <span className="text-[#dfe1fa] font-semibold">Dynamic Inventory Chunk:</span>
                <p className="mt-0.5 text-[10px]">
                  Following a Tailscale tag automatically tracks, creates, and reconciles all current and future mesh peers assigned to this tag.
                </p>
              </div>
            </div>

            <div>
              <label className="block text-[#8d909b] mb-1">TAG NAME</label>
              <div className="flex items-center gap-1">
                <span className="px-2 py-1.5 rounded bg-[#121528] border border-[#26293c] text-[#8d909b]">
                  tag:
                </span>
                <input
                  type="text"
                  value={followTag}
                  onChange={e => setFollowTag(e.target.value)}
                  placeholder="pve"
                  className="flex-1 bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#d0bcff] outline-none"
                />
              </div>
              <p className="text-[9px] text-[#8d909b] mt-1">
                Enter the plain tag name (e.g. <code className="text-[#d0bcff]">pve</code> or{' '}
                <code className="text-[#d0bcff]">host</code>). The <code>tag:</code> prefix is added
                automatically.
              </p>
            </div>

            {/* Live Matching Peers Preview */}
            <div className="p-2.5 rounded bg-[#171a2d] border border-[#26293c] flex items-center justify-between text-[11px]">
              <span className="text-[#8d909b]">
                Matching Peers:{' '}
                <strong className="text-[#8cd6cb]">
                  {matchingTagPeers.length} online peer{matchingTagPeers.length === 1 ? '' : 's'}
                </strong>{' '}
                in mesh
              </span>
              {matchingTagPeers.length > 0 && (
                <span className="text-[10px] text-[#c3c6d2] max-w-[220px] truncate">
                  ({matchingTagPeers.map(p => p.hostName).join(', ')})
                </span>
              )}
            </div>

            <div className="grid grid-cols-2 gap-2">
              <div>
                <label className="block text-[#8d909b] mb-1">SHARED SSH USER</label>
                <input
                  type="text"
                  value={followSshUser}
                  onChange={e => setFollowSshUser(e.target.value)}
                  placeholder="core"
                  className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#d0bcff] outline-none"
                />
              </div>

              <div>
                <label className="block text-[#8d909b] mb-1">SHARED AUTH METHOD</label>
                <select
                  value={followAuthMethod}
                  onChange={e => setFollowAuthMethod(e.target.value as any)}
                  className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#d0bcff] outline-none"
                >
                  <option value="tailscale-ssh">Tailscale SSH (Keyless / PAM)</option>
                  <option value="vault-password">Shared Password (Ansible Vault)</option>
                  <option value="vault-sshkey">Shared SSH Key (Ansible Vault)</option>
                  <option value="ssh-agent">ssh-agent forwarding</option>
                </select>
              </div>
            </div>

            {(followAuthMethod === 'vault-password' || followAuthMethod === 'vault-sshkey') && (
              <div className="p-3 rounded bg-[#121528] border border-[#26293c] space-y-2">
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-1.5">
                    <span className="material-symbols-outlined text-[#8cd6cb] text-[16px]">lock</span>
                    <span className="text-[#dfe1fa] font-bold text-[11px]">Shared SSH Vault Secret</span>
                  </div>
                  <div className="flex gap-2">
                    <label className="flex items-center gap-1 cursor-pointer text-[10px] text-[#c3c6d2]">
                      <input
                        type="radio"
                        name="followCredentialMode"
                        checked={followCredentialMode === 'existing'}
                        onChange={() => setFollowCredentialMode('existing')}
                      />
                      <span>Existing secret</span>
                    </label>
                    <label className="flex items-center gap-1 cursor-pointer text-[10px] text-[#c3c6d2]">
                      <input
                        type="radio"
                        name="followCredentialMode"
                        checked={followCredentialMode === 'new'}
                        onChange={() => setFollowCredentialMode('new')}
                      />
                      <span>New secret</span>
                    </label>
                  </div>
                </div>

                {followCredentialMode === 'existing' ? (
                  <div>
                    <label className="block text-[#8d909b] text-[10px] mb-1">
                      SELECT AN EXISTING VAULT VARIABLE
                    </label>
                    <select
                      value={selectedVaultSecretId}
                      onChange={e => setSelectedVaultSecretId(e.target.value)}
                      className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#d0bcff] outline-none"
                    >
                      <option value="">
                        {vaultVariables.length === 0
                          ? '-- No vault variables yet: switch to New secret --'
                          : '-- Choose a vault variable --'}
                      </option>
                      {vaultVariables.map(v => (
                        <option key={v.name} value={v.name}>
                          {v.name} {v.maskedValue ? `(${v.maskedValue})` : ''}
                        </option>
                      ))}
                    </select>
                    <p className="text-[9px] text-[#8d909b] mt-1">
                      The same variable can back both manual hosts and followed tags.
                    </p>
                  </div>
                ) : (
                  <div className="space-y-2">
                    <div>
                      <label className="block text-[#8d909b] text-[10px] mb-1">
                        SECRET NAME (E.G. ssh_key)
                      </label>
                      <input
                        type="text"
                        value={followSecretName}
                        onChange={e => setFollowSecretName(e.target.value)}
                        placeholder="ssh_key"
                        className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#8cd6cb] font-bold focus:border-[#d0bcff] outline-none"
                      />
                    </div>
                    <div>
                      <label className="block text-[#8d909b] text-[10px] mb-1">
                        {followAuthMethod === 'vault-password'
                          ? 'SHARED PASSWORD (ENCRYPTED BEFORE SAVING)'
                          : 'SHARED PRIVATE SSH KEY (ENCRYPTED BEFORE SAVING)'}
                      </label>
                      {followAuthMethod === 'vault-password' ? (
                        <input
                          type="password"
                          value={followSecret}
                          onChange={e => setFollowSecret(e.target.value)}
                          placeholder="Shared password for all tag hosts..."
                          className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#d0bcff] outline-none"
                        />
                      ) : (
                        <textarea
                          rows={3}
                          value={followSecret}
                          onChange={e => setFollowSecret(e.target.value)}
                          placeholder="-----BEGIN OPENSSH PRIVATE KEY-----..."
                          className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] focus:border-[#d0bcff] outline-none font-mono text-[10px]"
                        />
                      )}
                    </div>
                  </div>
                )}

                {vaultStatus?.source === 'ask_password' && followCredentialMode === 'new' && (
                  <div>
                    <label className="block text-[#ffd580] text-[10px] mb-1">
                      MASTER VAULT PASSWORD (REQUIRED FOR ENCRYPTION)
                    </label>
                    <input
                      type="password"
                      value={followMasterPassword}
                      onChange={e => setFollowMasterPassword(e.target.value)}
                      placeholder="Enter master vault password..."
                      className="w-full bg-[#1b1e31] border border-[#ffd580]/40 rounded px-2.5 py-1.5 text-[#dfe1fa] outline-none"
                    />
                  </div>
                )}
              </div>
            )}

            {tagError && (
              <div className="flex items-center gap-2 text-[#ffb4ab] bg-[#2d161a] p-2 rounded border border-[#ffb4ab]/30 text-[11px]">
                <span className="material-symbols-outlined text-[16px] shrink-0">error</span>
                <span>{tagError}</span>
              </div>
            )}

            <div className="pt-2 flex items-center justify-end gap-2 border-t border-[#26293c]">
              <button
                type="button"
                onClick={onClose}
                disabled={tagSubmitting}
                className="px-3 py-1.5 bg-[#1b1e31] hover:bg-[#26293c] text-[#c3c6d2] rounded cursor-pointer disabled:opacity-50"
              >
                Cancel
              </button>
              <button
                type="button"
                onClick={handleFollowTagSubmit}
                disabled={tagSubmitting || !followTag.trim()}
                className="px-3 py-1.5 bg-[#d0bcff] hover:bg-[#e8ddff] text-[#381e72] font-bold rounded cursor-pointer flex items-center gap-1 shadow-sm disabled:opacity-50"
              >
                <span className="material-symbols-outlined text-[15px]">
                  {tagSubmitting ? 'sync' : 'playlist_add_check'}
                </span>
                <span>{tagSubmitting ? 'Syncing...' : 'Follow Tag & Sync Nodes'}</span>
              </button>
            </div>
          </div>
        )}
      </div>
    </div>
  );
};
