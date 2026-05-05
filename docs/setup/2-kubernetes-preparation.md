# Step 2: Kubernetes Preparation

Verify that `kubectl` is connected to your target cluster and gather the information you need before deploying.

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

## About the provisioning Secret

The controller authenticates to the Losant REST API using a Kubernetes Secret named `losant-provisioning-credentials`. You will create this Secret in Step 3, after Helm installs the operator and creates the `losant-system` namespace.

> **Do not pre-create the `losant-system` namespace.** Helm creates it with proper ownership metadata during `helm install`. Pre-creating the namespace causes an `invalid ownership metadata` error that prevents installation.

The Secret requires exactly one key:

| Key | Value |
|---|---|
| `api-token` | Application API Token from Step 1 |

> **Key name matters**: The controller reads the `api-token` key specifically. Any other key name causes a startup error.

---

## Next step

**[Step 3 → Deploy](3-deploy.md)** — install the operator with Helm, create the provisioning Secret, and apply the LosantSync CR.
