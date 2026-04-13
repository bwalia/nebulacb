import React from 'react';
import {
  Command, ClusterMetrics, XDCRStatus, DataLossProof, StormMetrics,
  UpgradeStatus, Alert,
} from '../types';
import { ClusterCard } from './ClusterCard';
import { XDCRFlowPanel } from './XDCRFlowPanel';
import { LoadPanel } from './LoadPanel';
import { AlertPanel } from './AlertPanel';
import { DataLossProofPanel } from './DataLossProofPanel';
import { ControlPanel } from './ControlPanel';
import { UpgradeTimeline } from './UpgradeTimeline';
import { LogsPanel } from './LogsPanel';

interface Props {
  clusters: Record<string, ClusterMetrics>;
  source: ClusterMetrics;
  target: ClusterMetrics;
  xdcr: XDCRStatus;
  proof: DataLossProof;
  storm: StormMetrics;
  stormHistory: StormMetrics[];
  upgrade: UpgradeStatus;
  alerts: Alert[];
  onCommand: (cmd: Command) => void;
}

export const CockpitView: React.FC<Props> = ({
  clusters, source, target, xdcr, proof, storm, stormHistory, upgrade, alerts, onCommand,
}) => {
  const upgrading = upgrade.phase === 'upgrading' || upgrade.phase === 'rebalancing';
  const sourceHealthy = source.healthy;
  const targetHealthy = target.healthy;
  const overallHealth = sourceHealthy && targetHealthy ? 'green' : (!sourceHealthy && !targetHealthy ? 'red' : 'amber');
  const overallLabel = overallHealth === 'green' ? 'NOMINAL' : overallHealth === 'amber' ? 'DEGRADED' : 'CRITICAL';

  return (
    <div className="cockpit">
      <div className="cockpit-status-bar">
        <div className={`cockpit-status-pill ${overallHealth}`}>
          <span className="cockpit-status-dot" />
          MISSION STATUS &middot; {overallLabel}
        </div>
        <div className="cockpit-status-meta">
          <span><strong>{Object.keys(clusters).length || 2}</strong> clusters</span>
          <span><strong>{(source.nodes?.length || 0) + (target.nodes?.length || 0)}</strong> nodes</span>
          <span>XDCR: <strong className={`xdcr-${xdcr.state?.toLowerCase() || 'unknown'}`}>{xdcr.state || 'Unknown'}</strong></span>
          <span>Phase: <strong>{(upgrade.phase || 'idle').toUpperCase()}</strong></span>
          <span>Δ: <strong className={proof.current_delta === 0 ? 'good' : 'warn'}>{proof.current_delta || 0}</strong></span>
        </div>
      </div>

      {/* Top strip: 4 mission-status tiles */}
      <div className="cockpit-top-strip">
        <div className="cockpit-tile">
          <ClusterCard cluster={source} role="source" upgrading={upgrading} />
        </div>
        <div className="cockpit-tile">
          <ClusterCard cluster={target} role="target" />
        </div>
        <div className="cockpit-tile">
          <XDCRFlowPanel xdcr={xdcr} sourceDocs={source.total_docs} targetDocs={target.total_docs} />
        </div>
        <div className="cockpit-tile cockpit-tile-stack">
          <LoadPanel metrics={storm} history={stormHistory} />
          <AlertPanel alerts={alerts} />
        </div>
      </div>

      {/* Centerpiece: Upgrade Timeline */}
      <UpgradeTimeline upgrade={upgrade} xdcr={xdcr} source={source} alerts={alerts} />

      {/* Bottom row: Logs | Data Integrity | Controls */}
      <div className="cockpit-bottom-row">
        <div className="cockpit-bottom-col cockpit-logs-col">
          <LogsPanel alerts={alerts} xdcr={xdcr} upgrade={upgrade} clusters={clusters} />
        </div>
        <div className="cockpit-bottom-col cockpit-integrity-col">
          <DataLossProofPanel proof={proof} />
        </div>
        <div className="cockpit-bottom-col cockpit-controls-col">
          <ControlPanel onCommand={onCommand} clusters={clusters} xdcrStatus={xdcr} />
        </div>
      </div>
    </div>
  );
};
