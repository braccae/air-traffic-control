import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import { renderToString } from 'react-dom/server';
import { BootcDeploymentsCard } from './BootcDeploymentsCard.tsx';
import type { PackageCheckResult } from '../utils/packageManager.ts';

const baseCheck: PackageCheckResult = {
  success: true,
  manager: 'bootc',
  isImmutable: true,
  updatesCount: 0,
  updates: [],
  lastChecked: '2026-10-27T00:00:00Z',
  rebootRequired: false,
  currentImage: 'ghcr.io/braccae/alma:hci',
  currentImageVersion: 'hci',
  currentImageDigest: 'sha256:7db4d570e0d3146ee539eda9a640b54f0620f9895ee4bb2cb5fe8e5c173d1668',
  fallbackImage: 'ghcr.io/braccae/alma:hci',
  fallbackImageVersion: 'hci',
  fallbackImageDigest: 'sha256:c39dafda886598935a07451cdcddaaf61e0fc5e4be517f4057e06cbcdc69f22a',
  stagedImage: 'none',
};

test('bootc card lists booted and rollback images from bootc status', () => {
  const html = renderToString(<BootcDeploymentsCard check={baseCheck} />);

  assert.ok(html.includes('data-testid="bootc-deployments"'));
  assert.ok(html.includes('bootc status --format json'));

  // Current deployment
  assert.ok(html.includes('Current (booted)'));
  assert.ok(html.includes('ghcr.io/braccae/alma:hci'));
  assert.ok(html.includes('version: '));
  assert.ok(html.includes('hci'));
  assert.ok(html.includes('7db4d570e0d3'), 'booted digest must be rendered (short form)');

  // Rollback deployment
  assert.ok(html.includes('Rollback'));
  assert.ok(html.includes('c39dafda8865'), 'rollback digest must be rendered (short form)');
  assert.ok(html.includes('bootc rollback'));

  // No staged deployment: explicit empty state, never a fabricated image.
  assert.ok(html.includes('No staged deployment'));
  assert.ok(!html.includes('Reboot to activate the staged image'));
});

test('bootc card shows the staged image when one exists', () => {
  const staged: PackageCheckResult = {
    ...baseCheck,
    stagedImage: 'quay.io/centos-bootc/centos-bootc:stream9',
    stagedImageVersion: '9.20240320.0',
    stagedImageDigest: 'sha256:bbbb',
    updatesCount: 1,
    rebootRequired: true,
  };
  const html = renderToString(<BootcDeploymentsCard check={staged} />);

  assert.ok(html.includes('quay.io/centos-bootc/centos-bootc:stream9'));
  assert.ok(html.includes('9.20240320.0'));
  assert.ok(html.includes('STAGED'));
  assert.ok(html.includes('Reboot to activate the staged image'));
  assert.ok(!html.includes('No staged deployment'));
});

test('bootc card reports an absent rollback honestly', () => {
  const html = renderToString(
    <BootcDeploymentsCard check={{ ...baseCheck, fallbackImage: 'none', fallbackImageDigest: '' }} />
  );
  assert.ok(html.includes('No rollback deployment available'));
  assert.ok(!html.includes('bootc rollback'));
});
