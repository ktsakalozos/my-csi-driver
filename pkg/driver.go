package pkg

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"strings"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type MyCSIDriver struct {
	name    string
	version string
	nodeID  string
}

func NewMyCSIDriver(name, version, nodeID string) *MyCSIDriver {
	return &MyCSIDriver{name: name, version: version, nodeID: nodeID}
}

func (d *MyCSIDriver) Run(endpoint string) error {
	os.Remove(endpoint)
	lis, err := net.Listen("unix", endpoint)
	if err != nil {
		return fmt.Errorf("listen error: %v", err)
	}

	grpcServer := grpc.NewServer()
	csi.RegisterIdentityServer(grpcServer, d)
	csi.RegisterControllerServer(grpcServer, d)
	csi.RegisterNodeServer(grpcServer, d)

	log.Printf("Starting CSI driver %s at %s", d.name, endpoint)
	return grpcServer.Serve(lis)
}

// Identity Service
func (d *MyCSIDriver) GetPluginInfo(ctx context.Context, req *csi.GetPluginInfoRequest) (*csi.GetPluginInfoResponse, error) {
	return &csi.GetPluginInfoResponse{
		Name:          d.name,
		VendorVersion: d.version,
	}, nil
}

func (d *MyCSIDriver) GetPluginCapabilities(ctx context.Context, req *csi.GetPluginCapabilitiesRequest) (*csi.GetPluginCapabilitiesResponse, error) {
	caps := []*csi.PluginCapability{}
	// Indicate controller service is available
	caps = append(caps, &csi.PluginCapability{
		Type: &csi.PluginCapability_Service_{
			Service: &csi.PluginCapability_Service{
				Type: csi.PluginCapability_Service_CONTROLLER_SERVICE,
			},
		},
	})
	return &csi.GetPluginCapabilitiesResponse{Capabilities: caps}, nil
}

func (d *MyCSIDriver) Probe(ctx context.Context, req *csi.ProbeRequest) (*csi.ProbeResponse, error) {
	return &csi.ProbeResponse{}, nil
}

// Controller Service
func (d *MyCSIDriver) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	volID := "vol-" + uuid.New().String()
	log.Printf("CreateVolume: %s", volID)

	// Get volume size in bytes
	size := req.CapacityRange.GetRequiredBytes()
	if size == 0 {
		size = 1 << 30 // Default to 1GiB
	}

	// Backing file directory configurable via CSI_BACKING_DIR
	backingDir := os.Getenv("CSI_BACKING_DIR")
	if backingDir == "" {
		backingDir = "/var/lib/my-csi-driver"
	}
	if err := os.MkdirAll(backingDir, 0750); err != nil {
		return nil, err
	}
	backingFile := backingDir + "/" + volID + ".img"
	log.Printf("CreateVolume backingFile: %s", backingFile)

	// Create backing file
	f, err := os.Create(backingFile)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err := f.Truncate(size); err != nil {
		return nil, err
	}

	// Return volume context with file path
	return &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			VolumeId:      volID,
			CapacityBytes: size,
			VolumeContext: map[string]string{
				"backingFile": backingFile,
			},
		},
	}, nil
}

func (d *MyCSIDriver) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	log.Printf("DeleteVolume: %s", req.VolumeId)

	// Backing file directory configurable via CSI_BACKING_DIR
	backingDir := os.Getenv("CSI_BACKING_DIR")
	if backingDir == "" {
		backingDir = "/var/lib/my-csi-driver"
	}
	backingFile := backingDir + "/" + req.VolumeId + ".img"
	log.Printf("DeleteVolume backingFile: %s", backingFile)

	// Remove backing file if it exists
	if err := os.Remove(backingFile); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to remove backing file: %v", err)
	}

	return &csi.DeleteVolumeResponse{}, nil
}

func (d *MyCSIDriver) ControllerPublishVolume(ctx context.Context, req *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
	return &csi.ControllerPublishVolumeResponse{}, nil
}

