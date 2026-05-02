# Setup Guide

Complete setup for the losant-device operator requires steps in both the Losant UI and the Kubernetes cluster. The steps below must be followed **in order** — some Losant UI steps require the cluster to be running first.

## Sequence

| Step | What happens | Requires cluster? |
|---|---|---|
| [1. Losant device setup](1-losant-device-setup.md) | Create Losant Application, API token, and provisioning Kubernetes secret (device and GEA credentials are auto-provisioned by the controller) | No — UI + kubectl |
| [2. Cluster deployment](2-cluster-deployment.md) | Deploy the operator and GEA pod to the cluster | Yes |
| [3. Losant workflow setup](3-losant-workflow-setup.md) | Apply LosantSync CR, wait for Edge Compute device, then create and deploy Edge Workflow | Yes — GEA must be Online |
| [4. Operator configuration](4-operator-configuration.md) | (Optional) Device Recipe and dashboard setup | Yes |
| [5. Teardown and reset](5-teardown.md) | Remove all cluster resources and Losant UI objects for a clean slate | Yes (for cluster steps) |

> **Why this order?** Steps 1 and 2 are independent — you can create the Losant device and deploy the cluster in either order. Step 3 requires the GEA pod to have connected to Losant at least once (so Losant has an agent version on record). Step 4 requires the operator to be running.

## Start here

**[Step 1 → Losant device setup](1-losant-device-setup.md)**
