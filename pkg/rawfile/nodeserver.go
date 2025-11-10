package rawfile

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"golang.org/x/sys/unix"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	klog "k8s.io/klog/v2"
)

// Compile-time assertion
var _ csi.NodeServer = (*NodeServer)(nil)

// NodeServer implements the CSI Node service endpoints.
type NodeServer struct {
	nodeID     string
	driverName string
	backingDir string
	clientset  kubernetes.Interface
	csi.UnimplementedNodeServer
}

func NewNodeServer(nodeID, driverName, backingDir string, clientset kubernetes.Interface) *NodeServer {
	return &NodeServer{
		nodeID:     nodeID,
		driverName: driverName,
		backingDir: backingDir,
		clientset:  clientset,
	}
}

// NodePublishVolume mounts the volume to the target path on the node.
func (ns *NodeServer) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	klog.Infof("NodePublishVolume: %s at %s", req.VolumeId, req.TargetPath)
	if err := os.MkdirAll(req.TargetPath, 0750); err != nil {
		return nil, err
	}

	// Get backing file path from volume context
	backingFile, ok := req.VolumeContext["backingFile"]
	if !ok {
		return nil, fmt.Errorf("missing backingFile in volume context")
	}
	klog.Infof("NodePublishVolume backingFile: %s", backingFile)

	// Get size from volume context
	sizeStr, ok := req.VolumeContext["size"]
	if !ok {
		return nil, fmt.Errorf("missing size in volume context")
	}
	size, err := strconv.ParseInt(sizeStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid size in volume context: %v", err)
	}

	// Just-in-time creation: Create backing file if it doesn't exist
	if _, statErr := os.Stat(backingFile); statErr != nil {
		if os.IsNotExist(statErr) {
			klog.Infof("Backing file %s does not exist, creating just-in-time with size %d", backingFile, size)

			// Ensure backing directory exists
			backingFileDir := filepath.Dir(backingFile)
			if err := os.MkdirAll(backingFileDir, 0750); err != nil {
				return nil, fmt.Errorf("failed to create backing directory: %v", err)
			}

			// Create backing file
			f, err := os.Create(backingFile)
			if err != nil {
				return nil, fmt.Errorf("failed to create backing file: %v", err)
			}
			if err := f.Truncate(size); err != nil {
				f.Close()
				return nil, fmt.Errorf("failed to truncate backing file: %v", err)
			}
			f.Close()
			klog.Infof("Created backing file %s with size %d bytes", backingFile, size)
		} else {
			return nil, fmt.Errorf("backing file %s not accessible on node: %v", backingFile, statErr)
		}
	} else {
		klog.Infof("Backing file %s already exists", backingFile)
	}

	// Verify backing file exists and has content
	if fi, err := os.Stat(backingFile); err != nil {
		return nil, fmt.Errorf("backing file %s verification failed: %v", backingFile, err)
	} else if fi.Size() == 0 {
		klog.Warningf("backing file %s has zero size; losetup may fail", backingFile)
	}

	// Set up loop device
	loopDev, err := setupLoopDevice(backingFile)
	if err != nil {
		return nil, fmt.Errorf("failed to set up loop device: %v", err)
	}

	// Format if needed (only if not already formatted)
	fsType := req.VolumeCapability.GetMount().GetFsType()
	if fsType == "" {
		fsType = "ext4"
	}
	klog.Infof("NodePublishVolume format: %s %s", loopDev, fsType)

	if err := formatIfNeeded(loopDev, fsType); err != nil {
		return nil, fmt.Errorf("failed to format device: %v", err)
	}

	// Mount device
	if err := mountDevice(loopDev, req.TargetPath, fsType); err != nil {
		return nil, fmt.Errorf("failed to mount device: %v", err)
	}

	return &csi.NodePublishVolumeResponse{}, nil
}

// Helper: set up loop device
func setupLoopDevice(backingFile string) (string, error) {
	out, err := execCommand("losetup", "-f", "--show", backingFile)
	if err != nil {
		// Include losetup combined output to aid debugging (e.g., missing /dev/loop-control, permission denied, ENOENT)
		return "", fmt.Errorf("losetup failed for %s: %v: %s", backingFile, err, string(out))
	}
	// trim newline
	if len(out) > 0 && out[len(out)-1] == '\n' {
		out = out[:len(out)-1]
	}
	return string(out), nil
}

// Helper: format device if not already formatted
func formatIfNeeded(device, fsType string) error {
	klog.Infof("formatIfNeeded: checking %s", device)
	out, err := execCommand("blkid", device)
	if err == nil && len(out) > 0 {
		return nil // Already formatted
	}
	klog.Infof("formatIfNeeded: formatting %s with %s", device, fsType)
	_, err = execCommand("mkfs."+fsType, device)
	return err
}

// Helper: mount device
func mountDevice(device, target, fsType string) error {
	_, err := execCommand("mount", "-t", fsType, device, target)
	return err
}

// NodeUnpublishVolume unmounts the volume from the target path and detaches loop device.
func (ns *NodeServer) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	klog.Infof("NodeUnpublishVolume: %s", req.TargetPath)

	// Check if target path exists
	if _, err := os.Stat(req.TargetPath); os.IsNotExist(err) {
		// Path does not exist, treat as success (idempotent)
		return &csi.NodeUnpublishVolumeResponse{}, nil
	}

	// Check if it's mounted (by loop device); if not, treat as success
	loopDev, _ := FindLoopDevice(req.TargetPath)
	if loopDev == "" {
		// Not mounted; nothing to do
		return &csi.NodeUnpublishVolumeResponse{}, nil
	}

	// Unmount the target path
	if err := execCommandSimple("umount", req.TargetPath); err != nil {
		return nil, fmt.Errorf("failed to unmount: %v", err)
	}

	// Detach the loop device
	if err := execCommandSimple("losetup", "-d", loopDev); err != nil {
		return nil, fmt.Errorf("failed to detach loop device: %v", err)
	}

	return &csi.NodeUnpublishVolumeResponse{}, nil
}

