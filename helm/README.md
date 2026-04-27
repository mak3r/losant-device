# losant-device Helm Chart

Installs the losant-device Kubernetes operator and the Losant Gateway Edge Agent (GEA) into a single namespace.

Chart version: `0.1.0` (skeleton — full template implementation in Phase 5).

## Install

```bash
helm install losant-device helm/ \
  --namespace losant-system \
  --create-namespace \
  --set gea.credentials.secretValues.deviceID=<id> \
  --set gea.credentials.secretValues.accessKey=<key> \
  --set gea.credentials.secretValues.accessSecret=<secret>
```

To use an existing credentials secret instead of inline values:

```bash
helm install losant-device helm/ \
  --namespace losant-system \
  --create-namespace \
  --set gea.credentials.existingSecret=losant-gea-credentials
```

## Values Reference

### `controller`

Settings for the losant-device operator Deployment.

| Key | Type | Default | Description |
|---|---|---|---|
| `controller.image.repository` | string | `ghcr.io/mak3r/losant-device` | Container image repository |
| `controller.image.tag` | string | `latest` | Image tag |
| `controller.image.pullPolicy` | string | `IfNotPresent` | Image pull policy (`Always`, `IfNotPresent`, `Never`) |
| `controller.replicaCount` | integer | `1` | Number of controller replicas (>1 requires `leaderElection: true`) |
| `controller.resources` | object | see below | CPU/memory requests and limits |
| `controller.leaderElection` | bool | `true` | Enable leader election (required for replicaCount > 1) |
| `controller.logLevel` | string | `info` | Log verbosity (`debug`, `info`, `warn`, `error`) |

Default resources:
```yaml
controller:
  resources:
    requests:
      cpu: 100m
      memory: 64Mi
    limits:
      cpu: 500m
      memory: 256Mi
```

### `gea`

Settings for the Losant Gateway Edge Agent Deployment.

| Key | Type | Default | Description |
|---|---|---|---|
| `gea.image.repository` | string | `losant/edge-agent` | GEA container image repository |
| `gea.image.tag` | string | `latest` | Image tag |
| `gea.image.pullPolicy` | string | `IfNotPresent` | Image pull policy |
| `gea.service.port` | integer | `8080` | ClusterIP Service port; the controller POSTs metric payloads here |
| `gea.storage.size` | string | `1Gi` | Size of the PVC used for the GEA's SQLite offline buffer |
| `gea.storage.storageClassName` | string | `""` | StorageClass for the PVC; empty string uses the cluster default |
| `gea.resources` | object | see below | CPU/memory requests and limits |
| `gea.credentials.existingSecret` | string | `""` | Name of an existing Secret containing `DEVICE_ID`, `ACCESS_KEY`, `ACCESS_SECRET`. When set, `secretValues` is ignored. |
| `gea.credentials.secretValues.deviceID` | string | `""` | Losant Edge Compute device ID (creates a new Secret; ignored if `existingSecret` is set) |
| `gea.credentials.secretValues.accessKey` | string | `""` | Losant access key (creates a new Secret; ignored if `existingSecret` is set) |
| `gea.credentials.secretValues.accessSecret` | string | `""` | Losant access secret (creates a new Secret; ignored if `existingSecret` is set) |

Default resources:
```yaml
gea:
  resources:
    requests:
      cpu: 100m
      memory: 128Mi
    limits:
      cpu: 500m
      memory: 512Mi
```

#### Credentials: `existingSecret` vs `secretValues`

Use `existingSecret` when the credentials secret is managed outside Helm (e.g., by Vault, External Secrets Operator, or `kubectl create secret`). This avoids storing sensitive values in Helm release history.

Use `secretValues` for quick installs or when Helm manages the full release lifecycle. The chart creates the secret from these values and owns it on `helm uninstall`.

### `rbac`

| Key | Type | Default | Description |
|---|---|---|---|
| `rbac.create` | bool | `true` | Create the ClusterRole and ClusterRoleBinding for the controller |

Set to `false` if your cluster uses an external RBAC management system and you will apply the role manifests separately.

### `serviceAccount`

| Key | Type | Default | Description |
|---|---|---|---|
| `serviceAccount.create` | bool | `true` | Create a dedicated ServiceAccount for the controller |
| `serviceAccount.annotations` | object | `{}` | Annotations to add to the ServiceAccount (e.g., for IRSA or Workload Identity) |

### `namespace`

| Key | Type | Default | Description |
|---|---|---|---|
| `namespace.create` | bool | `true` | Create the target namespace |
| `namespace.name` | string | `losant-system` | Namespace to deploy into |

## Required Values

`controller.image.repository` and `gea.image.repository` are required by the schema. All other values have defaults.

At least one of `gea.credentials.existingSecret` or non-empty `gea.credentials.secretValues.*` must be provided for the GEA pod to authenticate to Losant.

## Upgrading

```bash
helm upgrade losant-device helm/ --namespace losant-system
```

## Uninstalling

```bash
helm uninstall losant-device --namespace losant-system
```

Note: CRDs and the `LosantSync` CRs are not removed by `helm uninstall`. Delete them manually if needed:

```bash
kubectl delete losantsyncs --all
kubectl delete crd losantsyncs.losant.io
```
