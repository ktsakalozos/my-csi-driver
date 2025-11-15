package rawfile

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	klog "k8s.io/klog/v2"
)

// ControllerServer implements the CSI Controller service endpoints.
type ControllerServer struct {
	name       string
	version    string
	backingDir string
	clientset  kubernetes.Interface
	csi.UnimplementedControllerServer
}

// Compile-time assertion
var _ csi.ControllerServer = (*ControllerServer)(nil)

// NewControllerServer creates a controller with backingDir resolved from env/defaults.
// It preserves previous behavior where CSI_BACKING_DIR env var was used.
func NewControllerServer(name, version string, clientset kubernetes.Interface) *ControllerServer {
	dir := os.Getenv("CSI_BACKING_DIR")
	if dir == "" {
		dir = "/var/lib/my-csi-driver"
	}
	return &ControllerServer{name: name, version: version, backingDir: dir, clientset: clientset}
}

// NewControllerServerWithBackingDir creates a controller with an explicit backingDir.
func NewControllerServerWithBackingDir(name, version, backingDir string, clientset kubernetes.Interface) *ControllerServer {
	dir := backingDir
	if dir == "" {
		// Fall back to env, then default
		dir = os.Getenv("CSI_BACKING_DIR")
		if dir == "" {
			dir = "/var/lib/my-csi-driver"
		}
	}
	return &ControllerServer{name: name, version: version, backingDir: dir, clientset: clientset}
}

func (cs *ControllerServer) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	volID := "vol-" + uuid.New().String()
	klog.Infof("CreateVolume: %s (logical creation)", volID)

	// Get volume size in bytes
	size := req.CapacityRange.GetRequiredBytes()
	if size == 0 {
		size = 1 << 30 // Default to 1GiB
	}

	// Define backing file path (will be created by NodeServer)
	backingFile := cs.backingDir + "/" + volID + ".img"
	klog.Infof("CreateVolume backingFile: %s (deferred to node)", backingFile)

	// Base context
	ctxMap := map[string]string{
		"backingFile": backingFile,
		"size":        strconv.FormatInt(size, 10),
	}

	// Handle restore from snapshot
	if src := req.GetVolumeContentSource(); src != nil && src.GetSnapshot() != nil {
		snapID := src.GetSnapshot().GetSnapshotId()
		ctxMap["restoreFromSnapshot"] = snapID
		ctxMap["snapshotFile"] = cs.backingDir + "/snap-" + snapID + ".img"
		klog.Infof("CreateVolume: restore from snapshot %s -> %s", snapID, backingFile)
	}

	// Prepare response
	resp := &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			VolumeId:      volID,
			CapacityBytes: size,
			VolumeContext: ctxMap,
		},
	}

	// Handle topology: if the external-provisioner provides preferred topology,
	// use the first preferred topology to indicate where the volume will be accessible.
	// This works with the JIT file creation model because the file will be created
	// on the node where the pod is scheduled, which matches the topology constraint.
	if req.AccessibilityRequirements != nil && len(req.AccessibilityRequirements.Preferred) > 0 {
		// Use the first preferred topology
		resp.Volume.AccessibleTopology = []*csi.Topology{req.AccessibilityRequirements.Preferred[0]}
		klog.Infof("CreateVolume: set AccessibleTopology from preferred: %+v", req.AccessibilityRequirements.Preferred[0])
	} else if req.AccessibilityRequirements != nil && len(req.AccessibilityRequirements.Requisite) > 0 {
		// Fall back to first requisite topology if no preferred
		resp.Volume.AccessibleTopology = []*csi.Topology{req.AccessibilityRequirements.Requisite[0]}
		klog.Infof("CreateVolume: set AccessibleTopology from requisite: %+v", req.AccessibilityRequirements.Requisite[0])
	}

	return resp, nil
}

func (cs *ControllerServer) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	klog.Infof("DeleteVolume: %s (logical deletion, physical cleanup handled by node garbage collector)", req.VolumeId)
	return &csi.DeleteVolumeResponse{}, nil
}

func (cs *ControllerServer) ControllerPublishVolume(ctx context.Context, req *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
	return &csi.ControllerPublishVolumeResponse{}, nil
}

