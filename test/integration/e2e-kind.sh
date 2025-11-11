#!/usr/bin/env bash
# e2e-kind.sh: End-to-end test suite for my-csi-driver running in a kind cluster
# This script:
#   1. Creates a kind cluster (if not already present)
#   2. Removes the Rancher local-path provisioner (to avoid conflicts)
#   3. Loads the CSI driver image into kind
#   4. Installs the CSI driver via Helm
#   5. Runs verification tests (controller/node modes, RBAC, StorageClass, dynamic provisioning)
#   6. Cleans up resources (optional, controlled by SKIP_CLEANUP)
#
# Environment variables:
#   IMG                  - Docker image to test (required)
#   REGISTRY             - Registry prefix for the image (required)
#   KIND_CLUSTER_NAME    - Name of the kind cluster (default: csi-e2e)
#   SKIP_CLEANUP         - Set to 'true' to skip cleanup on success (default: false)
#   CHART_PATH           - Path to Helm chart (default: ./charts/my-csi-driver)

set -xeuo pipefail

# Default values
KIND_CLUSTER_NAME="${KIND_CLUSTER_NAME:-csi-e2e}"
SKIP_CLEANUP="${SKIP_CLEANUP:-false}"
CHART_PATH="${CHART_PATH:-./charts/my-csi-driver}"

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
echo "E2E Test Configuration"
echo "========================================="
echo "IMG:                $IMG"
echo "REGISTRY:           $REGISTRY"
echo "IMAGE_TAG:          $IMAGE_TAG"
echo "KIND_CLUSTER_NAME:  $KIND_CLUSTER_NAME"
echo "CHART_PATH:         $CHART_PATH"
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
  kubectl delete -f /tmp/pvc-pod.yaml --ignore-not-found=true || true
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
echo "Step 1: Remove Rancher local-path provisioner and StorageClass"
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
echo "Step 2: Load image into kind"
echo "========================================="
kind load docker-image "$IMG" --name "$KIND_CLUSTER_NAME"

echo ""
echo "========================================="
echo "Step 3: Install CSI driver via Helm"
echo "========================================="
helm upgrade --install my-csi-driver "$CHART_PATH" \
  --set image.repository="${REGISTRY}/my-csi-driver" \
  --set image.tag="$IMAGE_TAG" \
  --set storageClass.create=true \
  --set storageClass.default=true \
  --set backingDir=/var/lib/my-csi-driver

echo ""
echo "========================================="
echo "Step 4: Wait for DaemonSet ready"
echo "========================================="
kubectl -n default rollout status ds/my-csi-driver --timeout=320s

echo ""
echo "========================================="
echo "Step 5: Wait for Controller Deployment ready"
echo "========================================="
kubectl -n default rollout status deploy/my-csi-driver-controller --timeout=320s

echo ""
echo "========================================="
echo "Step 6: Verify controller and node modes"
echo "========================================="
echo "Checking controller pod args include --mode=controller"
CTRL_POD=$(kubectl get pods -l app.kubernetes.io/component=controller -o jsonpath='{.items[0].metadata.name}')
kubectl get pod "$CTRL_POD" -o jsonpath='{.spec.containers[0].args}' | grep -- '--mode=controller'
echo "Checking node daemonset pod args include --mode=node"
NODE_POD=$(kubectl get pods -l app.kubernetes.io/component=node -o jsonpath='{.items[0].metadata.name}')
kubectl get pod "$NODE_POD" -o jsonpath='{.spec.containers[0].args}' | grep -- '--mode=node'
echo "Controller and node mode arguments verified."

echo ""
echo "========================================="
echo "Step 7: Verify split ServiceAccounts and RBAC artifacts"
echo "========================================="
echo "Listing service accounts"
kubectl get sa
kubectl get sa my-csi-driver-controller || (echo 'missing controller SA' && exit 1)
kubectl get sa my-csi-driver-node || (echo 'missing node SA' && exit 1)

echo "Checking that a leader election Lease is created (may take a few seconds)"
for i in {1..10}; do kubectl get lease my-csi-driver && break || sleep 2; done
kubectl get lease my-csi-driver || (echo 'Lease my-csi-driver not found' && exit 1)
echo "Lease found."

echo "Checking CSIStorageCapacity objects (non-fatal if empty but should exist with --enable-capacity=true)"
kubectl get csistoragecapacities.storage.k8s.io || true

echo "Attempting to read storageclasses (controller perms)"
kubectl get storageclasses || true
echo "RBAC verification step completed"

echo ""
echo "========================================="
echo "Step 8: Verify StorageClass exists"
echo "========================================="
kubectl get storageclass
kubectl get storageclass my-csi-driver-default -o yaml

echo ""
echo "========================================="
echo "Step 9: Deploy dynamic provisioning test pod (PVC + Pod)"
echo "========================================="
cat <<'YAML' > /tmp/pvc-pod.yaml
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
kubectl apply -f /tmp/pvc-pod.yaml

echo ""
echo "========================================="
echo "Step 10: Wait for PVC bound"
echo "========================================="
kubectl wait --for=jsonpath='{.status.phase}'=Bound pvc/demo-pvc --timeout=320s

echo ""
echo "========================================="
echo "Step 11: Wait for Pod completion (phase=Succeeded)"
echo "========================================="
if ! kubectl wait --for=jsonpath='{.status.phase}'=Succeeded pod/demo-app --timeout=300s; then
  echo "Pod did not reach Succeeded in time; dumping diagnostics..."
  kubectl get pod demo-app -o yaml || true
  kubectl describe pod demo-app || true
  kubectl logs pod/demo-app || true
  exit 1
fi
echo "Pod completed successfully"

echo ""
echo "========================================="
echo "E2E Tests PASSED!"
echo "========================================="
