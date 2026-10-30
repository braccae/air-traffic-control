import React from 'react';
import type { PackageCheckResult } from '../utils/packageManager.ts';

export interface BootcDeploymentsCardProps {
  check: PackageCheckResult;
}

const isAbsent = (value?: string): boolean => !value || value.trim() === '' || value === 'none';

const shortDigest = (digest?: string): string => {
  if (isAbsent(digest)) return '';
  return digest!.replace(/^sha256:/, '').slice(0, 12);
};

const DeploymentCell: React.FC<{
  label: string;
  badge: string;
  badgeClass: string;
  testId: string;
  image?: string;
  version?: string;
  digest?: string;
  emptyText: string;
  footnote?: string;
}> = ({ label, badge, badgeClass, testId, image, version, digest, emptyText, footnote }) => {
  const absent = isAbsent(image);
  return (
    <div
      data-testid={testId}
      className="rounded-lg bg-[#171a2d] border border-[#26293c] p-3 flex flex-col gap-1.5 min-w-0"
    >
      <div className="flex items-center justify-between gap-2">
        <span className="text-[10px] font-mono font-bold uppercase tracking-wider text-[#8d909b]">{label}</span>
        <span className={`text-[9px] font-mono px-1.5 py-0.2 rounded border ${badgeClass}`}>{badge}</span>
      </div>

      {absent ? (
        <div className="text-[11px] font-mono text-[#8d909b] italic py-1">{emptyText}</div>
      ) : (
        <>
          <div className="text-xs font-mono font-bold text-[#dfe1fa] break-all" title={image}>
            {image}
          </div>
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[10px] font-mono text-[#8d909b]">
            <span>
              version: <span className="text-[#8cd6cb]">{version || 'unknown'}</span>
            </span>
            {shortDigest(digest) && (
              <span title={digest}>
                digest: <span className="text-[#c3c6d2]">{shortDigest(digest)}</span>
              </span>
            )}
          </div>
          {footnote && <div className="text-[10px] font-mono text-[#ffd97d]">{footnote}</div>}
        </>
      )}
    </div>
  );
};

/**
 * bootc deployment summary: the image the host booted, the rollback target, and
 * (when one exists) the image staged for the next boot. Values come from
 * `bootc status --format json`; absent deployments render an explicit empty
 * state instead of a fabricated one.
 */
export const BootcDeploymentsCard: React.FC<BootcDeploymentsCardProps> = ({ check }) => {
  const staged = !isAbsent(check.stagedImage);
  return (
    <section
      data-testid="bootc-deployments"
      className="bg-[#121528] border border-[#26293c] rounded-lg p-3 flex flex-col gap-2.5"
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <span className="material-symbols-outlined text-[18px] text-[#d9b9ff]">deployed_code</span>
          <h3 className="text-xs font-mono font-bold text-[#dfe1fa] uppercase tracking-wider">bootc Deployments</h3>
        </div>
        <span className="text-[10px] font-mono text-[#8d909b]">
          source: <code className="text-[#79c0ff]">bootc status --format json</code>
        </span>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-3 gap-2">
        <DeploymentCell
          testId="bootc-booted"
          label="Current (booted)"
          badge="BOOTED"
          badgeClass="bg-[#162620] text-[#8cd6cb] border-[#8cd6cb]/30"
          image={check.currentImage}
          version={check.currentImageVersion}
          digest={check.currentImageDigest}
          emptyText="bootc reported no booted deployment"
        />
        <DeploymentCell
          testId="bootc-rollback"
          label="Rollback"
          badge="ROLLBACK"
          badgeClass="bg-[#21263d] text-[#b0c9ff] border-[#b0c9ff]/30"
          image={check.fallbackImage}
          version={check.fallbackImageVersion}
          digest={check.fallbackImageDigest}
          emptyText="No rollback deployment available"
          footnote="Roll back with: bootc rollback"
        />
        <DeploymentCell
          testId="bootc-staged"
          label="Staged"
          badge={staged ? 'STAGED' : 'NONE'}
          badgeClass={
            staged
              ? 'bg-[#2d281b] text-[#ffd97d] border-[#ffd97d]/40'
              : 'bg-[#171a2d] text-[#8d909b] border-[#26293c]'
          }
          image={check.stagedImage}
          version={check.stagedImageVersion}
          digest={check.stagedImageDigest}
          emptyText="No staged deployment — the running image is current"
          footnote={staged ? 'Reboot to activate the staged image' : undefined}
        />
      </div>
    </section>
  );
};

export default BootcDeploymentsCard;
