Set up this Go workspace for a new persona session. Follow these steps exactly, checking state before running anything.

## Step 1 — Verify working directory

Confirm we are in the `losant-device` repo root (a `go.mod` file must exist and the module line must read `github.com/mak3r/losant-device`). If not, stop and tell the user to `cd` to the correct directory.

## Step 2 — Go module dependencies

Check whether dependencies are already downloaded:
```bash
go mod verify 2>&1 | tail -5
```
- If the output ends with `all modules verified`, dependencies are present — skip to Step 3.
- Otherwise run:
  ```bash
  go mod download
  ```
  Accept any prompts automatically; do not ask the user unless `go mod download` itself prompts for credentials.

## Step 3 — Local toolchain binaries (`./bin/`)

The project installs four tools into `./bin/`. Check each one:

| Binary glob | Make target |
|---|---|
| `bin/controller-gen-*` | `make controller-gen` |
| `bin/kustomize-*` | `make kustomize` |
| `bin/setup-envtest-*` | `make envtest` |
| `bin/golangci-lint-*` | `make golangci-lint` |

Run `ls bin/` (or note the directory doesn't exist yet). For each missing tool, run its `make` target. You can run all missing targets in a single `make` invocation, for example:
```bash
make controller-gen kustomize envtest golangci-lint
```
Only include targets whose binary glob is absent.

## Step 4 — Envtest Kubernetes assets

The test suite needs a specific Kubernetes API server binary bundle. Check whether it has been downloaded:
```bash
./bin/setup-envtest list --bin-path ./bin 2>/dev/null | grep "1.31.0" || echo "not downloaded"
```
If not downloaded, fetch it:
```bash
./bin/setup-envtest use 1.31.0 --bin-path ./bin
```
This may take a moment; wait for it to complete.

## Step 5 — Smoke check

Run a quick build verification to confirm everything is wired up:
```bash
go build ./...
```
Report success or any errors to the user. If errors occur, diagnose them and fix (e.g. missing `go mod tidy`) before declaring setup complete.

## Step 6 — Report

Print a concise summary:
- Go version (`go version`)
- Module verification status
- Which tools were already present vs. freshly installed
- Whether envtest assets were already present vs. downloaded
- Whether `go build ./...` passed

Do not ask the user for confirmation between steps unless a command exits non-zero with an error that requires a decision.
