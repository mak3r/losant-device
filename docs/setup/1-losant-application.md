# Step 1: Losant Application Setup

Create the Losant Application and API Token the controller needs to provision devices.

## Prerequisites

- A Losant account at [app.losant.com](https://app.losant.com)

---

## Create a Losant Application

1. Log into [app.losant.com](https://app.losant.com)
2. Click **Applications** → **Add Application**
3. Give it a name (e.g., `k8s-cluster-monitor`)
4. Note the **Application ID** — this goes into `LosantSync.spec.applicationID`

---

## Create an Application API Token

The controller authenticates to the Losant REST API using an Application API Token — **not** a device access key. Device access keys are MQTT-only credentials and cannot make REST API calls.

1. In your Application → **Security** → **API Tokens** → **Add API Token**
2. Give it a name (e.g., `losant-device-controller`)
3. Set the expiration appropriate for your security policy (or leave as no expiration)
4. Note the **API Token** value — it is shown only once

---

## Next step

**[Step 2 → Kubernetes Preparation](2-kubernetes-preparation.md)** — create the provisioning Secret in your cluster.
