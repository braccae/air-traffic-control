import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import { renderToString } from 'react-dom/server';
import { DatacenterSummaryView } from './DatacenterSummaryView.tsx';
import type { GlobalTaskItem, NodeItem, QueuedAnsibleTask } from '../types/index.ts';

const node = (overrides: Partial<NodeItem> & Pick<NodeItem, 'id' | 'name'>): NodeItem => ({
  shortName: overrides.name,
  group: 'tag:host',
  datacenter: 'leo-banjo.ts.net',
  nodeType: 'linux',
  ip: '192.168.8.10',
  port: 22,
  tailscaleIp: '100.97.159.101',
  tailscaleHost: `${overrides.name}.leo-banjo.ts.net`,
  osDescription: 'Fedora 40',
  status: 'online',
  uptime: '4 days',
  hasGlances: true,
  hasCockpit: false,
  hasPodman: true,
  hasRestic: false,
  specs: {
    cpu: 'AMD Ryzen',
    vcpus: 8,
    ramGiB: 32,
    motherboard: '',
    bios: '',
    ipmi: '',
    ecc: '',
    secureBoot: '',
    iommu: '',
  },
  ...overrides,
});

const noop = () => {};

/** React inserts `<!-- -->` between adjacent text nodes; strip it before matching prose. */
const text = (html: string): string => html.replace(/<!--.*?-->/g, '');

const baseProps = {
  datacenterName: 'leo-banjo.ts.net',
  nodes: [
    node({ id: 'node-a', name: 'dev-pc' }),
    node({ id: 'node-b', name: 'likes-it-cold', status: 'degraded' }),
  ],
  tasks: [] as GlobalTaskItem[],
  onSelectNode: noop,
  onOpenPlaybookModal: noop,
  onOpenSetting: noop,
};

test('renders the datacenter quick actions and cluster stats from real node data', () => {
  const html = text(renderToString(<DatacenterSummaryView {...baseProps} />));

  assert.ok(html.includes('data-testid="datacenter-summary-view"'));
  assert.ok(html.includes('data-testid="summary-quick-action-run-playbook"'));
  assert.ok(html.includes('data-testid="summary-quick-action-manage-playbooks"'));
  assert.ok(html.includes('data-testid="summary-quick-action-vars-vault"'));
  assert.ok(html.includes('data-testid="summary-quick-action-console"'));
  assert.ok(html.includes('Datacenter Quick Actions'));

  // Health/online counts derive from the node statuses (1 online of 2 registered).
  assert.ok(html.includes('50% CRITICAL'));
  assert.ok(html.includes('1 / 2 NODES'));
  // No storage probe has been issued during server rendering, so totals are withheld.
  assert.ok(html.includes('Probing 1 online host'));
});

test('renders recent console tasks from the console feed and the ansible queue', () => {
  const tasks: GlobalTaskItem[] = [
    {
      id: 't1',
      startTime: '2026-10-20 09:00:00',
      nodeTarget: 'dev-pc.leo-banjo.ts.net',
      user: 'core@ssh-agent',
      description: 'zpool status -v',
      progress: 100,
      progressEta: 'done',
      status: 'OK',
      category: 'active',
    },
  ];

  const queue = [
    {
      id: 'q1',
      title: 'Restart nginx.service',
      targetNodeId: 'node-a',
      targetNodeName: 'dev-pc',
      category: 'system',
      primaryModule: 'ansible.builtin.systemd',
      steps: [],
      become: true,
      status: 'staged',
      createdAt: Date.parse('2026-10-20T09:45:00'),
    },
  ] as unknown as QueuedAnsibleTask[];

  const html = text(renderToString(<DatacenterSummaryView {...baseProps} tasks={tasks} queue={queue} />));

  assert.ok(html.includes('zpool status -v'));
  assert.ok(html.includes('Restart nginx.service'));
  assert.ok(html.includes('ansible queue'));
  assert.ok(html.includes('1 staged'));
  assert.ok(html.includes('1 Queued'));
  assert.ok(!html.includes('data-testid="summary-console-empty"'));
});

test('shows an honest empty state when no console tasks exist', () => {
  const html = renderToString(<DatacenterSummaryView {...baseProps} />);
  assert.ok(html.includes('data-testid="summary-console-empty"'));
  assert.ok(html.includes('No console tasks or queued ansible mutations recorded yet.'));
});

test('storage summary starts in a probing state and never fabricates totals', () => {
  const html = text(renderToString(<DatacenterSummaryView {...baseProps} />));

  assert.ok(html.includes('data-testid="summary-storage"'));
  assert.ok(html.includes('Probing 1 online host'));
  // No probe results have arrived during server rendering, so no totals are shown.
  assert.ok(!html.includes('Pool Capacity'));
});
