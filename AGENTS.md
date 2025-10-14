# Agent guide for my-csi-driver

This document is a quick, task-focused guide for an automated contributor (or human acting like one) to work effectively in this repository.

## Snapshot

- Repository: github.com/ktsakalozos/my-csi-driver
- Language/Tooling: Go (module), Docker, Helm, Kubernetes
- Go: 1.24.x (module path `github.com/ktsakalozos/my-csi-driver`)
- Entrypoint: `cmd/driver/main.go`
- Driver flags: `--endpoint`, `--nodeid`, `--drivername`, `--working-mount-dir`, `--mode` (controller|node|both)
- Env fallbacks: `NODE_NAME` (for nodeid), `CSI_BACKING_DIR` (overrides backing dir)
- Default backing dir: `/var/lib/my-csi-driver`
- Helm chart: `charts/my-csi-driver`
  - Controller: Deployment + external-provisioner
  - Node: DaemonSet + node-driver-registrar (privileged, mounts /dev)
  - CSIDriver + RBAC + optional StorageClass (default-enabled)
- CI e2e: `.github/workflows/e2e-kind.yaml`
  - Triggers: push (main), pull_request (main)
  - Image push to GHCR only on push events (`if: github.event_name == 'push'`).

## Common tasks

### Build and push image

- Build
  ```bash
  make build IMG=ghcr.io/<owner>/my-csi-driver:dev
  ```
- Push (requires registry login)
  ```bash
  make push IMG=ghcr.io/<owner>/my-csi-driver:dev
  ```

Notes
- `IMG` can also be derived from `REGISTRY`, `IMAGE_NAME`, and `IMAGE_TAG` Make variables.
- CI uses `IMG=ghcr.io/${{ github.repository_owner }}/my-csi-driver:${{ github.sha }}`.

### Run unit and integration tests

- Unit tests
  ```bash
  make test
  ```
- Integration tests (controller + node flows)
  ```bash
  make integration-test
  ```

Requirements for integration tests
- Root privileges and tools (losetup, mkfs.ext4, mount/umount) on the host.
- The test will create/truncate backing files; ensure there is space under the backing dir.

### Reproduce CI e2e locally with Kind (outline)

1) Build the image locally
   ```bash
   make build IMG=my-csi-driver:dev
   ```
2) Create a Kind cluster
   ```bash
   kind create cluster --name csi-e2e
   ```
3) Load the local image into Kind
   ```bash
   kind load docker-image my-csi-driver:dev --name csi-e2e
   ```
4) (Safety) Ensure no default StorageClass remains from local-path-provisioner
   ```bash
   for sc in $(kubectl get storageclass -o name); do
     kubectl annotate "$sc" storageclass.kubernetes.io/is-default-class- --overwrite || true
   done
   ```
5) Install the Helm chart
   ```bash
   helm upgrade --install my-csi-driver ./charts/my-csi-driver \
     --set image.repository=<registry-or-local>/my-csi-driver \
     --set image.tag=dev \
     --set storageClass.create=true \
     --set storageClass.default=true \
     --set backingDir=/var/lib/my-csi-driver
   ```
   - For a pure local image (no registry), set `image.repository` to just `my-csi-driver` and `image.tag=dev` to match the local image name/tag you loaded.
6) Wait for readiness
   ```bash
   kubectl rollout status ds/my-csi-driver --timeout=320s
   kubectl rollout status deploy/my-csi-driver-controller --timeout=320s
   ```
7) Smoke test (PVC + Pod)
   ```bash
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
         storage: 1Mi
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
   kubectl wait --for=jsonpath='{.status.phase}'=Bound pvc/demo-pvc --timeout=120s
   kubectl wait --for=condition=Ready pod/demo-app --timeout=120s || true
   kubectl logs pod/demo-app
   ```

Cleanup
```bash
helm uninstall my-csi-driver || true
kind delete cluster --name csi-e2e || true
```

## CI/CD behavior

- Workflow: `.github/workflows/e2e-kind.yaml`
- On pull_request: image is built and loaded into Kind, but NOT pushed to GHCR.
- On push (to main): image is built and pushed to GHCR, then e2e tests run.
- Helm values used in CI
  - `image.repository=ghcr.io/<owner>/my-csi-driver`
  - `image.tag=${{ github.sha }}`
  - `storageClass.create=true`, `storageClass.default=true`
  - `backingDir=/var/lib/my-csi-driver`

## Coding conventions

- Use `make fmt` and `make vet` locally.
- Unit tests should avoid privileged operations; integration tests may require them.
- Logging uses `k8s.io/klog/v2`; default is to log to stderr (set in `main.go`).
- Respect the flags and environment precedence for `nodeid` and `CSI_BACKING_DIR`.

## Helm and runtime notes

- Node DaemonSet runs privileged and mounts host `/dev` for loop device operations.
- Controller mounts the same host backing directory to manage sparse files shared with nodes.
- CSIDriver sets `attachRequired=false`, `storageCapacity=true`, `fsGroupPolicy=File`.
- Default StorageClass is named `my-csi-driver-default` (configurable via values).

## Safety checks for agents

- Do not push images from PR contexts unless explicitly required; follow the CI condition.
- When testing on Kind, remove or neutralize any pre-existing default StorageClass to avoid conflicts.
- Ensure `--nodeid` is set (the chart uses `NODE_NAME` env); the binary also falls back to hostname.
- Verify loop device availability for node path tests and deployments.

## PR checklist (quick)

- [ ] Code formatted and vetted (`make fmt && make vet`)
- [ ] Unit tests pass locally (`make test`)
- [ ] Integration tests pass locally if touched node/controller paths (`make integration-test`)
- [ ] Helm chart values and templates updated if flags/args change
- [ ] CI e2e passes on PR
- [ ] Docs updated (README and/or this guide) if behavior changed

## Troubleshooting quick picks

- PVC not binding: confirm StorageClass name and provisioner match driver name; ensure default SC conflicts are removed.
- Node errors about `driverNodeID must not be empty`: verify `NODE_NAME` wiring or set `--nodeid` explicitly.
- Loop device failures: ensure privileged pod and host `/dev` mount; check image contains `losetup` and filesystem tools.
- Controller RBAC issues: verify ClusterRole rules for PV/PVC/SC/leases/pods/nodes align with templates.
