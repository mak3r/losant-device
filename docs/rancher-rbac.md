# Rancher RBAC — Security-Approved Scope

This document is the authoritative record of the minimum permissions approved by the security persona for the Rancher dynamic cluster connect/disconnect feature.

## Rancher Manager: Service Account Token Scope

The controller reads a shared Rancher service account token from the `rancher-credentials` Secret in the `losant-system` namespace. This token must be provisioned with the following **minimum** Rancher RBAC role:

| Resource | Verb | Endpoint |
|---|---|---|
| Clusters | `create` | `POST /v3/clusters` |
| Clusters | `get` | `GET /v3/clusters/{id}` |
| Clusters | `delete` | `DELETE /v3/clusters/{id}` |

No other Rancher API endpoints or verbs are permitted for this token. Per-cluster tokens are a v2 hardening option deferred to a future phase.

### Rancher Manager Role Definition

Create this Global Role in Rancher Manager before deploying the controller:

```yaml
apiVersion: management.cattle.io/v3
kind: GlobalRole
metadata:
  name: losant-device-controller
rules:
- apiGroups:
  - management.cattle.io
  resources:
  - clusters
  verbs:
  - create
  - get
  - delete
```

Bind this role to the service account used to generate the token:

```yaml
apiVersion: management.cattle.io/v3
kind: GlobalRoleBinding
metadata:
  name: losant-device-controller-binding
globalRoleName: losant-device-controller
userName: <rancher-service-account-user>
```

## Kubernetes RBAC: Approved Verbs for RancherSessionReconciler

The following `// +kubebuilder:rbac` markers are security-approved. The developer **must not** add any verb or resource not listed here without opening a `persona/security` + `type/security` issue first.

Copy these markers verbatim into the `RancherSessionReconciler` source file:

```go
// +kubebuilder:rbac:groups=losant.io,resources=ranchersessions,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=losant.io,resources=ranchersessions/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;create;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get
```

### Rationale

| Resource | Verbs | Reason |
|---|---|---|
| `ranchersessions` | `get,list,watch,create,update,patch` | Reconciler owns the CRD lifecycle |
| `ranchersessions/status` | `get,update,patch` | Standard status subresource pattern |
| `namespaces` | `get,create,delete` | Clean up `cattle-system` namespace on disconnect |
| `secrets` | `get` | Read `rancher-credentials` in `losant-system`; existing role already grants this |

The `secrets` marker restates the existing baseline permission (`get` is already in `role.yaml`). It is included here so the reconciler file documents its own dependencies explicitly. `make manifests` will not widen the role because the verb is already present in the committed baseline.

`delete` on `ranchersessions` is intentionally omitted. The reconciler updates status and lets the CR owner (a human or higher-level controller) control object lifecycle. If delete is needed in a future phase, open a `type/security` issue.
