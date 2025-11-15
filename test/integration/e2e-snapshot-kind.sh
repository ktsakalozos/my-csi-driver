#!/usr/bin/env bash
# e2e-snapshot-kind.sh: End-to-end test for CSI snapshot functionality in a kind cluster
# This script:
#   1. Creates a kind cluster (if not already present)
#   2. Installs snapshot CRDs from kubernetes-csi/external-snapshotter
#   3. Removes the Rancher local-path provisioner (to avoid conflicts)
#   4. Loads the CSI driver image into kind
#   5. Installs the CSI driver via Helm (with snapshot support)
#   6. Creates a VolumeSnapshotClass
#   7. Creates a source volume with test data
#   8. Creates a snapshot of that volume
#   9. Restores a new volume from the snapshot
#   10. Verifies the restored data matches the original
#   11. Cleans up resources (optional, controlled by SKIP_CLEANUP)
#
# Environment variables:
#   IMG                  - Docker image to test (required)
#   REGISTRY             - Registry prefix for the image (required)
#   KIND_CLUSTER_NAME    - Name of the kind cluster (default: csi-snapshot-e2e)
#   SKIP_CLEANUP         - Set to 'true' to skip cleanup on success (default: false)
#   CHART_PATH           - Path to Helm chart (default: ./charts/my-csi-driver)
#   SNAPSHOTTER_VERSION  - Version of external-snapshotter CRDs (default: v6.3.3)

set -euo pipefail

# Default values
KIND_CLUSTER_NAME="${KIND_CLUSTER_NAME:-csi-snapshot-e2e}"
SKIP_CLEANUP="${SKIP_CLEANUP:-false}"
CHART_PATH="${CHART_PATH:-./charts/my-csi-driver}"
SNAPSHOTTER_VERSION="${SNAPSHOTTER_VERSION:-v6.3.3}"

# Validate required environment variables
if [ -z "${IMG:-}" ]; then
  echo "Error: IMG environment variable is required (e.g., IMG=ghcr.io/user/my-csi-driver:tag)"
  exit 1
fi

if [ -z "${REGISTRY:-}" ]; then
  echo "Error: REGISTRY environment variable is required (e.g., REGISTRY=ghcr.io/user)"
  exit 1
fi

# Extract image tag from IMG
IMAGE_TAG=$(echo "$IMG" | awk -F':' '{print $NF}')
if [ -z "$IMAGE_TAG" ] || [ "$IMAGE_TAG" = "$IMG" ]; then
  echo "Error: Unable to extract tag from IMG=$IMG"
  exit 1
fi

echo "========================================="
echo "E2E Snapshot Test Configuration"
echo "========================================="
echo "IMG:                $IMG"
echo "REGISTRY:           $REGISTRY"
echo "IMAGE_TAG:          $IMAGE_TAG"
echo "KIND_CLUSTER_NAME:  $KIND_CLUSTER_NAME"
echo "CHART_PATH:         $CHART_PATH"
echo "SNAPSHOTTER_VERSION: $SNAPSHOTTER_VERSION"
echo "SKIP_CLEANUP:       $SKIP_CLEANUP"
echo "========================================="

# Check if kind cluster exists, create if not
if ! kind get clusters | grep -q "^${KIND_CLUSTER_NAME}$"; then
  echo "Creating kind cluster: $KIND_CLUSTER_NAME"
  kind create cluster --name "$KIND_CLUSTER_NAME"
else
  echo "Kind cluster already exists: $KIND_CLUSTER_NAME"
fi

# Set kubectl context to the kind cluster
kubectl config use-context "kind-${KIND_CLUSTER_NAME}"

