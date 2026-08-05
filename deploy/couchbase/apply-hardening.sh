#!/usr/bin/env bash
# Idempotently apply Couchbase hardening on k3s1: 3 Longhorn replicas for the
# cb-bank-transactions data volumes, and node affinity keeping Couchbase pods
# off edge nodes. See README.md for background.
set -euo pipefail

NAMESPACE=couchbase
CLUSTERS=(cb-bank-transactions-eu-west-1 cb-bank-transactions-eu-west-2)

echo "== Longhorn: numberOfReplicas=3 for ${NAMESPACE} data volumes =="
for pvc in $(kubectl -n "$NAMESPACE" get pvc -o jsonpath='{.items[*].spec.volumeName}'); do
  current=$(kubectl -n longhorn-system get volumes.longhorn.io "$pvc" -o jsonpath='{.spec.numberOfReplicas}')
  if [ "$current" != "3" ]; then
    kubectl -n longhorn-system patch volumes.longhorn.io "$pvc" --type=merge -p '{"spec":{"numberOfReplicas":3}}'
  else
    echo "volume $pvc already at 3 replicas"
  fi
done

echo "== CouchbaseCluster: required node affinity (no edge nodes, no debian010) =="
AFFINITY='[{"op":"replace","path":"/spec/servers/0/pod/spec/affinity/nodeAffinity/requiredDuringSchedulingIgnoredDuringExecution/nodeSelectorTerms/0/matchExpressions","value":[
  {"key":"kubernetes.io/hostname","operator":"NotIn","values":["debian010"]},
  {"key":"node-role.kubernetes.io/edge","operator":"DoesNotExist"}
]}]'
for c in "${CLUSTERS[@]}"; do
  kubectl -n "$NAMESPACE" patch couchbasecluster "$c" --type=json -p "$AFFINITY"
done

echo "Done."
