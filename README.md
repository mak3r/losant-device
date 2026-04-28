# losant-device

A Kubernetes operator that monitors cluster health and reports to the [Losant IoT platform](https://losant.com), enabling a single-pane-of-glass view across hundreds or thousands of remote k3s/Kubernetes clusters.

## Overview

Remote Kubernetes clusters — especially k3s deployments at edge locations — are difficult to observe in aggregate. This project repurposes Losant's device management and dashboard model to provide:

- **Fleet view**: health status across all clusters at a glance
- **Cluster drill-down**: per-node metrics filtered by cluster or region
- **Node detail**: time-series CPU, memory, pod health, and condition flags
- **Rancher Manager link**: one click from any cluster view to the management UI

The operator runs inside each cluster and reports metrics to Losant on a configurable schedule (cron or interval), with resilience to intermittent network connectivity via the Losant Gateway Edge Agent (GEA).

## Architecture

```
┌─────────────────────────────────┐
│  k3s Cluster                    │
│                                 │
│  ┌─────────────────────────┐    │
│  │  losant-device-controller│    │
│  │  (this operator)         │    │
│  │   - watches nodes/pods   │    │
│  │   - computes health score│    │
│  │   - provisions devices   │────┼──── api.losant.com (REST)
│  │     via Losant REST API  │    │     provisioning only
│  └──────────┬──────────────┘    │
│             │ HTTP POST          │
│  ┌──────────▼──────────────┐    │
│  │  Losant GEA pod          │    │
│  │  (losant/edge-agent)     │────┼──── broker.losant.com (MQTT)
│  │   - 65k msg SQLite buffer│    │     metrics + state
│  │   - auto reconnect       │    │
│  └──────────────────────────┘    │
└─────────────────────────────────┘
```

**Two communication paths:**
- **REST API** (`api.losant.com`): device provisioning only — infrequent, tolerates latency
- **GEA HTTP** (in-cluster, port 8080): metric state reporting — high-frequency, backed by GEA's built-in offline buffer

See [docs/architecture.md](docs/architecture.md) for the full design.

## Losant Device Model

Each cluster maps to:
- One **Edge Compute device** (the GEA's identity) — cluster-level aggregate metrics
- One **peripheral device** per k8s node — per-node CPU, memory, pod health

Devices are tagged with `cluster_name`, `region`, `health_status`, and `rancher_url`, enabling Losant's dashboard context variables to filter across the fleet.

## Quick Start

### Prerequisites

- Go 1.23+
- kubebuilder v4
- kubectl + a running k3s cluster
- A Losant account with an Application created
- `gh` CLI (for repository management)

### Losant Setup

Before deploying, complete the one-time Losant setup described in [docs/losant-setup.md](docs/losant-setup.md).

### Deploy

```bash
# Install CRDs
make install

# Create the credentials secrets
kubectl create secret generic losant-gea-credentials \
  --from-literal=DEVICE_ID=<edge-compute-device-id> \
  --from-literal=ACCESS_KEY=<key> \
  --from-literal=ACCESS_SECRET=<secret> \
  -n losant-system

kubectl create secret generic losant-provisioning-credentials \
  --from-literal=device-id=<edge-compute-device-id> \
  --from-literal=access-key=<key> \
  --from-literal=access-secret=<secret> \
  -n losant-system

# Deploy operator + GEA
make deploy IMG=ghcr.io/mak3r/losant-device:latest

# Apply a LosantSync CR
kubectl apply -f config/samples/losant_v1alpha1_losantsync.yaml
```

### Helm

```bash
helm install losant-device helm/ \
  --namespace losant-system \
  --create-namespace \
  --set gea.credentials.deviceID=<id> \
  --set gea.credentials.accessKey=<key> \
  --set gea.credentials.accessSecret=<secret>
```

## Development

```bash
make generate      # regenerate DeepCopy methods
make manifests     # regenerate CRD/RBAC manifests
make build         # build controller binary to bin/manager
make test          # run unit + integration tests (no cluster required)
make e2e           # run end-to-end tests (requires KUBECONFIG)
make lint          # golangci-lint
make run           # run controller locally against ~/.kube/config
make docker-build  # build container image (set IMG=<image>:<tag>)
make docker-push   # push container image (set IMG=<image>:<tag>)
```

See [CLAUDE.md](CLAUDE.md) for agent development instructions and persona workflow rules.

## Documentation

- [Architecture](docs/architecture.md) — system design, device model, controller internals
- [Deployment](docs/deployment.md) — kustomize and Helm deployment paths, GEA manifest layout
- [Helm Values](helm/README.md) — full reference for all Helm chart values
- [Agent Workflow](docs/agent-workflow.md) — multi-agent branch strategy and persona rules
- [Losant Setup](docs/losant-setup.md) — one-time Losant Application and Edge Compute device configuration
- [Runbook](docs/runbook.md) — operational procedures: deploy, diagnose, schedule changes, e2e suite
- [Acceptance Criteria](docs/acceptance-criteria.md) — testable criteria for the full LosantSync reconciliation lifecycle

## License

Apache 2.0 — see [LICENSE](LICENSE).
