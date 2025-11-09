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
	return &ControllerServer{name: name, version: version, backingDir: dir}
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
	return &ControllerServer{name: name, version: version, backingDir: dir}
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

	// Check if creating from a snapshot
	if req.VolumeContentSource != nil {
		snapshot := req.VolumeContentSource.GetSnapshot()
		if snapshot != nil {
			snapshotID := snapshot.GetSnapshotId()
			klog.Infof("CreateVolume from snapshot: %s", snapshotID)

			snapshotFile := cs.backingDir + "/" + snapshotID + ".snap"
			if _, err := os.Stat(snapshotFile); err != nil {
				if os.IsNotExist(err) {
					return nil, status.Errorf(codes.NotFound, "snapshot %s not found", snapshotID)
				}
				return nil, status.Errorf(codes.Internal, "error accessing snapshot: %v", err)
			}

			// Copy snapshot file to new volume file
			if err := copyFile(snapshotFile, backingFile); err != nil {
				return nil, status.Errorf(codes.Internal, "failed to copy snapshot to volume: %v", err)
			}

			// Get actual size from copied file
			fi, err := os.Stat(backingFile)
			if err != nil {
				return nil, status.Errorf(codes.Internal, "failed to stat volume file: %v", err)
			}
			size = fi.Size()
		}
	} else {
		// Create backing file
		f, err := os.Create(backingFile)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		if err := f.Truncate(size); err != nil {
			return nil, err
		}
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
	sourceVolumeID := req.GetSourceVolumeId()
	if sourceVolumeID == "" {
		return nil, status.Error(codes.InvalidArgument, "source volume ID is required")
	}

	snapshotID := "snap-" + uuid.New().String()
	klog.Infof("CreateSnapshot: %s from volume %s", snapshotID, sourceVolumeID)

	// Check if source volume exists
	sourceFile := cs.backingDir + "/" + sourceVolumeID + ".img"
	fi, err := os.Stat(sourceFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, status.Errorf(codes.NotFound, "source volume %s not found", sourceVolumeID)
		}
		return nil, status.Errorf(codes.Internal, "error accessing source volume: %v", err)
	}

	// Ensure backing directory exists
	if err := os.MkdirAll(cs.backingDir, 0750); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create backing directory: %v", err)
	}

	// Create snapshot file by copying the source volume
	snapshotFile := cs.backingDir + "/" + snapshotID + ".snap"
	klog.Infof("CreateSnapshot backingFile: %s", snapshotFile)

	if err := copyFile(sourceFile, snapshotFile); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create snapshot: %v", err)
	}

	// Return snapshot response
	return &csi.CreateSnapshotResponse{
		Snapshot: &csi.Snapshot{
			SnapshotId:     snapshotID,
			SourceVolumeId: sourceVolumeID,
			SizeBytes:      fi.Size(),
			ReadyToUse:     true,
		},
	}, nil
}

func (cs *ControllerServer) DeleteSnapshot(ctx context.Context, req *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	snapshotID := req.GetSnapshotId()
	if snapshotID == "" {
		return nil, status.Error(codes.InvalidArgument, "snapshot ID is required")
	}

	klog.Infof("DeleteSnapshot: %s", snapshotID)

	snapshotFile := cs.backingDir + "/" + snapshotID + ".snap"
	klog.Infof("DeleteSnapshot backingFile: %s", snapshotFile)

	// Remove snapshot file if it exists
	if err := os.Remove(snapshotFile); err != nil && !os.IsNotExist(err) {
		return nil, status.Errorf(codes.Internal, "failed to remove snapshot file: %v", err)
	}

	return &csi.DeleteSnapshotResponse{}, nil
}

func (cs *ControllerServer) ListSnapshots(ctx context.Context, req *csi.ListSnapshotsRequest) (*csi.ListSnapshotsResponse, error) {
	klog.Infof("ListSnapshots called")

	// If a specific snapshot ID is requested, return only that snapshot
	if req.GetSnapshotId() != "" {
		snapshotID := req.GetSnapshotId()
		snapshotFile := cs.backingDir + "/" + snapshotID + ".snap"

		fi, err := os.Stat(snapshotFile)
		if err != nil {
			if os.IsNotExist(err) {
				// Snapshot not found, return empty list
				return &csi.ListSnapshotsResponse{}, nil
			}
			return nil, status.Errorf(codes.Internal, "error accessing snapshot: %v", err)
		}

		// Extract source volume ID from snapshot metadata file if it exists
		// For simplicity, we'll return empty source volume ID
		return &csi.ListSnapshotsResponse{
			Entries: []*csi.ListSnapshotsResponse_Entry{
				{
					Snapshot: &csi.Snapshot{
						SnapshotId:     snapshotID,
						SourceVolumeId: "",
						SizeBytes:      fi.Size(),
						ReadyToUse:     true,
					},
				},
			},
		}, nil
	}

	// List all snapshots in the backing directory
	entries, err := os.ReadDir(cs.backingDir)
	if err != nil {
		if os.IsNotExist(err) {
			return &csi.ListSnapshotsResponse{}, nil
		}
		return nil, status.Errorf(codes.Internal, "failed to read backing directory: %v", err)
	}

	var snapshots []*csi.ListSnapshotsResponse_Entry
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		// Only list .snap files
		name := entry.Name()
		if len(name) < 5 || name[len(name)-5:] != ".snap" {
			continue
		}

		// Extract snapshot ID (remove .snap extension)
		snapshotID := name[:len(name)-5]

		// If filtering by source volume, skip non-matching snapshots
		if req.GetSourceVolumeId() != "" {
			// For now, we don't have source volume metadata, so we can't filter
			// In a production implementation, you'd store metadata alongside snapshots
			continue
		}

		fi, err := entry.Info()
		if err != nil {
			klog.Warningf("Failed to get info for snapshot %s: %v", name, err)
			continue
		}

		snapshots = append(snapshots, &csi.ListSnapshotsResponse_Entry{
			Snapshot: &csi.Snapshot{
				SnapshotId:     snapshotID,
				SourceVolumeId: "",
				SizeBytes:      fi.Size(),
				ReadyToUse:     true,
			},
		})
	}

	return &csi.ListSnapshotsResponse{
		Entries: snapshots,
	}, nil
}
