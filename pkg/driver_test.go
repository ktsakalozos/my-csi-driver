package pkg

import (
	"context"
	"os"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
)

func TestGetPluginCapabilities_ControllerService(t *testing.T) {
	d := NewMyCSIDriver("my-csi-driver", "v1.0.0", "node-1")
	resp, err := d.GetPluginCapabilities(context.Background(), &csi.GetPluginCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, cap := range resp.Capabilities {
		if cap.GetService().GetType() == csi.PluginCapability_Service_CONTROLLER_SERVICE {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Controller service capability not reported")
	}
}

func TestControllerGetCapabilities_CreateDeleteVolume(t *testing.T) {
	d := NewMyCSIDriver("my-csi-driver", "v1.0.0", "node-1")
	resp, err := d.ControllerGetCapabilities(context.Background(), &csi.ControllerGetCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, cap := range resp.Capabilities {
		if cap.GetRpc().GetType() == csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Create/Delete volume capability not reported")
	}
}

func TestNodePublishVolume(t *testing.T) {
	os.Setenv("CSI_BACKING_DIR", "/tmp/my-csi-driver")
	driver := NewMyCSIDriver("test.csi", "0.1.0", "test-node")

	// Create a volume first
	volReq := &csi.CreateVolumeRequest{
		Name: "testvol",
		CapacityRange: &csi.CapacityRange{
			RequiredBytes: 1024 * 1024, // 1MiB
		},
	}
	volResp, err := driver.CreateVolume(context.Background(), volReq)
	if err != nil {
		t.Fatalf("CreateVolume failed: %v", err)
	}
	backingFile := volResp.Volume.VolumeContext["backingFile"]

	// Prepare NodePublishVolume request
	nodeReq := &csi.NodePublishVolumeRequest{
		VolumeId:      volResp.Volume.VolumeId,
		TargetPath:    "/tmp/my-csi-driver/test-mount",
		VolumeContext: map[string]string{"backingFile": backingFile},
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{FsType: "ext4"},
			},
		},
	}

	// Call NodePublishVolume
	_, err = driver.NodePublishVolume(context.Background(), nodeReq)
	if err != nil {
		t.Logf("NodePublishVolume returned error (expected if not root): %v", err)
	}

	// Check that target path exists
	if _, err := os.Stat(nodeReq.TargetPath); err != nil {
		t.Errorf("TargetPath not created: %v", err)
	}

	// Cleanup
	os.RemoveAll(nodeReq.TargetPath)
	os.Remove(backingFile)
}

func TestNodeUnpublishVolume(t *testing.T) {
	os.Setenv("CSI_BACKING_DIR", "/tmp/my-csi-driver")
	driver := NewMyCSIDriver("test.csi", "0.1.0", "test-node")

	// Setup: create a target directory
	target := "/tmp/my-csi-driver/test-mount-unpub"
	if err := os.MkdirAll(target, 0750); err != nil {
		t.Fatalf("failed to create target dir: %v", err)
	}

	// Simulate a mount by creating a dummy file (real mount/loop device requires root)
	dummyFile := target + "/dummy"
	f, err := os.Create(dummyFile)
	if err != nil {
		t.Fatalf("failed to create dummy file: %v", err)
	}
	f.Close()

	// Call NodeUnpublishVolume
	req := &csi.NodeUnpublishVolumeRequest{TargetPath: target}
	_, err = driver.NodeUnpublishVolume(context.Background(), req)
	if err != nil {
		t.Logf("NodeUnpublishVolume returned error (expected if not root): %v", err)
	}

	// Cleanup
	os.RemoveAll(target)
}

func TestCreateVolume(t *testing.T) {
	os.Setenv("CSI_BACKING_DIR", "/tmp/my-csi-driver")
	driver := NewMyCSIDriver("test.csi", "0.1.0", "test-node")

	req := &csi.CreateVolumeRequest{
		Name: "testvol",
		CapacityRange: &csi.CapacityRange{
			RequiredBytes: 1024 * 1024, // 1MiB
		},
	}

	resp, err := driver.CreateVolume(context.Background(), req)
	if err != nil {
		t.Fatalf("CreateVolume failed: %v", err)
	}

	if resp.Volume == nil {
		t.Fatalf("Volume not returned")
	}

	backingFile := resp.Volume.VolumeContext["backingFile"]
	if backingFile == "" {
		t.Fatalf("backingFile not set in VolumeContext")
	}

	info, err := os.Stat(backingFile)
	if err != nil {
		t.Fatalf("backing file not created: %v", err)
	}

	if info.Size() != 1024*1024 {
		t.Errorf("expected file size 1MiB, got %d", info.Size())
	}

	// Cleanup
	os.Remove(backingFile)
}

func TestDeleteVolume(t *testing.T) {
	os.Setenv("CSI_BACKING_DIR", "/tmp/my-csi-driver")
	driver := NewMyCSIDriver("test.csi", "0.1.0", "test-node")

	volID := "vol-test-delete"
	backingFile := "/tmp/my-csi-driver/" + volID + ".img"

	// Create a dummy backing file
	if err := os.MkdirAll("/tmp/my-csi-driver", 0750); err != nil {
		t.Fatalf("failed to create backing dir: %v", err)
	}
	f, err := os.Create(backingFile)
	if err != nil {
		t.Fatalf("failed to create backing file: %v", err)
	}
	f.Close()

	// Ensure file exists
	if _, err := os.Stat(backingFile); err != nil {
		t.Fatalf("backing file not found before delete: %v", err)
	}

	// Call DeleteVolume
	req := &csi.DeleteVolumeRequest{VolumeId: volID}
	_, err = driver.DeleteVolume(context.Background(), req)
	if err != nil {
		t.Fatalf("DeleteVolume failed: %v", err)
	}

	// Ensure file is deleted
	if _, err := os.Stat(backingFile); !os.IsNotExist(err) {
		t.Errorf("backing file still exists after delete")
	}
}

func TestControllerGetVolume(t *testing.T) {
	d := NewMyCSIDriver("test-driver", "0.1.0", "node-1")
	os.Setenv("CSI_BACKING_DIR", "/tmp/my-csi-driver")
	backingDir := "/tmp/my-csi-driver"
	_ = os.MkdirAll(backingDir, 0750)

	volID := "vol-test-getvolume"
	backingFile := backingDir + "/" + volID + ".img"
	f, err := os.Create(backingFile)
	if err != nil {
		t.Fatalf("failed to create backing file: %v", err)
	}
	size := int64(123456)
	if err := f.Truncate(size); err != nil {
		f.Close()
		t.Fatalf("failed to truncate backing file: %v", err)
	}
	f.Close()

	// Should find the volume
	resp, err := d.ControllerGetVolume(context.Background(), &csi.ControllerGetVolumeRequest{VolumeId: volID})
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if resp.Volume == nil || resp.Volume.VolumeId != volID {
		t.Errorf("unexpected volume info: %+v", resp.Volume)
	}
	if resp.Volume.CapacityBytes != size {
		t.Errorf("expected size %d, got %d", size, resp.Volume.CapacityBytes)
	}

	// Should return NotFound for missing volume
	_, err = d.ControllerGetVolume(context.Background(), &csi.ControllerGetVolumeRequest{VolumeId: "vol-does-not-exist"})
	if err == nil {
		t.Errorf("expected error for missing volume, got nil")
	}
}
