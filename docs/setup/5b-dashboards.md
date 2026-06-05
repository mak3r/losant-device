# Step 5b: Create Losant Dashboards

`scripts/create-dashboards.go` provisions two pre-built Losant dashboards via the REST API:

- **k8s-cluster-detail** — per-cluster view with node health, pod counts, and optional Rancher session controls
- **k8s-fleet-overview** — fleet-wide view linking out to individual cluster detail dashboards

---

## Prerequisites

- Go 1.21 or later installed locally
- A Losant Application API Token with `Dashboard: Create`, `Dashboard: Read`, and `Dashboard: Delete` permissions
- `LOSANT_APP_ID` and `LOSANT_API_TOKEN` set in your environment (or passed as flags)

```bash
export LOSANT_APP_ID=<your-application-id>
export LOSANT_API_TOKEN=<your-api-token>
```

---

## Basic Usage

Run the script from the repository root to create both dashboards immediately:

```bash
go run scripts/create-dashboards.go
```

Or pass credentials as flags:

```bash
go run scripts/create-dashboards.go \
  --app-id <LOSANT_APP_ID> \
  --api-token <LOSANT_API_TOKEN>
```

On success the script prints the URL for each dashboard:

```
Creating Cluster Detail dashboard (step 1 of 2)...
Creating Fleet Overview dashboard (step 2 of 2)...

=== Done ===
Fleet Overview:  https://app.losant.com/dashboards/<id>
Cluster Detail:  https://app.losant.com/dashboards/<id>
```

Open the printed URLs in your browser to confirm both dashboards appear in the Losant UI. See [Step 5 — Verify](5-verify.md#dashboard-quick-start) for the expected block layout.

---

## Rancher Connect/Disconnect Buttons (Block 2L)

> **Note:** `--rancher-connect-key` and `--rancher-disconnect-key` are not yet available in the current release. They are being added in [PR #486](https://github.com/mak3r/losant-device/pull/486). Until that PR merges, run without those two flags — Block 2L will be omitted and can be added later with `--force`.

The Cluster Detail dashboard includes an optional **Rancher Session Controls** block (Block 2L) with connect and disconnect buttons. This block is only provisioned when all three Rancher flags are supplied.

### Required flags

| Flag | Environment variable | Description |
|---|---|---|
| `--rancher-workflow-id` | *(none)* | Losant Workflow ID that handles connect/disconnect |
| `--rancher-connect-key` | `LOSANT_RANCHER_CONNECT_KEY` | Virtual button node key for the connect action *(pending PR #486)* |
| `--rancher-disconnect-key` | `LOSANT_RANCHER_DISCONNECT_KEY` | Virtual button node key for the disconnect action *(pending PR #486)* |

### Finding the virtual button keys

Virtual button keys are auto-generated node IDs assigned by Losant when the workflow is created. They are not visible in the Losant UI — you must export the workflow JSON to find them.

1. In Losant, open your Rancher session workflow
2. Click **Export** → **Download JSON**
3. Search the JSON for `"type": "virtualButton"` — each node has a `"key"` field:

```json
{
  "type": "virtualButton",
  "key": "abcdef123456",
  ...
}
```

4. Identify which key belongs to the connect action and which belongs to disconnect (check the node label or surrounding context)

### Running with Rancher flags

```bash
go run scripts/create-dashboards.go \
  --rancher-workflow-id <workflow-id> \
  --rancher-connect-key <connect-key> \
  --rancher-disconnect-key <disconnect-key>
```

If any of the three flags are missing, the script completes successfully but prints a note:

```
Note: Block 2L (Rancher buttons) was omitted — missing: --rancher-workflow-id
Re-run with --force and all three flags to add it.
```

---

## Idempotency

The script is safe to re-run. If a dashboard with the same canonical name already exists, the script skips creation and prints:

```
  [skip] dashboard "k8s-cluster-detail" already exists: https://app.losant.com/dashboards/<id>
```

No changes are made to existing dashboards in this mode.

---

## `--force` Caveat

`--force` deletes the existing dashboard and recreates it from scratch:

```bash
go run scripts/create-dashboards.go --force \
  --rancher-workflow-id <workflow-id> \
  --rancher-connect-key <connect-key> \
  --rancher-disconnect-key <disconnect-key>
```

**Any customizations made in the Losant UI after the initial run will be lost.** Prefer making UI changes directly in Losant rather than using `--force` once dashboards are in use.

Use `--force` only when:
- Adding Block 2L for the first time (Rancher flags were not available on the initial run)
- Resetting dashboards to the script-defined baseline after significant UI divergence

---

## Full Flag Reference

| Flag | Default | Description |
|---|---|---|
| `--app-id` | `$LOSANT_APP_ID` | Losant Application ID |
| `--api-token` | `$LOSANT_API_TOKEN` | Losant Application API Token |
| `--force` | false | Delete and recreate dashboards that already exist |
| `--rancher-workflow-id` | *(empty)* | Workflow ID for Rancher virtual button triggers |
| `--rancher-connect-key` | `$LOSANT_RANCHER_CONNECT_KEY` | Virtual button key for connect action *(pending PR #486)* |
| `--rancher-disconnect-key` | `$LOSANT_RANCHER_DISCONNECT_KEY` | Virtual button key for disconnect action *(pending PR #486)* |
