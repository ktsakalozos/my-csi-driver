package rawfile

import (
	"context"
	"os"
	"strconv"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

	// Return volume context with file path and size metadata
	return &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			VolumeId:      volID,
			CapacityBytes: size,
			VolumeContext: map[string]string{
				"backingFile": backingFile,
				"size":        strconv.FormatInt(size, 10),
			},
		},
	}, nil
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
	return &csi.ControllerGetCapabilitiesResponse{Capabilities: ctrlCaps}, nil
}

func (cs *ControllerServer) ControllerGetVolume(ctx context.Context, req *csi.ControllerGetVolumeRequest) (*csi.ControllerGetVolumeResponse, error) {
	klog.Infof("ControllerGetVolume: %s (fetching from Kubernetes API)", req.VolumeId)

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

	// Extract backing file from volume attributes
	backingFile := ""
	if pv.Spec.CSI.VolumeAttributes != nil {
		backingFile = pv.Spec.CSI.VolumeAttributes["backingFile"]
	}

	// Return volume info from Kubernetes API
	return &csi.ControllerGetVolumeResponse{
		Volume: &csi.Volume{
			VolumeId:      req.VolumeId,
			CapacityBytes: capacityBytes,
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
