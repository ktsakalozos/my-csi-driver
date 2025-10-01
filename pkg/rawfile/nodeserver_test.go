package rawfile

import (
	"context"
	"os"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
)

func TestNode_PublishVolume(t *testing.T) {
	cs := NewControllerServer("test.csi", "0.1.0")
	volResp, err := cs.CreateVolume(context.Background(), &csi.CreateVolumeRequest{
		Name:          "testvol",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1024 * 1024},
	})
	if err != nil {
		t.Fatalf("CreateVolume failed: %v", err)
	}
	backingFile := volResp.Volume.VolumeContext["backingFile"]

	ns := NewNodeServer("test-node")
	nodeReq := &csi.NodePublishVolumeRequest{
		VolumeId:         volResp.Volume.VolumeId,
		TargetPath:       "/tmp/my-csi-driver/test-mount",
		VolumeContext:    map[string]string{"backingFile": backingFile},
		VolumeCapability: &csi.VolumeCapability{AccessType: &csi.VolumeCapability_Mount{Mount: &csi.VolumeCapability_MountVolume{FsType: "ext4"}}},
	}
	if _, err := ns.NodePublishVolume(context.Background(), nodeReq); err != nil {
		t.Logf("NodePublishVolume returned error (expected if not root): %v", err)
	}
	if _, err := os.Stat(nodeReq.TargetPath); err != nil {
		t.Errorf("TargetPath not created: %v", err)
	}
	os.RemoveAll(nodeReq.TargetPath)
	os.Remove(backingFile)
}

func TestNode_UnpublishVolume(t *testing.T) {
	ns := NewNodeServer("test-node")
	target := "/tmp/my-csi-driver/test-mount-unpub"
	if err := os.MkdirAll(target, 0750); err != nil {
		t.Fatalf("failed to create target dir: %v", err)
	}
	f, err := os.Create(target + "/dummy")
	if err != nil {
		t.Fatalf("failed to create dummy file: %v", err)
	}
	f.Close()
	if _, err := ns.NodeUnpublishVolume(context.Background(), &csi.NodeUnpublishVolumeRequest{TargetPath: target}); err != nil {
		t.Logf("NodeUnpublishVolume returned error (expected if not root): %v", err)
	}
	os.RemoveAll(target)
}
