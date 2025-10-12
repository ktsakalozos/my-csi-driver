# my-csi-driver

A simple, educational Container Storage Interface (CSI) driver that provisions Kubernetes PersistentVolumes using raw files on each node. This repo is great for learning CSI control and node flows end‑to‑end and includes a Helm chart and CI that validate dynamic provisioning on a Kind cluster.

This project is inspired by prior work in the OpenEBS ecosystem around LocalPV Rawfile, but reimplements the idea in Go rather than Python.

## What it does

- Controller: Creates a sparse backing file per PVC under a configurable host path (default: `/var/lib/my-csi-driver`).
- Node: Attaches the backing file via loop device (losetup), formats it (ext4 by default), and mounts it to the pod’s target path.
- Sidecars: Uses external-provisioner (controller) and node-driver-registrar (node) to integrate with Kubernetes.
- StorageClass: A default StorageClass is included for quick testing.

> Note: This is not production-grade storage. It’s intended for demos, learning, CI, and local development.

## Features

- Split controller and node components (Deployment + DaemonSet) via Helm.
- Leader election and CSIStorageCapacity publishing (provisioner) when deployed with Helm.
- Minimal RBAC split between controller and node service accounts.
- Works on Kind and typical Kubernetes clusters with hostPath access.

## Requirements

- Kubernetes 1.25+
- Node kernel support for loop devices and access to `/dev/loop*`
- Container image includes `util-linux` and `e2fsprogs` (provided in the Dockerfile)

## Install

### Option A: Helm (recommended)

This deploys:
- CSIDriver
- Controller Deployment (driver + external-provisioner)
- Node DaemonSet (driver + node-driver-registrar)
- RBAC (split SAs/roles)
- StorageClass (optional, on by default)

Values of interest:
- `backingDir` (default `/var/lib/my-csi-driver`)
- `image.repository`, `image.tag`

Example install after building/pushing the image:

```
helm upgrade --install my-csi-driver ./charts/my-csi-driver \
	--set image.repository=ghcr.io/<owner>/my-csi-driver \
	--set image.tag=<sha-or-tag> \
	--set storageClass.create=true \
	--set storageClass.default=true \
	--set backingDir=/var/lib/my-csi-driver
```

### Option B: Raw manifests (single DaemonSet)

For quick tests without Helm, `deploy/deploy.yaml` installs a single DaemonSet with both controller and node in the same pod. It now passes a NodeID using the Downward API and sets the driver mode explicitly.

```
kubectl apply -f deploy/deploy.yaml
```

If you also want a sample PVC + Pod:

```
kubectl apply -f deploy/storage-test.yaml
```

## Quickstart (Helm)

1) Install the driver (see above).

2) Verify pods:

```
kubectl get pods -l app.kubernetes.io/name=my-csi-driver
kubectl get pods -l app.kubernetes.io/component=controller
kubectl get pods -l app.kubernetes.io/component=node
```

3) Create a PVC and a pod that writes/reads data:

```
cat <<'YAML' | kubectl apply -f -
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
	name: demo-pvc
spec:
	accessModes: [ "ReadWriteOnce" ]
	storageClassName: my-csi-driver-default
	resources:
		requests:
			storage: 1Gi
---
apiVersion: v1
kind: Pod
metadata:
	name: demo-app
spec:
	restartPolicy: Never
	containers:
	- name: app
		image: alpine:3.19
		command: ["/bin/sh","-c","echo hello > /data/hello && cat /data/hello && sleep 2"]
		volumeMounts:
		- name: data
			mountPath: /data
	volumes:
	- name: data
		persistentVolumeClaim:
			claimName: demo-pvc
YAML
```

4) Wait for PVC bound and pod success:

```
kubectl wait --for=jsonpath='{.status.phase}'=Bound pvc/demo-pvc --timeout=300s
kubectl wait --for=jsonpath='{.status.phase}'=Succeeded pod/demo-app --timeout=300s
```

## Development

Build the image:

```
make build IMG=ghcr.io/<owner>/my-csi-driver:dev
```

Push (optional):

```
make push IMG=ghcr.io/<owner>/my-csi-driver:dev
```

## Testing

Unit and integration tests are included. Integration tests exercise controller and node paths separately. The node test requires root and system tools (losetup, mkfs.ext4, mount, umount) and will be skipped otherwise.

Run locally:

```
make test         # unit tests
make integration-test  # controller + node integration (node requires sudo/tools)
```

CI: See `.github/workflows/e2e-kind.yaml` for a full Kind-based e2e that:
- Removes the default local-path StorageClass
- Installs the chart, waits for readiness
- Verifies controller/node modes and RBAC
- Creates a PVC + Pod and validates dynamic provisioning

## Configuration

- Backing directory: set `CSI_BACKING_DIR` env var or the Helm value `backingDir`. Defaults to `/var/lib/my-csi-driver`.
- Driver name: `--drivername` flag (defaults to `my-csi-driver`). Must match the `CSIDriver` and StorageClass provisioner.
- Endpoint: `--endpoint` flag (defaults to a kubelet plugin path).
- Mode: `--mode=controller|node|both`.
- Node ID: `--nodeid` flag; if omitted the driver falls back to `NODE_NAME` env or container hostname.

## Troubleshooting

- Registrar error: `driverNodeID must not be empty`
	- Ensure `--nodeid=$(NODE_NAME)` is passed or that `NODE_NAME` env is set. The driver also falls back to the hostname now, but explicit is better.

- Loop device setup fails (losetup exit 1)
	- Confirm the node pod is privileged and mounts `/dev` from the host.
	- Ensure the backing file exists on the host (controller must also mount the same hostPath when split) and is not zero bytes.
	- Check logs for detailed losetup stderr (the driver logs it now).

- Controller permission errors (for pods, nodes, leases)
	- Verify the controller ClusterRole includes read permissions for pods/nodes and CRUD for leases.

- No StorageClass or PVC doesn’t bind
	- Confirm StorageClass name matches your chart values and provisioner name reflects the driver name.

## Project layout

- `cmd/driver/main.go` — flags and bootstrap
- `pkg/rawfile/` — CSI Identity, Controller, and Node servers
- `charts/my-csi-driver` — Helm chart (controller Deployment, node DaemonSet, RBAC, CSIDriver, StorageClass)
- `deploy/` — raw YAML for non-Helm quick tests
- `test/` — unit and integration tests

## License

Apache License 2.0 — see `LICENSE` for details. This project is intended for education and experimentation.
