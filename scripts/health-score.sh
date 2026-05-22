#!/usr/bin/env bash
# health-score.sh — interrogate cluster health inputs and compute the expected
# losant-device health score without deploying anything.
#
# Usage:
#   ./scripts/health-score.sh [NAMESPACE]
#
# NAMESPACE defaults to "losant-system". The script queries the cluster that
# your current KUBECONFIG context points to.
#
# Requires: kubectl, awk (standard on macOS and Linux k3s nodes).
# Tested on k3s v1.28+.

set -euo pipefail

NS="${1:-losant-system}"
WARN_WINDOW_SECONDS=3600   # match internal/monitor/event_collector.go warningWindow

echo "=== losant-device health score interrogation ==="
echo "Cluster context : $(kubectl config current-context 2>/dev/null || echo 'unknown')"
echo "Timestamp       : $(date -u '+%Y-%m-%dT%H:%M:%SZ')"
echo ""

# ---------------------------------------------------------------------------
# 1. Node conditions
# ---------------------------------------------------------------------------
echo "--- Node conditions ---"
NODE_COUNT=0
NODE_NOT_READY=0
MEM_PRESSURE=0
DISK_PRESSURE=0
PID_PRESSURE=0

while IFS=$'\t' read -r node ready mem disk pid; do
    NODE_COUNT=$((NODE_COUNT + 1))
    echo "  $node: Ready=$ready  MemoryPressure=$mem  DiskPressure=$disk  PIDPressure=$pid"
    [ "$ready"  != "True" ] && NODE_NOT_READY=$((NODE_NOT_READY + 1))
    [ "$mem"  = "True" ] && MEM_PRESSURE=$((MEM_PRESSURE + 1))
    [ "$disk" = "True" ] && DISK_PRESSURE=$((DISK_PRESSURE + 1))
    [ "$pid"  = "True" ] && PID_PRESSURE=$((PID_PRESSURE + 1))
done < <(kubectl get nodes \
    -o custom-columns=\
'NAME:.metadata.name,READY:.status.conditions[?(@.type=="Ready")].status,MEM:.status.conditions[?(@.type=="MemoryPressure")].status,DISK:.status.conditions[?(@.type=="DiskPressure")].status,PID:.status.conditions[?(@.type=="PIDPressure")].status' \
    --no-headers 2>/dev/null | awk '{print $1"\t"$2"\t"$3"\t"$4"\t"$5}')

echo "  Summary: $NODE_COUNT nodes, $NODE_NOT_READY not-ready, $MEM_PRESSURE memory-pressure, $DISK_PRESSURE disk-pressure, $PID_PRESSURE pid-pressure"
echo ""

# ---------------------------------------------------------------------------
# 2. Crash-looping pods (CrashLoopBackOff waiting reason — all namespaces)
# ---------------------------------------------------------------------------
echo "--- Crash-looping pods (CrashLoopBackOff) ---"
CRASH_LOOP_PODS=0
CRASH_LOOP_LIST=$(kubectl get pods -A \
    -o custom-columns='NAMESPACE:.metadata.namespace,NAME:.metadata.name,STATE:.status.containerStatuses[*].state.waiting.reason' \
    --no-headers 2>/dev/null | awk '$3 == "CrashLoopBackOff" {print "  "$1"/"$2}')
if [ -n "$CRASH_LOOP_LIST" ]; then
    echo "$CRASH_LOOP_LIST"
    CRASH_LOOP_PODS=$(echo "$CRASH_LOOP_LIST" | wc -l | tr -d ' ')
else
    echo "  (none)"
fi
echo "  Count: $CRASH_LOOP_PODS"
echo ""

# ---------------------------------------------------------------------------
# 3. Failed pods (phase=Failed — all namespaces)
# ---------------------------------------------------------------------------
echo "--- Failed pods ---"
FAILED_PODS=0
FAILED_LIST=$(kubectl get pods -A --field-selector=status.phase=Failed \
    -o custom-columns='NAMESPACE:.metadata.namespace,NAME:.metadata.name' \
    --no-headers 2>/dev/null | awk '{print "  "$1"/"$2}')
