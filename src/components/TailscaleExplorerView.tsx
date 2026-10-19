import React, { useState, useEffect } from 'react';
import {
  Shield,
  Wifi,
  Radio,
  ArrowDownUp,
  Search,
  ExternalLink,
  RefreshCw,
  Copy,
  Check,
  Zap,
  Globe,
  Clock,
  Key,
} from 'lucide-react';
import { formatBytes } from '../utils/orchestrator.ts';

interface PeerData {
  ID?: string;
  HostName: string;
  DNSName: string;
  TailscaleIPs: string[];
  Online: boolean;
  OS: string;
  Tags?: string[];
  Active?: boolean;
  Relay?: string;
  isDirect?: boolean;
  CurAddr?: string;
  AllowedIPs?: string[];
  RxBytes?: number;
  TxBytes?: number;
  LastHandshake?: string;
  LastSeen?: string;
  KeyExpiry?: string;
  ExitNode?: boolean;
  ExitNodeOption?: boolean;
}

interface TailscaleExplorerViewProps {
  onOpenTerminalForHost?: (hostIp: string) => void;
}

export const TailscaleExplorerView: React.FC<TailscaleExplorerViewProps> = ({
  onOpenTerminalForHost,
}) => {
  const [peers, setPeers] = useState<PeerData[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [search, setSearch] = useState('');
  const [filterTag, setFilterTag] = useState<string>('all');
  const [pingTarget, setPingTarget] = useState<string | null>(null);
  const [pingResult, setPingResult] = useState<{ target: string; output: string; rtt?: string } | null>(null);
  const [pinging, setPinging] = useState(false);
  const [copiedIp, setCopiedIp] = useState<string | null>(null);

  const fetchPeers = async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await fetch('/api/tailscale/peers');
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data = await res.json();
      if (data.peers) {
        setPeers(data.peers);
      }
    } catch (err: any) {
      setError(err?.message || 'Failed to query live Tailscale peers');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchPeers();
  }, []);

  const handlePing = async (targetIpOrHost: string) => {
    setPinging(true);
    setPingTarget(targetIpOrHost);
    setPingResult(null);
    try {
      const res = await fetch('/api/tailscale/ping', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ target: targetIpOrHost }),
      });
      const data = await res.json();
      setPingResult({
        target: targetIpOrHost,
        output: data.output || data.error || 'No output',
        rtt: data.rtt,
      });
    } catch (err: any) {
      setPingResult({
        target: targetIpOrHost,
        output: err?.message || 'Ping failed',
      });
    } finally {
      setPinging(false);
    }
  };

  const handleCopyIp = (ip: string) => {
    navigator.clipboard.writeText(ip);
    setCopiedIp(ip);
    setTimeout(() => setCopiedIp(null), 2000);
  };

  // Filter peers
  const filteredPeers = peers.filter((p) => {
    const q = search.toLowerCase();
    const matchesSearch =
      !q ||
      p.HostName?.toLowerCase().includes(q) ||
      p.DNSName?.toLowerCase().includes(q) ||
      p.TailscaleIPs?.some((ip) => ip.includes(q)) ||
      p.Tags?.some((t) => t.toLowerCase().includes(q));

    if (!matchesSearch) return false;

    if (filterTag === 'all') return true;
    if (filterTag === 'online') return p.Online;
    if (filterTag === 'direct') return p.isDirect;
    if (filterTag === 'derp') return !p.isDirect && p.Relay;
    if (filterTag === 'exit-nodes') return p.ExitNode || p.ExitNodeOption;
    return p.Tags?.some((t) => t.includes(filterTag));
  });

  const totalPeers = peers.length;
  const onlineCount = peers.filter((p) => p.Online).length;
  const directCount = peers.filter((p) => p.isDirect).length;
  const derpCount = peers.filter((p) => !p.isDirect && p.Relay).length;

  return (
    <div className="space-y-4 p-4 text-[#c9d1d9] font-sans">
      {/* Top Header & Metrics Bar */}
      <div className="flex flex-col md:flex-row md:items-center justify-between gap-3 border-b border-[#30363d] pb-3">
        <div>
          <h2 className="text-lg font-bold text-white flex items-center gap-2">
            <Shield className="w-5 h-5 text-[#58a6ff]" />
            Tailscale Mesh Explorer (leo-banjo.ts.net)
          </h2>
          <p className="text-xs text-gray-400 font-mono mt-0.5">
            Full mesh telemetry & WireGuard endpoints across all cluster nodes
          </p>
        </div>

        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={fetchPeers}
            disabled={loading}
            className="flex items-center gap-1.5 px-3 py-1.5 bg-[#21262d] hover:bg-[#30363d] text-gray-200 rounded text-xs font-mono border border-[#30363d] transition-colors"
          >
            <RefreshCw className={`w-3.5 h-3.5 ${loading ? 'animate-spin text-[#58a6ff]' : ''}`} />
            Refresh Mesh
          </button>
        </div>
      </div>

      {/* KPI Cards */}
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-3 font-mono">
        <div className="bg-[#161b22] border border-[#30363d] p-3 rounded-lg">
          <span className="text-[11px] text-gray-400 block uppercase">Total Peers</span>
          <div className="flex items-baseline gap-2 mt-1">
            <span className="text-2xl font-bold text-white">{totalPeers}</span>
            <span className="text-xs text-[#3fb950]">{onlineCount} online</span>
          </div>
        </div>

        <div className="bg-[#161b22] border border-[#30363d] p-3 rounded-lg">
          <span className="text-[11px] text-gray-400 block uppercase">Direct WireGuard</span>
          <div className="flex items-baseline gap-2 mt-1">
            <span className="text-2xl font-bold text-[#3fb950]">{directCount}</span>
            <span className="text-xs text-gray-400">P2P UDP</span>
          </div>
        </div>

        <div className="bg-[#161b22] border border-[#30363d] p-3 rounded-lg">
          <span className="text-[11px] text-gray-400 block uppercase">DERP Relayed</span>
          <div className="flex items-baseline gap-2 mt-1">
            <span className="text-2xl font-bold text-amber-400">{derpCount}</span>
            <span className="text-xs text-gray-400">Encrypted relay</span>
          </div>
        </div>

        <div className="bg-[#161b22] border border-[#30363d] p-3 rounded-lg">
          <span className="text-[11px] text-gray-400 block uppercase">Overlay Cipher</span>
          <div className="flex items-baseline gap-2 mt-1">
            <span className="text-lg font-bold text-[#58a6ff]">ChaCha20</span>
            <span className="text-xs text-gray-400">Poly1305</span>
          </div>
        </div>
      </div>

      {/* Search & Tag Filter Pills */}
      <div className="flex flex-col sm:flex-row items-stretch sm:items-center justify-between gap-2 bg-[#161b22] p-2 rounded-lg border border-[#30363d]">
        <div className="relative flex-1 max-w-md">
          <Search className="w-4 h-4 absolute left-2.5 top-2.5 text-gray-400" />
          <input
            type="text"
            placeholder="Search by hostname, IP, tag, or DNS..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="w-full bg-[#0d1117] border border-[#30363d] rounded pl-8 pr-3 py-1.5 text-xs font-mono text-gray-200 placeholder-gray-500 focus:outline-none focus:border-[#58a6ff]"
          />
        </div>

        <div className="flex items-center gap-1.5 overflow-x-auto text-xs font-mono">
          {[
            { id: 'all', label: 'All Peers' },
            { id: 'online', label: 'Online' },
            { id: 'direct', label: 'Direct UDP' },
            { id: 'derp', label: 'DERP' },
            { id: 'host', label: 'tag:host' },
            { id: 'exit-nodes', label: 'Exit Nodes' },
          ].map((pill) => (
            <button
              key={pill.id}
              type="button"
              onClick={() => setFilterTag(pill.id)}
              className={`px-2.5 py-1 rounded text-xs transition-colors whitespace-nowrap border ${
                filterTag === pill.id
                  ? 'bg-[#1f6feb]/20 border-[#58a6ff] text-white font-semibold'
                  : 'bg-[#0d1117] border-[#30363d] text-gray-400 hover:text-gray-200 hover:border-gray-500'
              }`}
            >
              {pill.label}
            </button>
          ))}
        </div>
      </div>

      {/* Ping Results Panel (if active) */}
      {pingResult && (
        <div className="bg-[#161b22] border border-[#388bfd]/50 rounded-lg p-3 font-mono text-xs">
          <div className="flex items-center justify-between border-b border-[#30363d] pb-2 mb-2">
            <span className="text-[#58a6ff] font-semibold flex items-center gap-1.5">
              <Zap className="w-3.5 h-3.5 text-amber-400" />
              Tailscale Ping Result: {pingResult.target}
            </span>
            {pingResult.rtt && (
              <span className="px-2 py-0.5 rounded bg-[#3fb950]/20 text-[#3fb950] border border-[#3fb950]/40 text-[11px]">
                RTT: {pingResult.rtt}
              </span>
            )}
            <button
              type="button"
              onClick={() => setPingResult(null)}
              className="text-gray-400 hover:text-white"
            >
              ✕
            </button>
          </div>
          <pre className="text-gray-300 whitespace-pre-wrap overflow-x-auto bg-[#0d1117] p-2 rounded">
            {pingResult.output}
          </pre>
        </div>
      )}

      {/* Peers List / Grid */}
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3">
        {filteredPeers.map((peer) => {
          const isDirect = peer.isDirect;
          const primaryIp = peer.TailscaleIPs?.[0] || '';
          const cleanName = peer.HostName || (peer.DNSName ? peer.DNSName.split('.')[0] : 'node');
          const isExitNode = peer.ExitNode || peer.ExitNodeOption;

          return (
            <div
              key={peer.DNSName || primaryIp}
              className="bg-[#161b22] border border-[#30363d] rounded-lg p-3.5 flex flex-col justify-between hover:border-gray-500 transition-all shadow-sm"
            >
              <div>
                {/* Node Title & Status */}
                <div className="flex items-start justify-between gap-2">
                  <div className="flex items-center gap-2">
                    <span
                      className={`w-2.5 h-2.5 rounded-full ${
                        peer.Online ? 'bg-[#3fb950] shadow-sm shadow-[#3fb950]/50' : 'bg-gray-500'
                      }`}
                      title={peer.Online ? 'Peer Online' : 'Peer Offline'}
                    />
                    <h3 className="font-mono font-bold text-white text-sm truncate max-w-[170px]">
                      {cleanName}
                    </h3>
                  </div>

                  <div className="flex items-center gap-1">
                    {/* Connection type badge */}
                    {isDirect ? (
                      <span className="px-1.5 py-0.5 rounded bg-[#238636]/20 border border-[#238636]/50 text-[#3fb950] text-[10px] font-mono flex items-center gap-1">
                        <Wifi className="w-3 h-3" /> Direct
                      </span>
                    ) : (
                      <span className="px-1.5 py-0.5 rounded bg-amber-500/20 border border-amber-500/50 text-amber-400 text-[10px] font-mono flex items-center gap-1">
                        <Radio className="w-3 h-3" /> {peer.Relay || 'DERP'}
                      </span>
                    )}

                    {isExitNode && (
                      <span className="px-1.5 py-0.5 rounded bg-purple-500/20 border border-purple-500/50 text-purple-400 text-[10px] font-mono">
                        Exit
                      </span>
                    )}
                  </div>
                </div>

                {/* DNS Name & IP */}
                <div className="mt-2 text-xs font-mono text-gray-400 space-y-1">
                  <div className="flex items-center justify-between">
                    <span className="text-gray-500">IP:</span>
                    <div className="flex items-center gap-1 text-gray-200">
                      <span>{primaryIp}</span>
                      <button
                        type="button"
                        onClick={() => handleCopyIp(primaryIp)}
                        className="p-0.5 text-gray-400 hover:text-white"
                        title="Copy IP"
                      >
                        {copiedIp === primaryIp ? (
                          <Check className="w-3 h-3 text-[#3fb950]" />
                        ) : (
                          <Copy className="w-3 h-3" />
                        )}
                      </button>
                    </div>
                  </div>

                  <div className="flex items-center justify-between text-[11px]">
                    <span className="text-gray-500">FQDN:</span>
                    <span className="text-gray-300 truncate max-w-[180px]">{peer.DNSName}</span>
                  </div>

                  {peer.CurAddr && (
                    <div className="flex items-center justify-between text-[11px]">
                      <span className="text-gray-500">Endpoint:</span>
                      <span className="text-[#79c0ff] truncate max-w-[180px]">{peer.CurAddr}</span>
                    </div>
                  )}

                  {/* Transfer stats */}
                  {(peer.RxBytes !== undefined || peer.TxBytes !== undefined) && (
                    <div className="flex items-center justify-between text-[11px] pt-1 border-t border-[#30363d]/50">
                      <span className="text-gray-500 flex items-center gap-1">
                        <ArrowDownUp className="w-3 h-3 text-[#58a6ff]" /> I/O:
                      </span>
                      <span className="text-gray-300">
                        ↓ {formatBytes(peer.RxBytes || 0)} | ↑ {formatBytes(peer.TxBytes || 0)}
                      </span>
                    </div>
                  )}

                  {/* Subnet Routes */}
                  {peer.AllowedIPs && peer.AllowedIPs.length > 1 && (
                    <div className="text-[10px] text-gray-500 truncate pt-1">
                      Routes: {peer.AllowedIPs.filter((ip) => ip !== primaryIp).join(', ')}
                    </div>
                  )}

                  {/* Tags */}
                  {peer.Tags && peer.Tags.length > 0 && (
                    <div className="flex flex-wrap gap-1 pt-1.5">
                      {peer.Tags.map((tag) => (
                        <span
                          key={tag}
                          className="px-1.5 py-0.5 bg-[#21262d] text-gray-400 rounded text-[9px] border border-[#30363d]"
                        >
                          {tag}
                        </span>
                      ))}
                    </div>
                  )}
                </div>
              </div>

              {/* Actions Footer */}
              <div className="mt-3 pt-2.5 border-t border-[#30363d] flex items-center justify-between font-mono text-xs">
                <button
                  type="button"
                  disabled={pinging && pingTarget === primaryIp}
                  onClick={() => handlePing(primaryIp)}
                  className="px-2 py-1 bg-[#21262d] hover:bg-[#30363d] text-gray-200 rounded text-[11px] border border-[#30363d] transition-colors flex items-center gap-1"
                >
                  <Zap className="w-3 h-3 text-amber-400" />
                  {pinging && pingTarget === primaryIp ? 'Pinging...' : 'Ping'}
                </button>

                {onOpenTerminalForHost && (
                  <button
                    type="button"
                    onClick={() => onOpenTerminalForHost(primaryIp)}
                    className="px-2 py-1 bg-[#1f6feb]/20 hover:bg-[#1f6feb]/40 text-[#58a6ff] rounded text-[11px] border border-[#1f6feb]/40 transition-colors"
                  >
                    Open SSH
                  </button>
                )}
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
};
