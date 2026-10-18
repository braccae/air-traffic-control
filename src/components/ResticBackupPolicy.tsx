import React from 'react';

interface ResticBackupPolicyProps {
  onIntegrityCheck: () => void;
  onBrowseFiles: () => void;
  onSnapshot: () => void;
}

export const ResticBackupPolicy: React.FC<ResticBackupPolicyProps> = ({
  onIntegrityCheck,
  onBrowseFiles,
  onSnapshot,
}) => {
  return (
    <section className="md:col-span-6 bg-[#171a2d] rounded p-3 flex flex-col justify-between border border-[#26293c]">
      <div>
        {/* Header */}
        <div className="flex items-center justify-between pb-1.5 bg-[#0a0d1f]/40 px-2 py-1 rounded border border-[#26293c]/40">
          <div className="flex items-center gap-2">
            <span className="material-symbols-outlined text-[#8cd6cb] text-[18px]">
              verified_user
            </span>
            <span className="font-sans text-sm text-[#dfe1fa] font-semibold">
              Restic Immutable Backups
            </span>
          </div>
          <span className="px-2 py-0.5 bg-[#8cd6cb]/20 text-[#8cd6cb] font-mono text-xs font-bold rounded border border-[#8cd6cb]/30">
            GOLD POLICY
          </span>
        </div>

        {/* Repository details */}
        <div className="mt-2 space-y-1.5 font-mono text-xs">
          <div className="bg-[#1b1e31] p-2 rounded flex flex-col gap-1 border border-[#26293c]/50">
            <div className="flex items-center justify-between">
              <span className="text-[#8d909b] text-[10px]">TARGET REPOSITORY</span>
              <span className="text-[#8cd6cb] text-[10px] font-semibold">AES-256 ENCRYPTED</span>
            </div>
            <span className="text-[#b0c9ff] font-bold truncate">
              s3://restic-homelab-vault/the-freezer
            </span>
            <div className="flex items-center justify-between text-[#8d909b] text-[10px]">
              <span>Retention: 7d, 4w, 12m</span>
              <span>Immutable Object Lock: ON</span>
            </div>
          </div>

          <div className="bg-[#0a0d1f]/60 p-2 rounded grid grid-cols-2 gap-2 border border-[#26293c]/40">
            <div>
              <span className="text-[#8d909b] text-[10px] block">LAST SNAPSHOT</span>
              <span className="text-[#dfe1fa] font-bold text-[#8cd6cb]">24 mins ago</span>
              <span className="text-[#8d909b] text-[10px] block">
                Snapshot: <strong className="text-[#b0c9ff]">f7a91b2e</strong>
              </span>
            </div>
            <div>
              <span className="text-[#8d909b] text-[10px] block">DEDUPLICATION</span>
              <span className="text-[#dfe1fa] font-bold">412.8 GiB</span>
              <span className="text-[#8d909b] text-[10px] block">New chunks: 0 B (100% dedup)</span>
            </div>
          </div>
        </div>
      </div>

      {/* Buttons */}
      <div className="mt-2 pt-2 flex items-center justify-between gap-1 border-t border-[#26293c]/50">
        <button
          type="button"
          onClick={onIntegrityCheck}
          className="flex-1 flex items-center justify-center gap-1 bg-[#26293c] hover:bg-[#35384c] text-[#8cd6cb] px-2 py-1 rounded font-mono text-xs transition-colors cursor-pointer border border-[#434750]/30"
        >
          <span className="material-symbols-outlined text-[14px]">shield_with_heart</span>
          <span>Integrity Check</span>
        </button>

        <button
          type="button"
          onClick={onBrowseFiles}
          className="flex-1 flex items-center justify-center gap-1 bg-[#26293c] hover:bg-[#35384c] text-[#b0c9ff] px-2 py-1 rounded font-mono text-xs transition-colors cursor-pointer border border-[#434750]/30"
        >
          <span className="material-symbols-outlined text-[14px]">history</span>
          <span>Browse Files</span>
        </button>

        <button
          type="button"
          onClick={onSnapshot}
          className="flex-1 flex items-center justify-center gap-1 bg-[#b0c9ff] text-[#002e68] font-bold hover:bg-[#d8e2ff] px-2 py-1 rounded font-mono text-xs transition-colors shadow-sm cursor-pointer"
        >
          <span className="material-symbols-outlined text-[14px]">backup</span>
          <span>Snapshot</span>
        </button>
      </div>
    </section>
  );
};