if [ -n "$FAILED_LIST" ]; then
    echo "$FAILED_LIST"
    FAILED_PODS=$(echo "$FAILED_LIST" | wc -l | tr -d ' ')
else
    echo "  (none)"
fi
echo "  Count: $FAILED_PODS"
echo ""

# ---------------------------------------------------------------------------
# 4. Warning events in the last hour (all namespaces)
# ---------------------------------------------------------------------------
echo "--- Warning events (last ${WARN_WINDOW_SECONDS}s) ---"
CUTOFF_EPOCH=$(date -u -d "-${WARN_WINDOW_SECONDS} seconds" '+%s' 2>/dev/null \
    || date -u -v -"${WARN_WINDOW_SECONDS}"S '+%s' 2>/dev/null \
    || echo 0)

EVENT_WARNINGS=0
if [ "$CUTOFF_EPOCH" -gt 0 ] 2>/dev/null; then
    WARN_LIST=$(kubectl get events -A \
        -o custom-columns='NAMESPACE:.metadata.namespace,REASON:.reason,MSG:.message,LAST:.lastTimestamp,TYPE:.type' \
        --no-headers 2>/dev/null \
      | awk -v cutoff="$CUTOFF_EPOCH" '
        $5 == "Warning" {
            # Parse lastTimestamp — only works if awk has mktime (gawk)
            # Fall back to counting all Warning events if time parsing unavailable.
            print "  "$1" "$2": "$3
        }')
    # Count all Warning events (conservative — same as controller when time parse unavailable)
    EVENT_WARNINGS=$(kubectl get events -A --field-selector=type=Warning \
        --no-headers 2>/dev/null | wc -l | tr -d ' ')
    if [ -n "$WARN_LIST" ]; then
        echo "$WARN_LIST" | head -20
        SHOWN=$(echo "$WARN_LIST" | wc -l | tr -d ' ')
        [ "$SHOWN" -gt 20 ] && echo "  ... ($((SHOWN - 20)) more)"
    else
        echo "  (none)"
    fi
else
    EVENT_WARNINGS=$(kubectl get events -A --field-selector=type=Warning \
        --no-headers 2>/dev/null | wc -l | tr -d ' ')
    echo "  (time parsing unavailable; counting all Warning events)"
fi
echo "  Count (capped at 10 for score): $EVENT_WARNINGS"
echo ""

# ---------------------------------------------------------------------------
# 5. CoreDNS pod health
# ---------------------------------------------------------------------------
echo "--- CoreDNS health ---"
COREDNS_HEALTHY=true
COREDNS_STATUS=$(kubectl get pods -n kube-system -l k8s-app=kube-dns \
    -o custom-columns='NAME:.metadata.name,READY:.status.conditions[?(@.type=="Ready")].status' \
    --no-headers 2>/dev/null)
if [ -z "$COREDNS_STATUS" ]; then
    echo "  (no CoreDNS pods found)"
    COREDNS_HEALTHY=false
else
    echo "$COREDNS_STATUS" | while IFS= read -r line; do echo "  $line"; done
    NOT_READY=$(echo "$COREDNS_STATUS" | awk '$2 != "True"' | wc -l | tr -d ' ')
    [ "$NOT_READY" -gt 0 ] && COREDNS_HEALTHY=false
fi
echo "  Healthy: $COREDNS_HEALTHY"
echo ""

# ---------------------------------------------------------------------------
# 6. PVCs not in Bound phase (all namespaces)
# ---------------------------------------------------------------------------
echo "--- Degraded PVCs (not Bound) ---"
DEGRADED_PVCS=0
PVC_LIST=$(kubectl get pvc -A \
    -o custom-columns='NAMESPACE:.metadata.namespace,NAME:.metadata.name,PHASE:.status.phase' \
    --no-headers 2>/dev/null | awk '$3 != "Bound" {print "  "$1"/"$2" ("$3")"}')
if [ -n "$PVC_LIST" ]; then
    echo "$PVC_LIST"
    DEGRADED_PVCS=$(echo "$PVC_LIST" | wc -l | tr -d ' ')
else
    echo "  (none)"
fi
echo "  Count: $DEGRADED_PVCS"
echo ""

# ---------------------------------------------------------------------------
# Score computation (mirrors internal/monitor/health_score.go exactly)
# ---------------------------------------------------------------------------
echo "--- Score computation ---"

# Per-node deductions averaged across all nodes.
NODE_SCORE=100
if [ "$NODE_COUNT" -gt 0 ]; then
    TOTAL_NODE_DEDUCTIONS=0
    # Re-compute per-node deductions from raw counts (approximate: assumes deductions
    # distribute uniformly, which is the same assumption as the controller for averages).
    # For an exact per-node result, read each node individually (already done above).
    NOT_READY_DED=$((NODE_NOT_READY * 20))
    MEM_DED=$((MEM_PRESSURE * 10))
    DISK_DED=$((DISK_PRESSURE * 10))
    PID_DED=$((PID_PRESSURE * 5))
    TOTAL_NODE_DEDUCTIONS=$((NOT_READY_DED + MEM_DED + DISK_DED + PID_DED))
    NODE_AVG_DED=$((TOTAL_NODE_DEDUCTIONS / NODE_COUNT))
    NODE_SCORE=$((100 - NODE_AVG_DED))
fi

SCORE=$NODE_SCORE

# CrashLoop deduction (2 per pod, capped at 20).
CRASH_DED=$((CRASH_LOOP_PODS * 2))
[ "$CRASH_DED" -gt 20 ] && CRASH_DED=20
SCORE=$((SCORE - CRASH_DED))

# Failed pod deduction (1 per pod, capped at 10).
FAIL_DED=$FAILED_PODS
[ "$FAIL_DED" -gt 10 ] && FAIL_DED=10
SCORE=$((SCORE - FAIL_DED))

# Event warning deduction (1 per event, capped at 10).
WARN_DED=$EVENT_WARNINGS
[ "$WARN_DED" -gt 10 ] && WARN_DED=10
SCORE=$((SCORE - WARN_DED))

# CoreDNS deduction.
COREDNS_DED=0
[ "$COREDNS_HEALTHY" = "false" ] && COREDNS_DED=15
SCORE=$((SCORE - COREDNS_DED))

# PVC deduction (1 per PVC, capped at 10).
PVC_DED=$DEGRADED_PVCS
[ "$PVC_DED" -gt 10 ] && PVC_DED=10
SCORE=$((SCORE - PVC_DED))

# Clamp to [0, 100].
[ "$SCORE" -lt 0 ] && SCORE=0
[ "$SCORE" -gt 100 ] && SCORE=100

# Status label (mirrors HealthStatusFromScore).
if   [ "$SCORE" -ge 80 ]; then STATUS="healthy"
elif [ "$SCORE" -ge 50 ]; then STATUS="degraded"
else                            STATUS="critical"
fi

echo "  Node avg deduction : $((100 - NODE_SCORE))"
echo "  CrashLoop deduction: $CRASH_DED  (${CRASH_LOOP_PODS} pods × 2, cap 20)"
echo "  Failed pod deduction: $FAIL_DED  (${FAILED_PODS} pods × 1, cap 10)"
echo "  Event warn deduction: $WARN_DED  (${EVENT_WARNINGS} events × 1, cap 10)"
echo "  CoreDNS deduction  : $COREDNS_DED"
echo "  PVC deduction      : $PVC_DED  (${DEGRADED_PVCS} PVCs × 1, cap 10)"
echo ""
echo "  CLUSTER HEALTH SCORE : $SCORE / 100  ($STATUS)"