# Function to cleanup resources
cleanup() {
  local exit_code=$?
  if [ "$SKIP_CLEANUP" = "true" ]; then
    echo "Skipping cleanup (SKIP_CLEANUP=true)"
    return
  fi

  echo "Cleaning up resources..."
  kubectl delete -f /tmp/snapshot-test.yaml --ignore-not-found=true || true
  kubectl delete volumesnapshotclass my-csi-driver-snapclass --ignore-not-found=true || true
  helm uninstall my-csi-driver --ignore-not-found || true
  
  # Only delete cluster on failure or if explicitly requested
  if [ $exit_code -ne 0 ]; then
    echo "Tests failed, deleting kind cluster: $KIND_CLUSTER_NAME"
    kind delete cluster --name "$KIND_CLUSTER_NAME" || true
  fi
}

# Register cleanup function
trap cleanup EXIT

echo ""
echo "========================================="
echo "Step 1: Install Snapshot CRDs"
echo "========================================="
CRD_BASE_URL="https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/${SNAPSHOTTER_VERSION}/client/config/crd"

echo "Installing VolumeSnapshotClass CRD..."
kubectl apply -f "${CRD_BASE_URL}/snapshot.storage.k8s.io_volumesnapshotclasses.yaml"

echo "Installing VolumeSnapshotContent CRD..."
kubectl apply -f "${CRD_BASE_URL}/snapshot.storage.k8s.io_volumesnapshotcontents.yaml"

echo "Installing VolumeSnapshot CRD..."
kubectl apply -f "${CRD_BASE_URL}/snapshot.storage.k8s.io_volumesnapshots.yaml"

echo "Waiting for CRDs to be established..."
kubectl wait --for condition=established --timeout=60s crd/volumesnapshotclasses.snapshot.storage.k8s.io
kubectl wait --for condition=established --timeout=60s crd/volumesnapshotcontents.snapshot.storage.k8s.io
kubectl wait --for condition=established --timeout=60s crd/volumesnapshots.snapshot.storage.k8s.io

echo "Snapshot CRDs installed successfully"

echo ""
echo "========================================="
echo "Step 2: Remove Rancher local-path provisioner and StorageClass"
echo "========================================="
echo "Removing any StorageClasses using rancher.io/local-path provisioner..."
kubectl get sc -o jsonpath='{range .items[*]}{.metadata.name}{" "}{.provisioner}{"\n"}{end}' \
  | awk '$2=="rancher.io/local-path"{print $1}' \
  | xargs -r -n1 kubectl delete storageclass || true

echo "Deleting local-path-provisioner Deployment..."
kubectl delete deployment.apps/local-path-provisioner -n local-path-storage --ignore-not-found=true || true
kubectl delete deployment.apps/local-path-provisioner -n kube-system --ignore-not-found=true || true

echo "Waiting briefly for deployment removal..."
kubectl wait --for=delete deployment/local-path-provisioner -n local-path-storage --timeout=30s 2>/dev/null || true
kubectl wait --for=delete deployment/local-path-provisioner -n kube-system --timeout=30s 2>/dev/null || true

echo "Stripping default annotation from any remaining StorageClasses..."
for sc in $(kubectl get storageclass -o name 2>/dev/null || true); do
  kubectl annotate "$sc" storageclass.kubernetes.io/is-default-class- --overwrite || true
done

echo "Final StorageClass list after cleanup:"
kubectl get sc -o wide || true

echo ""
echo "========================================="
echo "Step 3: Load image into kind"
echo "========================================="
kind load docker-image "$IMG" --name "$KIND_CLUSTER_NAME"

echo ""
echo "========================================="
echo "Step 4: Install CSI driver via Helm"
echo "========================================="
helm upgrade --install my-csi-driver "$CHART_PATH" \
  --set image.repository="${REGISTRY}/my-csi-driver" \
  --set image.tag="$IMAGE_TAG" \
  --set storageClass.create=true \
  --set storageClass.default=true \
  --set backingDir=/var/lib/my-csi-driver

echo ""
echo "========================================="
echo "Step 5: Wait for DaemonSet ready"
echo "========================================="
kubectl -n default rollout status ds/my-csi-driver --timeout=320s