func (d *MyCSIDriver) ControllerUnpublishVolume(ctx context.Context, req *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	return &csi.ControllerUnpublishVolumeResponse{}, nil
}

func (d *MyCSIDriver) ValidateVolumeCapabilities(ctx context.Context, req *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	return &csi.ValidateVolumeCapabilitiesResponse{
		Confirmed: &csi.ValidateVolumeCapabilitiesResponse_Confirmed{
			VolumeCapabilities: req.VolumeCapabilities,
		},
	}, nil
}

func (d *MyCSIDriver) ListVolumes(ctx context.Context, req *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {
	return &csi.ListVolumesResponse{}, nil
}

func (d *MyCSIDriver) GetCapacity(ctx context.Context, req *csi.GetCapacityRequest) (*csi.GetCapacityResponse, error) {
	return &csi.GetCapacityResponse{AvailableCapacity: 1 << 30}, nil
}

func (d *MyCSIDriver) ControllerGetCapabilities(ctx context.Context, req *csi.ControllerGetCapabilitiesRequest) (*csi.ControllerGetCapabilitiesResponse, error) {
	ctrlCaps := []*csi.ControllerServiceCapability{}
	// Indicate support for create/delete volume
	ctrlCaps = append(ctrlCaps, &csi.ControllerServiceCapability{
		Type: &csi.ControllerServiceCapability_Rpc{
			Rpc: &csi.ControllerServiceCapability_RPC{
				Type: csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
			},
		},
	})
	return &csi.ControllerGetCapabilitiesResponse{Capabilities: ctrlCaps}, nil
}

func (d *MyCSIDriver) ControllerGetVolume(ctx context.Context, req *csi.ControllerGetVolumeRequest) (*csi.ControllerGetVolumeResponse, error) {
	// Backing file directory configurable via CSI_BACKING_DIR
	backingDir := os.Getenv("CSI_BACKING_DIR")
	if backingDir == "" {
		backingDir = "/var/lib/my-csi-driver"
	}
	backingFile := backingDir + "/" + req.VolumeId + ".img"
	log.Printf("ControllerGetVolume backingFile: %s", backingFile)

	fi, err := os.Stat(backingFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, status.Errorf(codes.NotFound, "volume %s not found", req.VolumeId)
		}
		return nil, status.Errorf(codes.Internal, "error accessing volume: %v", err)
	}

	// Return basic volume info
	return &csi.ControllerGetVolumeResponse{
		Volume: &csi.Volume{
			VolumeId:      req.VolumeId,
			CapacityBytes: fi.Size(),
			VolumeContext: map[string]string{
				"backingFile": backingFile,
			},
		},
	}, nil
}

func (d *MyCSIDriver) ControllerExpandVolume(ctx context.Context, req *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	return &csi.ControllerExpandVolumeResponse{
		CapacityBytes:         req.CapacityRange.GetRequiredBytes(),
		NodeExpansionRequired: false,
	}, nil
}

func (d *MyCSIDriver) ControllerModifyVolume(ctx context.Context, req *csi.ControllerModifyVolumeRequest) (*csi.ControllerModifyVolumeResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "ControllerModifyVolume not implemented")
}

// Node Service
func (d *MyCSIDriver) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	log.Printf("NodePublishVolume: %s at %s", req.VolumeId, req.TargetPath)
	if err := os.MkdirAll(req.TargetPath, 0750); err != nil {
		return nil, err
	}

	// Get backing file path from volume context
	backingFile, ok := req.VolumeContext["backingFile"]
	if !ok {
		return nil, fmt.Errorf("missing backingFile in volume context")
	}
	log.Printf("NodePublishVolume backingFile: %s", backingFile)

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
	log.Printf("NodePublishVolume format: %s %s", loopDev, fsType)

	if err := formatIfNeeded(loopDev, fsType); err != nil {
		return nil, fmt.Errorf("failed to format device: %v", err)
	}

	// Mount device
	if err := mountDevice(loopDev, req.TargetPath, fsType); err != nil {
		return nil, fmt.Errorf("failed to mount device: %v", err)
	}

	return &csi.NodePublishVolumeResponse{}, nil
}

