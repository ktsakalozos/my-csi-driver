package rawfile

import (
	"context"
	"fmt"
	"os"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"golang.org/x/sys/unix"
	klog "k8s.io/klog/v2"
)

// Compile-time assertion
var _ csi.NodeServer = (*NodeServer)(nil)

// NodeServer implements the CSI Node service endpoints.
type NodeServer struct {
	nodeID string
	csi.UnimplementedNodeServer
}

func NewNodeServer(nodeID string) *NodeServer {
	return &NodeServer{nodeID: nodeID}
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

	// Ensure backing file exists on this node (controller may have created it on another node in multi-node clusters)
	if fi, statErr := os.Stat(backingFile); statErr != nil {
		return nil, fmt.Errorf("backing file %s not accessible on node: %v", backingFile, statErr)
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