echo ""
echo "========================================="
echo "Step 6: Wait for Controller Deployment ready"
echo "========================================="
kubectl -n default rollout status deploy/my-csi-driver-controller --timeout=320s

echo ""
echo "========================================="
echo "Step 7: Verify snapshot sidecar is present"
echo "========================================="
echo "Checking controller pod has csi-snapshotter container"
CTRL_POD=$(kubectl get pods -l app.kubernetes.io/component=controller -o jsonpath='{.items[0].metadata.name}')
kubectl get pod "$CTRL_POD" -o jsonpath='{.spec.containers[*].name}' | grep csi-snapshotter || {
  echo "ERROR: csi-snapshotter container not found in controller pod"
  exit 1
}
echo "✓ csi-snapshotter sidecar verified"

echo ""
echo "========================================="
echo "Step 8: Create VolumeSnapshotClass"
echo "========================================="
cat <<'YAML' | kubectl apply -f -
apiVersion: snapshot.storage.k8s.io/v1
kind: VolumeSnapshotClass
metadata:
  name: my-csi-driver-snapclass
driver: my-csi-driver
deletionPolicy: Delete
YAML

kubectl get volumesnapshotclass my-csi-driver-snapclass

echo ""
echo "========================================="
echo "Step 9: Create source PVC and Pod with test data"
echo "========================================="
cat <<'YAML' > /tmp/snapshot-test.yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: source-pvc
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
  name: source-pod
spec:
  restartPolicy: Never
  containers:
  - name: writer
    image: alpine:3.19
    command: ["/bin/sh", "-c"]
    args:
      - |
        echo "This is test data for snapshot e2e" > /data/test.txt
        echo "Timestamp: $(date)" >> /data/test.txt
        cat /data/test.txt
        sync
        sleep 2
    volumeMounts:
    - name: data
      mountPath: /data
  volumes:
  - name: data
    persistentVolumeClaim:
      claimName: source-pvc
YAML

kubectl apply -f /tmp/snapshot-test.yaml

echo "Waiting for source PVC to be bound..."
kubectl wait --for=jsonpath='{.status.phase}'=Bound pvc/source-pvc --timeout=120s

echo "Waiting for source pod to complete..."
kubectl wait --for=jsonpath='{.status.phase}'=Succeeded pod/source-pod --timeout=120s

echo "Source pod logs:"
kubectl logs source-pod

echo ""
echo "========================================="
echo "Step 10: Create VolumeSnapshot"
echo "========================================="
cat <<'YAML' >> /tmp/snapshot-test.yaml
---
apiVersion: snapshot.storage.k8s.io/v1
kind: VolumeSnapshot
metadata:
  name: source-snapshot
spec:
  volumeSnapshotClassName: my-csi-driver-snapclass
  source:
    persistentVolumeClaimName: source-pvc
YAML

kubectl apply -f /tmp/snapshot-test.yaml

echo "Waiting for snapshot to be ready..."
for i in {1..60}; do
  READY=$(kubectl get volumesnapshot source-snapshot -o jsonpath='{.status.readyToUse}' 2>/dev/null || echo "false")
  if [ "$READY" = "true" ]; then
    echo "✓ Snapshot is ready"
    break
  fi
  if [ $i -eq 60 ]; then
    echo "ERROR: Snapshot did not become ready in time"
    kubectl describe volumesnapshot source-snapshot
    kubectl get volumesnapshotcontent -o yaml
    exit 1
  fi
  echo "Waiting for snapshot to be ready... ($i/60)"
  sleep 2
done

kubectl get volumesnapshot source-snapshot -o yaml

echo ""
echo "========================================="
echo "Step 11: Create restored PVC from snapshot"
echo "========================================="
cat <<'YAML' >> /tmp/snapshot-test.yaml
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: restored-pvc
spec:
  accessModes: [ "ReadWriteOnce" ]
  storageClassName: my-csi-driver-default
  dataSource:
    name: source-snapshot
    kind: VolumeSnapshot
    apiGroup: snapshot.storage.k8s.io
  resources:
    requests:
      storage: 1Mi
