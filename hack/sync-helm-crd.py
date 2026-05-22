#!/usr/bin/env python3
"""Sync spec from generated CRD into helm/templates/crd.yaml.

Preserves Helm-specific metadata (helm.sh/resource-policy, label templates)
by splicing at the 'spec:' boundary rather than copying the whole file.
"""
import sys

HELM_CRD = "helm/templates/crd.yaml"
GEN_CRD = "config/crd/bases/losant.io_losantsyncs.yaml"

with open(HELM_CRD) as f:
    helm_lines = f.readlines()

with open(GEN_CRD) as f:
    gen_lines = f.readlines()

helm_spec = next((i for i, l in enumerate(helm_lines) if l.startswith("spec:")), None)
gen_spec = next((i for i, l in enumerate(gen_lines) if l.startswith("spec:")), None)

if helm_spec is None:
    print(f"ERROR: 'spec:' not found in {HELM_CRD}", file=sys.stderr)
    sys.exit(1)
if gen_spec is None:
    print(f"ERROR: 'spec:' not found in {GEN_CRD}", file=sys.stderr)
    sys.exit(1)

with open(HELM_CRD, "w") as f:
    f.writelines(helm_lines[:helm_spec])
    f.writelines(gen_lines[gen_spec:])

print(f"{HELM_CRD} spec synced from {GEN_CRD}")