func (cs *ControllerServer) ControllerUnpublishVolume(ctx context.Context, req *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	return &csi.ControllerUnpublishVolumeResponse{}, nil
}

func (cs *ControllerServer) ValidateVolumeCapabilities(ctx context.Context, req *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	return &csi.ValidateVolumeCapabilitiesResponse{
		Confirmed: &csi.ValidateVolumeCapabilitiesResponse_Confirmed{
			VolumeCapabilities: req.VolumeCapabilities,
		},
	}, nil
}

func (cs *ControllerServer) ListVolumes(ctx context.Context, req *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {
	return &csi.ListVolumesResponse{}, nil
}

func (cs *ControllerServer) GetCapacity(ctx context.Context, req *csi.GetCapacityRequest) (*csi.GetCapacityResponse, error) {
	return &csi.GetCapacityResponse{AvailableCapacity: 1 << 30}, nil
}

func (cs *ControllerServer) ControllerGetCapabilities(ctx context.Context, req *csi.ControllerGetCapabilitiesRequest) (*csi.ControllerGetCapabilitiesResponse, error) {
	ctrlCaps := []*csi.ControllerServiceCapability{}
	// Indicate support for create/delete volume
	ctrlCaps = append(ctrlCaps, &csi.ControllerServiceCapability{
		Type: &csi.ControllerServiceCapability_Rpc{
			Rpc: &csi.ControllerServiceCapability_RPC{
				Type: csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
			},
		},
	})
	// Indicate support for create/delete snapshot
	ctrlCaps = append(ctrlCaps, &csi.ControllerServiceCapability{
		Type: &csi.ControllerServiceCapability_Rpc{
			Rpc: &csi.ControllerServiceCapability_RPC{
				Type: csi.ControllerServiceCapability_RPC_CREATE_DELETE_SNAPSHOT,
			},
		},
	})
	// Indicate support for list snapshots
	ctrlCaps = append(ctrlCaps, &csi.ControllerServiceCapability{
		Type: &csi.ControllerServiceCapability_Rpc{
			Rpc: &csi.ControllerServiceCapability_RPC{
				Type: csi.ControllerServiceCapability_RPC_LIST_SNAPSHOTS,
			},
		},
	})
	return &csi.ControllerGetCapabilitiesResponse{Capabilities: ctrlCaps}, nil
}

func (cs *ControllerServer) ControllerGetVolume(ctx context.Context, req *csi.ControllerGetVolumeRequest) (*csi.ControllerGetVolumeResponse, error) {
	klog.Infof("ControllerGetVolume: %s (fetching from Kubernetes API)", req.VolumeId)

	// Check if clientset is available
	if cs.clientset == nil {
		return nil, status.Errorf(codes.FailedPrecondition, "Kubernetes clientset not configured - cannot query volume status")
	}

	// Fetch the PersistentVolume object from Kubernetes API
	pv, err := cs.clientset.CoreV1().PersistentVolumes().Get(ctx, req.VolumeId, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return nil, status.Errorf(codes.NotFound, "volume %s not found", req.VolumeId)
		}
		return nil, status.Errorf(codes.Internal, "error accessing volume: %v", err)
	}

	// Verify that this PV belongs to our driver
	if pv.Spec.CSI == nil || pv.Spec.CSI.Driver != cs.name {
		return nil, status.Errorf(codes.NotFound, "volume %s not managed by driver %s", req.VolumeId, cs.name)
	}

	// Extract capacity from PV spec
	var capacityBytes int64
	if capacity, ok := pv.Spec.Capacity[corev1.ResourceStorage]; ok {
		capacityBytes = capacity.Value()
	}

	// Extract backing file and size from volume attributes
	backingFile := ""
	size := ""
	if pv.Spec.CSI.VolumeAttributes != nil {
		backingFile = pv.Spec.CSI.VolumeAttributes["backingFile"]
		size = pv.Spec.CSI.VolumeAttributes["size"]
	}

	// Return volume info from Kubernetes API
	volumeContext := map[string]string{
		"backingFile": backingFile,
	}
	// Include size if available from PV attributes, otherwise use capacity
	if size != "" {
		volumeContext["size"] = size
	} else if capacityBytes > 0 {
		volumeContext["size"] = strconv.FormatInt(capacityBytes, 10)
	}

	return &csi.ControllerGetVolumeResponse{
		Volume: &csi.Volume{
			VolumeId:      req.VolumeId,
			CapacityBytes: capacityBytes,
			VolumeContext: volumeContext,
		},
	}, nil
}

