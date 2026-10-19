import React from 'react';
import type { TelemetryData } from '../types/index.ts';
import { calculateSystemHealth } from '../utils/orchestrator.ts';

interface SystemHealthTelemetryProps {
  telemetry: TelemetryData;
}

export const SystemHealthTelemetry: React.FC<SystemHealthTelemetryProps> = ({
  telemetry,
}) => {
  const health = calculateSystemHealth(telemetry);

  // Build SVG path from sparkline array
  const points = telemetry.sparkline;
  const maxVal = Math.max(...points, 25);
  const minVal = Math.min(...points, 5);
  const width = 100;
  const height = 24;

  const pathD = points
    .map((val, idx) => {
      const x = (idx / (points.length - 1)) * width;
      const normalized = (val - minVal) / (maxVal - minVal || 1);
      const y = height - 4 - normalized * (height - 8);
      return `${idx === 0 ? 'M' : 'L'} ${x.toFixed(1)} ${y.toFixed(1)}`;
    })
    .join(' ');

  const areaD = `${pathD} L ${width} ${height} L 0 ${height} Z`;

  return (
    <section className="md:col-span-7 bg-[#171a2d] rounded p-3 flex flex-col justify-between border border-[#26293c]">
      <div>
        {/* Title and Health badge */}
        <div className="flex items-center justify-between pb-1.5 bg-[#0a0d1f]/40 px-2 py-1 rounded border border-[#26293c]/40">
          <div className="flex items-center gap-2">
            <span className="material-symbols-outlined text-[#b0c9ff] text-[18px]">speed</span>
            <span className="font-sans text-sm text-[#dfe1fa] font-semibold">
              System Health &amp; Telemetry
            </span>
          </div>
          <div className="flex items-center gap-2 font-mono text-xs">
            {telemetry.metricsSource && (
              <span className={`px-1.5 py-0.5 rounded text-[10px] font-bold border ${
                telemetry.metricsSource === 'glances'
                  ? 'bg-[#8cd6cb]/20 text-[#8cd6cb] border-[#8cd6cb]/30'
                  : telemetry.metricsSource === 'pcp'
                  ? 'bg-[#b0c9ff]/20 text-[#b0c9ff] border-[#b0c9ff]/30'
                  : 'bg-[#ffd97d]/20 text-[#ffd97d] border-[#ffd97d]/30'
              }`}>
                {telemetry.metricsSource.toUpperCase()}
              </span>
            )}
            <span className={`px-1.5 py-0.5 rounded font-bold border ${
              health === 'HEALTHY'
                ? 'bg-[#8cd6cb]/20 text-[#8cd6cb] border-[#8cd6cb]/30'
                : health === 'WARNING'
                ? 'bg-[#ffd97d]/20 text-[#ffd97d] border-[#ffd97d]/30'
                : 'bg-[#ffb4ab]/20 text-[#ffb4ab] border-[#ffb4ab]/30'
            }`}>
              {health}
            </span>
          </div>
        </div>

        {/* Warning banner when Glances is not installed and fallback is used */}
        {telemetry.metricsWarning && (
          <div className="mt-2 bg-[#ffd97d]/10 border border-[#ffd97d]/30 rounded px-2.5 py-1.5 flex items-center gap-2 font-mono text-[11px] text-[#ffd97d]">
            <span className="material-symbols-outlined text-[15px] shrink-0 text-[#ffd97d]">
              warning
            </span>
            <span className="flex-1">{telemetry.metricsWarning}</span>
          </div>
        )}

        {/* 4 Telemetry Metrics Grid */}
        <div className="grid grid-cols-2 sm:grid-cols-4 gap-2 mt-2">
          {/* CPU Usage */}
          <div className="bg-[#1b1e31] p-2 rounded flex flex-col border border-[#26293c]/50">
            <span className="font-mono text-[10px] text-[#8d909b]">CPU USAGE</span>
            <span className="font-mono text-xs text-[#dfe1fa] font-bold mt-0.5">
              {telemetry.cpuUsage}%{' '}
              <span className="text-[#8d909b] text-[10px] font-normal">
                / {telemetry.vCpus} vCPUs
              </span>
            </span>
            <div className="w-full bg-[#26293c] h-1.5 rounded overflow-hidden mt-1.5">
              <div
                className="bg-[#b0c9ff] h-full transition-all duration-500"
                style={{ width: `${Math.max(2, telemetry.cpuUsage)}%` }}
              ></div>
            </div>
            <span className="font-mono text-[10px] text-[#8d909b] mt-1 truncate">
              {telemetry.cpuModel}
            </span>
          </div>

          {/* Memory & Cache */}
          <div className="bg-[#1b1e31] p-2 rounded flex flex-col border border-[#26293c]/50">
            <span className="font-mono text-[10px] text-[#8d909b]">
              MEMORY &amp; {telemetry.arcCacheGiB > 0 ? 'ZFS ARC' : 'BUFF/CACHE'}
            </span>
            <span className="font-mono text-xs text-[#dfe1fa] font-bold mt-0.5">
              {telemetry.memoryUsedGiB}{' '}
              <span className="text-[#8d909b] text-[10px] font-normal">
                / {telemetry.memoryTotalGiB} GiB
              </span>
            </span>
            <div className="w-full bg-[#26293c] h-1.5 rounded overflow-hidden mt-1.5 flex">
              <div
                className="bg-[#d9b9ff] h-full"
                style={{ width: `${Math.min(100, Math.round((telemetry.memoryUsedGiB / Math.max(1, telemetry.memoryTotalGiB)) * 100))}%` }}
                title={`Used Memory: ${telemetry.memoryUsedGiB} GiB`}
              ></div>
              <div
                className="bg-[#8cd6cb] h-full"
                style={{ width: `${Math.min(40, Math.round(((telemetry.arcCacheGiB || telemetry.buffCacheGiB || 4) / Math.max(1, telemetry.memoryTotalGiB)) * 100))}%` }}
                title={telemetry.arcCacheGiB > 0 ? `ZFS ARC: ${telemetry.arcCacheGiB} GiB` : `Page Cache: ${telemetry.buffCacheGiB || 'N/A'} GiB`}
              ></div>
            </div>
            <span className="font-mono text-[10px] text-[#d9b9ff] mt-1 truncate">
              {telemetry.arcCacheGiB > 0
                ? `ZFS ARC: ${telemetry.arcCacheGiB} GiB`
                : `Buff/Cache: ${telemetry.buffCacheGiB ? telemetry.buffCacheGiB + ' GiB' : 'Page Cache'} (ZFS: N/A)`}
            </span>
          </div>

          {/* Storage / ZFS Pool */}
          <div className="bg-[#1b1e31] p-2 rounded flex flex-col border border-[#26293c]/50">
            <span className="font-mono text-[10px] text-[#8d909b]">
              STORAGE: {telemetry.zfsTankTotalTb > 0 ? 'ZFS POOL' : 'FILESYSTEM'}
            </span>
            <span className="font-mono text-xs text-[#dfe1fa] font-bold mt-0.5 truncate">
              {telemetry.zfsTankTotalTb > 0 ? (
                <>
                  {telemetry.zfsTankUsedTb}{' '}
                  <span className="text-[#8d909b] text-[10px] font-normal">
                    / {telemetry.zfsTankTotalTb} TB
                  </span>
                </>
              ) : (
                telemetry.zfsRaidType || 'Btrfs / XFS Partitions'
              )}
            </span>
            <div className="w-full bg-[#26293c] h-1.5 rounded overflow-hidden mt-1.5">
              <div
                className="bg-[#8cd6cb] h-full"
                style={{
                  width: telemetry.zfsTankTotalTb > 0
                    ? `${Math.min(100, Math.round((telemetry.zfsTankUsedTb / Math.max(0.1, telemetry.zfsTankTotalTb)) * 100))}%`
                    : '45%',
                }}
              ></div>
            </div>
            <span className="font-mono text-[10px] text-[#8cd6cb] mt-1 truncate">
              {telemetry.zfsTankTotalTb > 0
                ? `${telemetry.zfsRaidType} • ${telemetry.zfsScrubStatus}`
                : telemetry.zfsScrubStatus || 'Root FS nominal'}
            </span>
          </div>

          {/* Load Average */}
          <div className="bg-[#1b1e31] p-2 rounded flex flex-col border border-[#26293c]/50">
            <span className="font-mono text-[10px] text-[#8d909b]">LOAD AVERAGE</span>
            <span className="font-mono text-xs text-[#dfe1fa] font-bold mt-0.5">
              {telemetry.loadAvg1m}{' '}
              <span className="text-[#8d909b] text-[10px] font-normal">
                {telemetry.loadAvg5m} {telemetry.loadAvg15m}
              </span>
            </span>
            <div className="w-full bg-[#26293c] h-1.5 rounded overflow-hidden mt-1.5">
              <div className="bg-[#8cd6cb] h-full w-[20%]"></div>
            </div>
            <span className="font-mono text-[10px] text-[#8cd6cb] mt-1">
              IO Delay: {telemetry.ioDelayPercent}%
            </span>
          </div>
        </div>
      </div>

      {/* Micro Sparkline SVG & Thermal Metrics */}
      <div className="mt-2.5 pt-2 bg-[#313447]/20 p-2 rounded flex items-center justify-between border border-[#26293c]/40">
        <div className="flex items-center gap-3 font-mono text-xs">
          <span className="flex items-center gap-1 text-[#dfe1fa]">
            <span className="material-symbols-outlined text-[15px] text-[#b0c9ff]">
              device_thermostat
            </span>
            CPU Package: {telemetry.cpuTempC}°C
          </span>
          <span className="flex items-center gap-1 text-[#8d909b]">
            <span className="material-symbols-outlined text-[15px] text-[#8cd6cb]">toys</span>
            Fans: {telemetry.fanSpeedRpm} RPM
          </span>
          <span className="flex items-center gap-1 text-[#8d909b]">
            <span className="material-symbols-outlined text-[15px] text-[#d9b9ff]">bolt</span>
            Package: {telemetry.packageWatts} W
          </span>
        </div>

        {/* CPU Activity Sparkline (Inline SVG) */}
        <div className="w-32 h-6 flex items-center">
          <svg className="w-full h-full text-[#b0c9ff]" fill="none" viewBox="0 0 100 24">
            <path
              d={pathD}
              stroke="currentColor"
              strokeLinecap="round"
              strokeLinejoin="round"
              strokeWidth="1.8"
            />
            <path d={areaD} fill="currentColor" fillOpacity="0.15" />
          </svg>
        </div>
      </div>
    </section>
  );
};