func (ns *NodeServer) NodeGetInfo(ctx context.Context, req *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	return &csi.NodeGetInfoResponse{NodeId: ns.nodeID}, nil
}

func (ns *NodeServer) NodeGetCapabilities(ctx context.Context, req *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	caps := []*csi.NodeServiceCapability{
		{
			Type: &csi.NodeServiceCapability_Rpc{
				Rpc: &csi.NodeServiceCapability_RPC{
					Type: csi.NodeServiceCapability_RPC_GET_VOLUME_STATS,
				},
			},
		},
	}
	return &csi.NodeGetCapabilitiesResponse{Capabilities: caps}, nil
}

func (ns *NodeServer) NodeGetVolumeStats(ctx context.Context, req *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	klog.Infof("NodeGetVolumeStats: %s", req.VolumeId)

	// Validate volume path is provided
	if req.VolumePath == "" {
		return nil, fmt.Errorf("volume path is required")
	}

	// Check if volume path exists
	if _, err := os.Stat(req.VolumePath); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("volume path %s does not exist", req.VolumePath)
		}
		return nil, fmt.Errorf("failed to stat volume path %s: %v", req.VolumePath, err)
	}

	// Get filesystem statistics using statfs
	var stats unix.Statfs_t
	if err := unix.Statfs(req.VolumePath, &stats); err != nil {
		return nil, fmt.Errorf("failed to get volume stats for %s: %v", req.VolumePath, err)
	}

	// Calculate total capacity and available bytes
	// Blocks * BlockSize gives us the total/available in bytes
	// Note: While this multiplication could theoretically overflow for extremely large filesystems,
	// int64 can represent up to ~8 exabytes which exceeds current practical filesystem sizes.
	// This matches the CSI spec which defines these fields as int64.
	total := int64(stats.Blocks) * int64(stats.Bsize)
	available := int64(stats.Bavail) * int64(stats.Bsize)

	klog.Infof("NodeGetVolumeStats: volume=%s, total=%d bytes, available=%d bytes", req.VolumeId, total, available)

	return &csi.NodeGetVolumeStatsResponse{
		Usage: []*csi.VolumeUsage{
			{
				Unit:      csi.VolumeUsage_BYTES,
				Total:     total,
				Available: available,
			},
		},
	}, nil
}

func (ns *NodeServer) NodeStageVolume(ctx context.Context, req *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	return &csi.NodeStageVolumeResponse{}, nil
}

func (ns *NodeServer) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	return &csi.NodeUnstageVolumeResponse{}, nil
}

func (ns *NodeServer) NodeExpandVolume(ctx context.Context, req *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	return &csi.NodeExpandVolumeResponse{}, nil
}

// garbageCollectVolumes finds and deletes orphaned backing files
func (ns *NodeServer) garbageCollectVolumes(ctx context.Context) {
	klog.V(2).Infof("Starting garbage collection of orphaned volumes in %s", ns.backingDir)

	// Check if clientset is available
	if ns.clientset == nil {
		klog.V(2).Infof("Skipping garbage collection: Kubernetes clientset not configured")
		return
	}

	// List all .img files in backing directory
	files, err := filepath.Glob(filepath.Join(ns.backingDir, "*.img"))
	if err != nil {
		klog.Errorf("Failed to list backing files: %v", err)
		return
	}

	if len(files) == 0 {
		klog.V(2).Infof("No backing files found in %s", ns.backingDir)
		return
	}

	// List all PersistentVolumes from Kubernetes
	pvList, err := ns.clientset.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
	if err != nil {
		klog.Errorf("Failed to list PersistentVolumes: %v", err)
		return
	}

	// Build a map of active volume handles for CSI volumes belonging to this driver
	activeVolumes := make(map[string]bool)
	for _, pv := range pvList.Items {
		// Only consider PVs managed by this driver
		if pv.Spec.CSI != nil && pv.Spec.CSI.Driver == ns.driverName && pv.Spec.CSI.VolumeHandle != "" {
			// Extract volume ID from backing file path if present
			if backingFile, ok := pv.Spec.CSI.VolumeAttributes["backingFile"]; ok {
				activeVolumes[backingFile] = true
			}
			// Also track by volume handle/ID
			volumeFile := filepath.Join(ns.backingDir, pv.Spec.CSI.VolumeHandle+".img")
			activeVolumes[volumeFile] = true
		}
	}

	// Check each backing file
	deletedCount := 0
	for _, file := range files {
		if !activeVolumes[file] {
			// File is orphaned, delete it
			klog.Infof("Deleting orphaned backing file: %s", file)
			if err := os.Remove(file); err != nil {
				klog.Errorf("Failed to delete orphaned file %s: %v", file, err)
			} else {
				deletedCount++
			}
		}
	}

	klog.V(2).Infof("Garbage collection complete: deleted %d orphaned files out of %d total backing files", deletedCount, len(files))
}

// RunGarbageCollector runs the garbage collector periodically
func (ns *NodeServer) RunGarbageCollector(ctx context.Context, interval time.Duration) {
	klog.Infof("Starting garbage collector with interval %v", interval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			klog.Infof("Garbage collector stopped")
			return
		case <-ticker.C:
			ns.garbageCollectVolumes(ctx)
		}
	}
}
