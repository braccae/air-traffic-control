import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import { renderToString } from 'react-dom/server';
import { HostModulesView, type NodeModuleItem } from './HostModulesView.tsx';
import type { NodeItem } from '../types/index.ts';

const mockNode: NodeItem = {
  id: 'node-modules-test',
  name: 'Test Worker Node',
  shortName: 'worker-1',
  group: 'tag:worker',
  datacenter: 'leo-banjo.ts.net',
  nodeType: 'linux',
  ip: '192.168.1.150',
  port: 22,
  tailscaleIp: '100.64.0.50',
  tailscaleHost: 'worker-1.leo-banjo.ts.net',
  osDescription: 'AlmaLinux 9',
  status: 'online',
  uptime: '5 days',
  hasGlances: true,
  hasCockpit: true,
  hasPodman: true,
  hasRestic: true,
  specs: {
    cpu: 'AMD EPYC',
    vcpus: 16,
    ramGiB: 64,
    motherboard: 'Supermicro H12SSL',
    bios: 'v2.1',
    ipmi: '192.168.1.151',
    ecc: 'DDR4 ECC',
    secureBoot: 'enabled',
    iommu: 'enabled',
  },
};

const mockModules: NodeModuleItem[] = [
  {
    id: 'storage/zfs',
    kind: 'storage',
    name: 'zfs',
    label: 'OpenZFS Storage',
    icon: 'storage',
    section: 'storage',
    order: 10,
    status: 'active',
    requirements: {
      hostPackages: {
        zfs: ['zfs', 'zfs-kmod'],
      },
      controllerCollections: ['community.general'],
    },
    detection: {
      detected: true,
      status: 'active',
      details: 'zfs-2.2.3, 2 pools healthy',
    },
  },
  {
    id: 'container/podman',
    kind: 'container',
    name: 'podman',
    label: 'Podman Containers',
    icon: 'deployed_code',
    section: 'container',
    order: 10,
    status: 'available',
    reason: 'Package or subsystem not detected on host',
    requirements: {
      hostPackages: {
        podman: ['podman', 'crun'],
      },
      controllerCollections: ['containers.podman'],
    },
    detection: {
      detected: false,
      status: 'AVAILABLE',
      details: 'podman binary not in PATH',
    },
  },
  {
    id: 'vm/libvirt',
    kind: 'vm',
    name: 'libvirt',
    label: 'KVM / Libvirt',
    icon: 'computer',
    section: 'vm',
    order: 20,
    status: 'unsupported',
    reason: 'Incompatible with host OS (windows) or connection',
    requirements: {
      hostPackages: {
        libvirt: ['libvirt-daemon-system', 'qemu-kvm'],
      },
      controllerCollections: ['community.general'],
    },
  },
];

test('HostModulesView renders active, available, and unsupported plugins', () => {
  const html = renderToString(
    React.createElement(HostModulesView, {
      node: mockNode,
      initialModules: mockModules,
    })
  );

  // Status pills
  assert.ok(html.includes('ACTIVE'), 'Must render ACTIVE badge');
  assert.ok(html.includes('AVAILABLE'), 'Must render AVAILABLE badge');
  assert.ok(html.includes('UNSUPPORTED'), 'Must render UNSUPPORTED badge');

  // Plugin labels
  assert.ok(html.includes('OpenZFS Storage'));
  assert.ok(html.includes('Podman Containers'));
  assert.ok(html.includes('KVM / Libvirt'));

  // Reason for unsupported
  assert.ok(html.includes('Incompatible with host OS'));
});

test('HostModulesView displays host package requirements and Ansible collections', () => {
  const html = renderToString(
    React.createElement(HostModulesView, {
      node: mockNode,
      initialModules: mockModules,
    })
  );

  // Host packages
  assert.ok(html.includes('zfs-kmod'), 'Must display required host package zfs-kmod');
  assert.ok(html.includes('crun'), 'Must display required host package crun');

  // Ansible collections
  assert.ok(html.includes('community.general'), 'Must display Ansible collection tag');
  assert.ok(html.includes('containers.podman'), 'Must display Ansible collection tag');
});

test('HostModulesView provides an Install button for available plugins with package dependencies', () => {
  const html = renderToString(
    React.createElement(HostModulesView, {
      node: mockNode,
      initialModules: mockModules,
    })
  );

  // Available plugin should have an Install button
  assert.ok(html.includes('Install'), 'Must render Install button for available plugin');
});
