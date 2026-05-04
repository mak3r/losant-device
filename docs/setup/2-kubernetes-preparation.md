# Step 2: Kubernetes Preparation

Create the provisioning Secret the controller needs to authenticate to the Losant REST API.

## Prerequisites

- `kubectl` connected to your target cluster (`kubectl get nodes` returns nodes)
- Application API Token from [Step 1](1-losant-application.md)

---

## Verify your kubeconfig context

```bash
kubectl config current-context
kubectl get nodes
```

Confirm the context points to the cluster you intend to deploy to before proceeding.

---

## Create the provisioning Secret

The Helm chart creates the `losant-system` namespace automatically. If you are pre-creating it:

```bash
kubectl create namespace losant-system --dry-run=client -o yaml | kubectl apply -f -
```

Create the provisioning Secret:

```bash
kubectl create secret generic losant-provisioning-credentials \
  --from-literal=api-token=<application-api-token-from-step-1> \
  -n losant-system
```

> **Key name matters**: The controller reads a single `api-token` key from this Secret. Any other key name causes a startup error.

---

## Next step

**[Step 3 → Deploy](3-deploy.md)** — install the operator with Helm and apply the LosantSync CR.
