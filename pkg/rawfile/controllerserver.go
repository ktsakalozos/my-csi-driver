package rawfile

import (
	"context"
	"fmt"
	"os"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	klog "k8s.io/klog/v2"
)

// ControllerServer implements the CSI Controller service endpoints.
type ControllerServer struct {
	name       string
	version    string
	backingDir string
	nodeID     string
	csi.UnimplementedControllerServer
}

// Compile-time assertion
var _ csi.ControllerServer = (*ControllerServer)(nil)

// NewControllerServer creates a controller with backingDir resolved from env/defaults.
// It preserves previous behavior where CSI_BACKING_DIR env var was used.
func NewControllerServer(name, version string) *ControllerServer {
	dir := os.Getenv("CSI_BACKING_DIR")
	if dir == "" {
		dir = "/var/lib/my-csi-driver"
	}
	return &ControllerServer{name: name, version: version, backingDir: dir, nodeID: ""}
}

// NewControllerServerWithBackingDir creates a controller with an explicit backingDir.
func NewControllerServerWithBackingDir(name, version, backingDir string) *ControllerServer {
	dir := backingDir
	if dir == "" {
		// Fall back to env, then default
		dir = os.Getenv("CSI_BACKING_DIR")
		if dir == "" {
			dir = "/var/lib/my-csi-driver"
		}
	}
	return &ControllerServer{name: name, version: version, backingDir: dir, nodeID: ""}
}

// NewControllerServerWithNodeID creates a controller with explicit backingDir and nodeID for topology awareness.
func NewControllerServerWithNodeID(name, version, backingDir, nodeID string) *ControllerServer {
	dir := backingDir
	if dir == "" {
		// Fall back to env, then default
		dir = os.Getenv("CSI_BACKING_DIR")
		if dir == "" {
			dir = "/var/lib/my-csi-driver"
		}
	}
	return &ControllerServer{name: name, version: version, backingDir: dir, nodeID: nodeID}
}

func (cs *ControllerServer) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	volID := "vol-" + uuid.New().String()
	klog.Infof("CreateVolume: %s", volID)

	// Get volume size in bytes
	size := req.CapacityRange.GetRequiredBytes()
	if size == 0 {
		size = 1 << 30 // Default to 1GiB
	}

	// Ensure backing directory exists
	if err := os.MkdirAll(cs.backingDir, 0750); err != nil {
		return nil, err
	}
	backingFile := cs.backingDir + "/" + volID + ".img"
	klog.Infof("CreateVolume backingFile: %s", backingFile)

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
	resp := &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			VolumeId:      volID,
			CapacityBytes: size,
			VolumeContext: map[string]string{
				"backingFile": backingFile,
			},
		},
	}

	// Add topology information if nodeID is set
	if cs.nodeID != "" {
		resp.Volume.AccessibleTopology = []*csi.Topology{
			{
				Segments: map[string]string{
					"topology.kubernetes.io/hostname": cs.nodeID,
				},
			},
		}
	}

	return resp, nil
}

func (cs *ControllerServer) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	klog.Infof("DeleteVolume: %s", req.VolumeId)

	backingFile := cs.backingDir + "/" + req.VolumeId + ".img"
	klog.Infof("DeleteVolume backingFile: %s", backingFile)

	// Remove backing file if it exists
	if err := os.Remove(backingFile); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to remove backing file: %v", err)
	}

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
	return &csi.ControllerGetCapabilitiesResponse{Capabilities: ctrlCaps}, nil
}

func (cs *ControllerServer) ControllerGetVolume(ctx context.Context, req *csi.ControllerGetVolumeRequest) (*csi.ControllerGetVolumeResponse, error) {
	backingFile := cs.backingDir + "/" + req.VolumeId + ".img"
	klog.Infof("ControllerGetVolume backingFile: %s", backingFile)

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
	return nil, status.Errorf(codes.Unimplemented, "CreateSnapshot not implemented")
}

func (cs *ControllerServer) DeleteSnapshot(ctx context.Context, req *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "DeleteSnapshot not implemented")
}

func (cs *ControllerServer) ListSnapshots(ctx context.Context, req *csi.ListSnapshotsRequest) (*csi.ListSnapshotsResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "ListSnapshots not implemented")
}