func (cs *ControllerServer) ControllerExpandVolume(ctx context.Context, req *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	return &csi.ControllerExpandVolumeResponse{
		CapacityBytes:         req.CapacityRange.GetRequiredBytes(),
		NodeExpansionRequired: false,
	}, nil
}

func (cs *ControllerServer) ControllerModifyVolume(ctx context.Context, req *csi.ControllerModifyVolumeRequest) (*csi.ControllerModifyVolumeResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "ControllerModifyVolume not implemented")
}

// Snapshot RPCs
func (cs *ControllerServer) CreateSnapshot(ctx context.Context, req *csi.CreateSnapshotRequest) (*csi.CreateSnapshotResponse, error) {
	if req.GetSourceVolumeId() == "" {
		return nil, status.Errorf(codes.InvalidArgument, "missing source volume id")
	}
	if cs.clientset == nil {
		return nil, status.Errorf(codes.FailedPrecondition, "kubernetes clientset not configured")
	}

	volID := req.GetSourceVolumeId()
	pv, err := cs.clientset.CoreV1().PersistentVolumes().Get(ctx, volID, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return nil, status.Errorf(codes.NotFound, "source volume %s not found", volID)
		}
		return nil, status.Errorf(codes.Internal, "failed to get PV: %v", err)
	}
	if pv.Spec.CSI == nil || pv.Spec.CSI.Driver != cs.name {
		return nil, status.Errorf(codes.NotFound, "volume %s not managed by driver %s", volID, cs.name)
	}

	// Determine node (hostname) from PV nodeAffinity
	nodeName := extractNodeHostnameFromPV(pv)
	if nodeName == "" {
		return nil, status.Errorf(codes.FailedPrecondition, "cannot determine node for volume %s", volID)
	}

	// Resolve source backing file
	srcFile := ""
	if pv.Spec.CSI.VolumeAttributes != nil {
		srcFile = pv.Spec.CSI.VolumeAttributes["backingFile"]
	}
	if srcFile == "" {
		srcFile = cs.backingDir + "/" + volID + ".img"
	}

	snapID := "snap-" + uuid.New().String()
	dstFile := cs.backingDir + "/" + snapID + ".img"

	// Idempotency: if already exists, return success
	exists, err := fileExistsOnNode(ctx, cs.clientset, nodeName, cs.backingDir, snapID+".img")
	if err != nil {
		klog.Warningf("CreateSnapshot: could not check existence: %v", err)
	}
	if exists {
		klog.Infof("CreateSnapshot: snapshot %s already exists", snapID)
		return &csi.CreateSnapshotResponse{
			Snapshot: &csi.Snapshot{
				SnapshotId:     snapID,
				SourceVolumeId: volID,
				ReadyToUse:     true,
				CreationTime:   timestampProto(time.Now()),
			},
		}, nil
	}

	// Launch node-scoped job/pod to copy file
	klog.Infof("CreateSnapshot: copying %s to %s on node %s", srcFile, dstFile, nodeName)
	if err := runNodeCopyPod(ctx, cs.clientset, nodeName, cs.backingDir, srcFile, dstFile); err != nil {
		return nil, status.Errorf(codes.Internal, "snapshot copy failed: %v", err)
	}

	return &csi.CreateSnapshotResponse{
		Snapshot: &csi.Snapshot{
			SnapshotId:     snapID,
			SourceVolumeId: volID,
			ReadyToUse:     true,
			CreationTime:   timestampProto(time.Now()),
		},
	}, nil
}

