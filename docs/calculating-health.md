# Health Score — Calculation and Debugging

The operator assigns every cluster and every node a **health score** — an integer from 0 to 100 that summarises the observed condition of that resource. The score is reported to Losant as the `health_score` data field and mapped to a `health_status` tag (`healthy`, `degraded`, or `critical`) on each Losant device.

This document explains exactly how the score is computed and how to interrogate a cluster to understand a score that is lower than expected.

---

## Score Thresholds

| Range | `health_status` tag | What it means |
|---|---|---|
| 80 – 100 | `healthy` | All major components operating normally; minor issues may exist |
| 50 – 79 | `degraded` | Noticeable problems present; cluster is functional but impaired |
| 0 – 49 | `critical` | Severe failure conditions; immediate attention required |

**Important:** equal score values do not always represent equal health. A score of 75 caused by a single crashlooping pod has a different operational impact than a score of 75 caused by averaged node memory pressure. Always look at which deductions are active, not just the numeric total.

---

## Cluster Health Score

Source of truth: [`internal/monitor/health_score.go`](../internal/monitor/health_score.go) — `ComputeClusterHealthScore`.

```
base = 100

Per-node deductions (averaged across all nodes):
  -20  node not Ready
  -10  MemoryPressure condition True
  -10  DiskPressure condition True
  -5   PIDPressure condition True

Cluster-level deductions:
  -2 per crashlooping pod  (capped at -20)
  -1 per failed pod        (capped at -10)
  -1 per warning event     (capped at -10)
  -15 CoreDNS not healthy
  -1 per degraded PVC      (capped at -10)

final = clamp(base - sum(deductions), 0, 100)
```

### Per-node deductions and averaging

Node conditions are averaged across the fleet. On a 3-node cluster where one node has MemoryPressure, the effective deduction is `10 / 3 ≈ 3` points — not 10. This means a single bad node on a large cluster causes a smaller cluster-level penalty than the same condition on a single-node cluster.

---

## Node Health Score

Source of truth: [`internal/monitor/health_score.go`](../internal/monitor/health_score.go) — `ComputeNodeHealthScore`.

Each node receives its own score using the same conditions, but **without averaging** — the full deduction applies regardless of cluster size.

```
base = 100
  -20  node not Ready
  -10  MemoryPressure condition True
  -10  DiskPressure condition True
  -5   PIDPressure condition True

final = clamp(base - sum(deductions), 0, 100)
```

A node can score no lower than 0 and no higher than 100.

---

## Debugging a Low Score

Work through each factor in order of maximum possible impact.

### 1. Node conditions (up to -45 per node before averaging)

```bash
kubectl get nodes
kubectl describe nodes | grep -A5 "Conditions:"
```

Look for conditions where `Status` is not `False` / `Unknown`:
- `Ready=False` or `Ready=Unknown` → -20 per node
- `MemoryPressure=True` → -10 per node
- `DiskPressure=True` → -10 per node
- `PIDPressure=True` → -5 per node

Remember: on a multi-node cluster the cluster score deducts the **average** of these values across all nodes.

### 2. Crashlooping pods (up to -20)

```bash
kubectl get pods -A | grep -E 'CrashLoopBackOff|Error|OOMKilled'
```

Each crashlooping pod costs 2 points, capped at 20 total (i.e., 10 or more crashlooping pods all result in the same -20).

### 3. CoreDNS health (-15 if unhealthy)

```bash
kubectl get deployment coredns -n kube-system
kubectl get pods -n kube-system -l k8s-app=kube-dns
```

CoreDNS is considered healthy when at least one pod in the `kube-system` namespace with label `k8s-app=kube-dns` is Running and Ready. An unhealthy CoreDNS costs -15 regardless of cluster size.

### 4. Warning events (up to -10)

```bash
kubectl get events -A --field-selector=type=Warning --sort-by='.lastTimestamp'
```

The operator counts Warning events that occurred in the past hour. Each event costs 1 point, capped at 10. High event volume often indicates a root cause in another category (e.g., a crashlooping pod generates repeated Warning events).

### 5. Failed pods (up to -10)

```bash
kubectl get pods -A --field-selector=status.phase=Failed
```

Each pod in `Failed` phase costs 1 point, capped at 10. Failed pods differ from crashlooping pods — they have stopped and will not restart automatically.

### 6. Degraded PVCs (up to -10)

```bash
kubectl get pvc -A | grep -v Bound
```

Any PVC not in `Bound` phase costs 1 point, capped at 10. Common non-Bound statuses are `Pending` (no matching PV) and `Lost` (backing storage gone).

---

## Example: Score of 93

Starting from 100, a score of 93 represents 7 points of deductions. Likely candidates on a healthy-looking cluster:

- 7 Warning events in the last hour → -7 (and no other factors)
- Or: 1 crashlooping pod (-2) + 5 warning events (-5)
- Or: 1 failed pod (-1) + MemoryPressure on one node of a 3-node cluster (-3) + 3 warning events (-3)

Check events first — they are the most common cause of small deductions on otherwise healthy clusters:

```bash
kubectl get events -A --field-selector=type=Warning --sort-by='.lastTimestamp' | tail -20
```

---

## Worked Example: 3-Node Cluster, Score 72

Suppose your cluster reports `health_score=72` with `health_status=degraded`.

| Factor | Value | Deduction |
|---|---|---|
| Node MemoryPressure on 1 of 3 nodes | avg: 10/3 ≈ 3 | -3 |
| 3 crashlooping pods | 3 × 2 = 6 | -6 |
| 8 warning events | capped at 10, actual 8 | -8 |
| CoreDNS healthy | — | 0 |
| No failed pods | — | 0 |
| No degraded PVCs | — | 0 |
| **Total deduction** | | **-17** |
| **Score** | 100 - 17 | **83** |

If the actual score is 72 rather than 83, there are additional active factors not accounted for — re-run each check above to find them.

---

## Score vs. Status: Not a Simple Equivalence

Two clusters at score 75 can have very different operational urgency:

| Cluster A — score 75 | Cluster B — score 75 |
|---|---|
| 25 warning events (capped -10) + 1 node MemoryPressure on 3-node cluster (-3) + 12 crashloop pods (capped -20, but wait — that puts us at 100-10-3-20 = 67) | Actually 2 failed pods (-2) + 13 warning events (capped -10) + 1 crashloop pod (-2) + DiskPressure on 1/3 nodes (-3) + 8 degraded PVCs (capped -8) |

The first scenario may indicate a noisy but stable cluster. The second — lost storage — requires immediate investigation. Always inspect the individual factors, not just the total.
