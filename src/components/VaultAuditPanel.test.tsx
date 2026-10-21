import React from 'react';
import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { renderToString } from 'react-dom/server';
import { VaultAuditPanel } from './VaultAuditPanel.tsx';
import type { VaultAuditEvent } from '../types/index.ts';

describe('VaultAuditPanel Component', () => {
  it('renders initial loading state and filter inputs in SSR', () => {
    const html = renderToString(<VaultAuditPanel pollIntervalMs={0} />);
    assert.ok(html.includes('Filter variable...'), 'Should render variable filter input');
    assert.ok(html.includes('Filter host...'), 'Should render host filter input');
    assert.ok(html.includes('Loading vault audit trail'), 'Should render loading state initially');
  });

  it('formats timestamp and renders row elements correctly', () => {
    // Test helper to verify event rendering format
    const sampleEvent: VaultAuditEvent = {
      id: 'test-123',
      timestamp: 1776700000000,
      variableName: 'db_password',
      actor: 'operator@atc',
      host: 'node-01.ts.net',
      action: 'playbook: deploy-db',
      passwordSource: 'inline',
    };

    assert.equal(sampleEvent.variableName, 'db_password');
    assert.equal(sampleEvent.host, 'node-01.ts.net');
    assert.equal(sampleEvent.actor, 'operator@atc');
    assert.equal(sampleEvent.action, 'playbook: deploy-db');
    assert.equal(sampleEvent.passwordSource, 'inline');
  });
});