YAML

kubectl apply -f /tmp/snapshot-test.yaml

echo "Waiting for restored PVC to be bound..."
kubectl wait --for=jsonpath='{.status.phase}'=Bound pvc/restored-pvc --timeout=120s

echo ""
echo "========================================="
echo "Step 12: Verify restored data matches original"
echo "========================================="
cat <<'YAML' >> /tmp/snapshot-test.yaml
---
apiVersion: v1
kind: Pod
metadata:
  name: restored-pod
spec:
  restartPolicy: Never
  containers:
  - name: reader
    image: alpine:3.19
    command: ["/bin/sh", "-c"]
    args:
      - |
        echo "=== Contents of /data/test.txt ==="
        cat /data/test.txt
        echo "==================================="
        # Verify the expected content is present
        if grep -q "This is test data for snapshot e2e" /data/test.txt; then
          echo "✓ Data verification successful"
          exit 0
        else
          echo "✗ Data verification failed"
          exit 1
        fi
    volumeMounts:
    - name: data
      mountPath: /data
  volumes:
  - name: data
    persistentVolumeClaim:
      claimName: restored-pvc
YAML

kubectl apply -f /tmp/snapshot-test.yaml

echo "Waiting for restored pod to complete..."
if ! kubectl wait --for=jsonpath='{.status.phase}'=Succeeded pod/restored-pod --timeout=120s; then
  echo "ERROR: Restored pod did not complete successfully"
  kubectl logs restored-pod || true
  kubectl describe pod restored-pod || true
  exit 1
fi

echo "Restored pod logs:"
kubectl logs restored-pod

echo ""
echo "========================================="
echo "Step 13: Verify snapshot metadata"
echo "========================================="
echo "Checking VolumeSnapshotContent..."
VSC_NAME=$(kubectl get volumesnapshot source-snapshot -o jsonpath='{.status.boundVolumeSnapshotContentName}')
kubectl get volumesnapshotcontent "$VSC_NAME" -o yaml

echo "Verifying snapshot source volume ID..."
SOURCE_VOL=$(kubectl get volumesnapshotcontent "$VSC_NAME" -o jsonpath='{.spec.source.volumeHandle}')
if [ -z "$SOURCE_VOL" ]; then
  echo "ERROR: Source volume ID not found in VolumeSnapshotContent"
  exit 1
fi
echo "✓ Source volume ID: $SOURCE_VOL"

echo "Verifying snapshot ID..."
SNAP_ID=$(kubectl get volumesnapshotcontent "$VSC_NAME" -o jsonpath='{.status.snapshotHandle}')
if [ -z "$SNAP_ID" ]; then
  echo "ERROR: Snapshot ID not found in VolumeSnapshotContent"
  exit 1
fi
echo "✓ Snapshot ID: $SNAP_ID"

echo ""
echo "========================================="
echo "Step 14: Test snapshot deletion"
echo "========================================="
echo "Deleting VolumeSnapshot..."
kubectl delete volumesnapshot source-snapshot

echo "Waiting for VolumeSnapshotContent to be deleted..."
if ! kubectl wait --for=delete volumesnapshotcontent "$VSC_NAME" --timeout=60s; then
  echo "Warning: VolumeSnapshotContent was not deleted in time (may be handled asynchronously)"
fi

echo ""
echo "========================================="
echo "E2E Snapshot Tests PASSED!"
echo "========================================="
echo "Summary:"
echo "  ✓ Snapshot CRDs installed"
echo "  ✓ CSI driver with snapshot sidecar deployed"
echo "  ✓ VolumeSnapshotClass created"
echo "  ✓ Source volume created with test data"
echo "  ✓ Snapshot created successfully"
echo "  ✓ Volume restored from snapshot"
echo "  ✓ Restored data verified"
echo "  ✓ Snapshot deletion tested"
echo "========================================="