func (cs *ControllerServer) DeleteSnapshot(ctx context.Context, req *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	if req.GetSnapshotId() == "" {
		return nil, status.Errorf(codes.InvalidArgument, "missing snapshot id")
	}
	if cs.clientset == nil {
		return &csi.DeleteSnapshotResponse{}, nil // be idempotent
	}

	snapID := req.GetSnapshotId()
	// Try to delete on all nodes (best-effort); ignore failures.
	// Simplest approach: attempt delete everywhere; treat not found as success.
	klog.Infof("DeleteSnapshot: attempting to delete snapshot %s", snapID)
	if err := runNodeDeletePodAllNodes(ctx, cs.clientset, cs.backingDir, cs.backingDir+"/"+snapID+".img"); err != nil {
		klog.Warningf("DeleteSnapshot: best-effort delete: %v", err)
	}
	return &csi.DeleteSnapshotResponse{}, nil
}

func (cs *ControllerServer) ListSnapshots(ctx context.Context, req *csi.ListSnapshotsRequest) (*csi.ListSnapshotsResponse, error) {
	// Minimal implementation: return empty list if SnapshotId not provided
	// If SnapshotId is provided, return that entry
	entries := []*csi.ListSnapshotsResponse_Entry{}
	if req.GetSnapshotId() != "" {
		entries = append(entries, &csi.ListSnapshotsResponse_Entry{
			Snapshot: &csi.Snapshot{
				SnapshotId: req.GetSnapshotId(),
				ReadyToUse: true, // best-effort
			},
		})
	}
	return &csi.ListSnapshotsResponse{Entries: entries}, nil
}

// extractNodeHostnameFromPV extracts the node hostname from PV's node affinity
func extractNodeHostnameFromPV(pv *corev1.PersistentVolume) string {
	if pv.Spec.NodeAffinity == nil || pv.Spec.NodeAffinity.Required == nil {
		return ""
	}
	for _, term := range pv.Spec.NodeAffinity.Required.NodeSelectorTerms {
		for _, expr := range term.MatchExpressions {
			if expr.Key == "kubernetes.io/hostname" && len(expr.Values) > 0 {
				return expr.Values[0]
			}
		}
	}
	return ""
}

// runNodeCopyPod creates a pod on the specified node to copy a file
func runNodeCopyPod(ctx context.Context, client kubernetes.Interface, nodeName, hostDir, src, dst string) error {
	podName := "csi-snapshot-copy-" + uuid.New().String()[:8]
	namespace := "kube-system" // Use kube-system for privileged operations

	// Create a pod with hostPath mount to perform the copy
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: namespace,
		},
		Spec: corev1.PodSpec{
			NodeSelector: map[string]string{
				"kubernetes.io/hostname": nodeName,
			},
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:    "copy",
					Image:   "busybox:latest",
					Command: []string{"/bin/sh", "-c"},
					Args: []string{
						fmt.Sprintf("cp --reflink=auto -f %s %s || cat %s > %s", src, dst, src, dst),
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "data-dir",
							MountPath: hostDir,
						},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "data-dir",
					VolumeSource: corev1.VolumeSource{
						HostPath: &corev1.HostPathVolumeSource{
							Path: hostDir,
							Type: func() *corev1.HostPathType { t := corev1.HostPathDirectoryOrCreate; return &t }(),
						},
					},
				},
			},
		},
	}

	// Create the pod
	if _, err := client.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create pod: %v", err)
	}
	defer func() {
		// Clean up the pod
		_ = client.CoreV1().Pods(namespace).Delete(ctx, podName, metav1.DeleteOptions{})
	}()

	// Wait for pod to complete (with timeout)
	timeout := time.After(2 * time.Minute)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return fmt.Errorf("timeout waiting for copy pod to complete")
		case <-ticker.C:
			p, err := client.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("failed to get pod status: %v", err)
			}
			if p.Status.Phase == corev1.PodSucceeded {
				klog.Infof("Copy pod %s completed successfully", podName)
				return nil
			}
			if p.Status.Phase == corev1.PodFailed {
				return fmt.Errorf("copy pod failed with phase: %s", p.Status.Phase)
			}
		}
	}
}