func (d *MyCSIDriver) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	log.Printf("NodeUnpublishVolume: %s", req.TargetPath)

	// Check if target path exists
	if _, err := os.Stat(req.TargetPath); os.IsNotExist(err) {
		// Path does not exist, treat as success (idempotent)
		return &csi.NodeUnpublishVolumeResponse{}, nil
	}

	// Unmount the target path
	if err := execCommandSimple("umount", req.TargetPath); err != nil {
		return nil, fmt.Errorf("failed to unmount: %v", err)
	}

	// Find and detach the loop device
	loopDev, err := FindLoopDevice(req.TargetPath)
	if err == nil && loopDev != "" {
		if err := execCommandSimple("losetup", "-d", loopDev); err != nil {
			return nil, fmt.Errorf("failed to detach loop device: %v", err)
		}
	}

	return &csi.NodeUnpublishVolumeResponse{}, nil
}

func (d *MyCSIDriver) NodeGetInfo(ctx context.Context, req *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	return &csi.NodeGetInfoResponse{NodeId: d.nodeID}, nil
}

func (d *MyCSIDriver) NodeGetCapabilities(ctx context.Context, req *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	return &csi.NodeGetCapabilitiesResponse{}, nil
}

func (d *MyCSIDriver) NodeGetVolumeStats(ctx context.Context, req *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	return &csi.NodeGetVolumeStatsResponse{}, nil
}

func (d *MyCSIDriver) NodeStageVolume(ctx context.Context, req *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	return &csi.NodeStageVolumeResponse{}, nil
}

func (d *MyCSIDriver) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	return &csi.NodeUnstageVolumeResponse{}, nil
}

func (d *MyCSIDriver) NodeExpandVolume(ctx context.Context, req *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	return &csi.NodeExpandVolumeResponse{}, nil
}

// Snapshot RPCs (ControllerServer)
func (d *MyCSIDriver) CreateSnapshot(ctx context.Context, req *csi.CreateSnapshotRequest) (*csi.CreateSnapshotResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "CreateSnapshot not implemented")
}

func (d *MyCSIDriver) DeleteSnapshot(ctx context.Context, req *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "DeleteSnapshot not implemented")
}

func (d *MyCSIDriver) ListSnapshots(ctx context.Context, req *csi.ListSnapshotsRequest) (*csi.ListSnapshotsResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "ListSnapshots not implemented")
}

// Helper: set up loop device
func setupLoopDevice(backingFile string) (string, error) {
	// Use losetup to attach file as loop device
	out, err := execCommand("losetup", "-f", "--show", backingFile)
	if err != nil {
		return "", err
	}
	outstr := strings.TrimSuffix(string(out), "\n")
	return outstr, nil
}

// Helper: format device if not already formatted
func formatIfNeeded(device, fsType string) error {
	// Check if already formatted
	log.Printf("formatIfNeeded: checking %s", device)
	out, err := execCommand("blkid", device)
	if err == nil && len(out) > 0 {
		return nil // Already formatted
	}
	// Format
	log.Printf("formatIfNeeded: formatting %s with %s", device, fsType)
	out, err = execCommand("mkfs."+fsType, device)
	log.Printf("mkfs output: %s", string(out))
	return err
}

// Helper: mount device
func mountDevice(device, target, fsType string) error {
	_, err := execCommand("mount", "-t", fsType, device, target)
	return err
}

// Helper: run command and return output
func execCommand(name string, args ...string) ([]byte, error) {
	log.Printf("execCommand: %s %v", name, args)
	cmd := exec.Command(name, args...)
	return cmd.CombinedOutput()
}
