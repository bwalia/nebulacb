# Couchbase cluster hardening (k3s1)

Runbook and apply script for the `cb-bank-transactions-*` CouchbaseClusters in the
`couchbase` namespace on the k3s1 cluster. These CRs are managed by the Couchbase
Operator and are not tracked in git; this directory records the desired settings and
how to reapply them.

## Background (incident, 2026-08-05)

`cb-bank-transactions-eu-west-2-0000` was stuck for hours with repeated
`FailedAttachVolume` / `DeadlineExceeded` events. Root cause: an orphaned Longhorn
CSI attachment ticket (created 2026-07-30) pinned the volume
`pvc-2764271f-2ce0-46f9-8f9e-2ea125a731a9` to edge node `cloud001`, so Longhorn
refused to attach it to the node the pod was scheduled on. The ticket's Kubernetes
`VolumeAttachment` had long been garbage-collected, so nothing could satisfy or
clean it up.

Fix applied (one-off):

```sh
kubectl -n longhorn-system patch volumeattachments.longhorn.io <volume-name> \
  --type=json \
  -p '[{"op":"remove","path":"/spec/attachmentTickets/<stale-ticket-id>"}]'
```

Stale tickets are identifiable in `spec.attachmentTickets` of the
`volumeattachments.longhorn.io` CR: their ID has no matching
`kubectl get volumeattachment` (storage.k8s.io) object.

## Hardening (applied 2026-08-05, reapply with ./apply-hardening.sh)

1. **Longhorn replicas: 3** for both `cb-bank-transactions-*` data volumes.
   The `longhorn` StorageClass defaults to `numberOfReplicas: 1`, which loses the
   member's data on a single disk failure.
2. **Node affinity: keep Couchbase pods off edge nodes.** Adds
   `node-role.kubernetes.io/edge DoesNotExist` to the existing required node
   affinity (alongside the `debian010` hostname exclusion) in both CouchbaseCluster
   specs. The original incident started because a data pod had been scheduled on
   public-IP edge node `cloud001`.

New volumes created for these clusters still inherit `numberOfReplicas: 1` from the
StorageClass — rerun the script (or add a dedicated StorageClass) after the operator
creates new members.
