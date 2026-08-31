import React from 'react';
import { render, screen, fireEvent, act, waitFor } from '@testing-library/react';
import { ControlPanel } from './ControlPanel';
import { ClusterMetrics, Command } from '../types';

const cluster: ClusterMetrics = {
  cluster_name: 'src', nodes: [], rebalance_state: 'none', total_docs: 42,
  total_mem_used_mb: 0, total_mem_total_mb: 0, ops_per_sec: 0,
  cmd_get_per_sec: 0, cmd_set_per_sec: 0, disk_write_queue: 0,
  ep_bg_fetched: 0, ep_cache_miss_rate: 0, vb_active_resident_items_ratio: 0,
  curr_connections: 0, version: '7.2.2', healthy: true,
};

const clusters: Record<string, ClusterMetrics> = { src: cluster, tgt: { ...cluster, cluster_name: 'tgt' } };

const diagnosticsResponse = {
  timestamp: '2026-04-15T00:00:00Z',
  replication_id: 'src->tgt',
  overall_status: 'healthy',
  checks: [
    { name: 'pipeline', title: 'Pipeline', status: 'ok', detail: 'Running' },
  ],
  delay_windows: [],
  current_status: { state: 'Running', changes_left: 0, docs_processed: 100, topology_change: false },
};

beforeEach(() => {
  // URL-aware fetch mock: dashboard returns clusters, diagnostics returns check results
  (global as any).fetch = jest.fn((url: string) => {
    const body = typeof url === 'string' && url.includes('/xdcr/diagnostics')
      ? diagnosticsResponse
      : { clusters };
    return Promise.resolve({ ok: true, json: () => Promise.resolve(body) });
  });
});

afterEach(() => {
  jest.resetAllMocks();
});

// Actions that are sent immediately on click (no modal)
const directActions: { label: string; action: string }[] = [
  { label: 'Start Load', action: 'start_load' },
  { label: 'Pause Load', action: 'pause_load' },
  { label: 'Resume Load', action: 'resume_load' },
  { label: 'Stop Load', action: 'stop_load' },
  { label: 'Abort Upgrade', action: 'abort_upgrade' },
  { label: 'Pause XDCR', action: 'pause_xdcr' },
  { label: 'Resume XDCR', action: 'resume_xdcr' },
  { label: 'Stop XDCR', action: 'stop_xdcr' },
  { label: 'Restart XDCR', action: 'restart_xdcr' },
  { label: 'Full Audit', action: 'run_audit' },
  { label: 'AI Analyze', action: 'ai_analyze' },
];

describe('ControlPanel direct-action buttons', () => {
  directActions.forEach(({ label, action }) => {
    test(`${label} -> ${action}`, () => {
      const onCommand = jest.fn();
      render(<ControlPanel onCommand={onCommand} clusters={clusters} />);
      fireEvent.click(screen.getByText(label));
      expect(onCommand).toHaveBeenCalledWith({ action });
    });
  });

  test('Inject Failure sends default params', () => {
    const onCommand = jest.fn();
    render(<ControlPanel onCommand={onCommand} clusters={clusters} />);
    fireEvent.click(screen.getByText('Inject Failure'));
    expect(onCommand).toHaveBeenCalledWith({
      action: 'inject_failure',
      params: { type: 'xdcr_partition', target: 'replication' },
    });
  });
});

describe('ControlPanel modal actions', () => {
  test('Backup opens modal and submits start_backup', async () => {
    const onCommand = jest.fn();
    render(<ControlPanel onCommand={onCommand} clusters={clusters} />);
    fireEvent.click(screen.getByText('Backup'));
    const confirm = await screen.findByRole('button', { name: /Start Backup/i });
    // wait until cluster list is populated so button is enabled
    await waitFor(() => expect(confirm).not.toBeDisabled());
    await act(async () => {
      fireEvent.click(confirm);
    });
    expect(onCommand).toHaveBeenCalledWith({
      action: 'start_backup',
      params: { cluster_name: 'src' },
    });
  });

  test('Downgrade opens confirmation modal and fires action', async () => {
    const onCommand = jest.fn();
    render(<ControlPanel onCommand={onCommand} clusters={clusters} />);
    fireEvent.click(screen.getByText('Downgrade'));
    const confirm = await screen.findByRole('button', { name: /Confirm Downgrade/i });
    fireEvent.click(confirm);
    expect(onCommand).toHaveBeenCalledWith({ action: 'downgrade' });
  });

  test('Start Upgrade submits cluster_name/target_version/namespace', async () => {
    const onCommand = jest.fn();
    render(<ControlPanel onCommand={onCommand} clusters={clusters} />);
    fireEvent.click(screen.getByText('Start Upgrade'));
    // confirm button label contains "Upgrade ... to ..."
    const confirmBtn = await screen.findByRole('button', { name: /^Upgrade/i });
    await waitFor(() => expect(confirmBtn).not.toBeDisabled());
    await act(async () => {
      fireEvent.click(confirmBtn);
    });
    expect(onCommand).toHaveBeenCalledWith(expect.objectContaining({
      action: 'start_upgrade',
      params: expect.objectContaining({ cluster_name: 'src', namespace: 'couchbase' }),
    }));
  });

  test('Manual Failover submits source/target', async () => {
    const onCommand = jest.fn();
    render(<ControlPanel onCommand={onCommand} clusters={clusters} />);
    fireEvent.click(screen.getByText('Failover'));
    const sourceSelect = await screen.findByLabelText(/Source/i);
    const targetSelect = await screen.findByLabelText(/Target/i);
    fireEvent.change(sourceSelect, { target: { value: 'src' } });
    fireEvent.change(targetSelect, { target: { value: 'tgt' } });
    const exec = screen.getByRole('button', { name: /Execute Failover/i });
    await waitFor(() => expect(exec).not.toBeDisabled());
    fireEvent.click(exec);
    expect(onCommand).toHaveBeenCalledWith({
      action: 'manual_failover',
      params: { source_cluster: 'src', target_cluster: 'tgt' },
    });
  });
});

describe('ControlPanel button inventory', () => {
  test('renders every button action defined for the panel', () => {
    const onCommand = jest.fn();
    render(<ControlPanel onCommand={onCommand} clusters={clusters} />);
    const expected = [
      'Start Load', 'Pause Load', 'Resume Load', 'Stop Load',
      'Start Upgrade', 'Abort Upgrade', 'Downgrade',
      'Pause XDCR', 'Resume XDCR', 'Stop XDCR', 'Restart XDCR', 'XDCR Troubleshoot',
      'Full Audit', 'Inject Failure', 'AI Analyze', 'Backup', 'Restore', 'Failover',
    ];
    expected.forEach((label) => {
      expect(screen.getByText(label)).toBeInTheDocument();
    });
  });
});

// Ensure the Command shape stays in sync
const _typeCheck: Command = { action: 'noop' };
void _typeCheck;