// runNodeDeletePodAllNodes attempts to delete a file on all nodes (best-effort)
func runNodeDeletePodAllNodes(ctx context.Context, client kubernetes.Interface, hostDir, filePath string) error {
	// List all nodes
	nodes, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list nodes: %v", err)
	}

	// For each node, try to delete the file (best-effort)
	for _, node := range nodes.Items {
		nodeName := node.Name
		if err := runNodeDeletePod(ctx, client, nodeName, hostDir, filePath); err != nil {
			klog.V(2).Infof("Failed to delete file on node %s (ignoring): %v", nodeName, err)
		}
	}
	return nil
}

// runNodeDeletePod creates a pod on the specified node to delete a file
func runNodeDeletePod(ctx context.Context, client kubernetes.Interface, nodeName, hostDir, filePath string) error {
	podName := "csi-snapshot-delete-" + uuid.New().String()[:8]
	namespace := "kube-system"

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: namespace,
		},
		Spec: corev1.PodSpec{
			NodeSelector: map[string]string{
				"kubernetes.io/hostname": nodeName,
			},
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:    "delete",
					Image:   "busybox:latest",
					Command: []string{"/bin/sh", "-c"},
					Args: []string{
						fmt.Sprintf("rm -f %s || true", filePath),
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "data-dir",
							MountPath: hostDir,
						},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "data-dir",
					VolumeSource: corev1.VolumeSource{
						HostPath: &corev1.HostPathVolumeSource{
							Path: hostDir,
							Type: func() *corev1.HostPathType { t := corev1.HostPathDirectoryOrCreate; return &t }(),
						},
					},
				},
			},
		},
	}

	if _, err := client.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create delete pod: %v", err)
	}
	defer func() {
		_ = client.CoreV1().Pods(namespace).Delete(ctx, podName, metav1.DeleteOptions{})
	}()

	// Wait for pod to complete
	timeout := time.After(30 * time.Second)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return fmt.Errorf("timeout waiting for delete pod to complete")
		case <-ticker.C:
			p, err := client.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("failed to get pod status: %v", err)
			}
			if p.Status.Phase == corev1.PodSucceeded {
				return nil
			}
			if p.Status.Phase == corev1.PodFailed {
				// Ignore failures for delete (file may not exist)
				return nil
			}
		}
	}
}

// fileExistsOnNode checks if a file exists on a specific node
func fileExistsOnNode(ctx context.Context, client kubernetes.Interface, nodeName, hostDir, fileName string) (bool, error) {
	podName := "csi-snapshot-check-" + uuid.New().String()[:8]
	namespace := "kube-system"

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: namespace,
		},
		Spec: corev1.PodSpec{
			NodeSelector: map[string]string{
				"kubernetes.io/hostname": nodeName,
			},
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:    "check",
					Image:   "busybox:latest",
					Command: []string{"/bin/sh", "-c"},
					Args: []string{
						fmt.Sprintf("test -f %s/%s", hostDir, fileName),
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "data-dir",
							MountPath: hostDir,
						},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "data-dir",
					VolumeSource: corev1.VolumeSource{
						HostPath: &corev1.HostPathVolumeSource{
							Path: hostDir,
							Type: func() *corev1.HostPathType { t := corev1.HostPathDirectoryOrCreate; return &t }(),
						},
					},
				},
			},
		},
	}

	if _, err := client.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		return false, fmt.Errorf("failed to create check pod: %v", err)
	}
	defer func() {
		_ = client.CoreV1().Pods(namespace).Delete(ctx, podName, metav1.DeleteOptions{})
	}()

	// Wait for pod to complete
	timeout := time.After(30 * time.Second)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return false, fmt.Errorf("timeout waiting for check pod to complete")
		case <-ticker.C:
			p, err := client.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
			if err != nil {
				return false, fmt.Errorf("failed to get pod status: %v", err)
			}
			if p.Status.Phase == corev1.PodSucceeded {
				return true, nil
			}
			if p.Status.Phase == corev1.PodFailed {
				return false, nil
			}
		}
	}
}

// timestampProto creates a protobuf Timestamp from a time.Time
func timestampProto(t time.Time) *timestamppb.Timestamp {
	return timestamppb.New(t)
}
