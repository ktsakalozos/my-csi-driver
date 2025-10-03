package rawfile

import (
	"context"
	"os"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
)

func TestController_GetCapabilities_CreateDeleteVolume(t *testing.T) {
	cs := NewControllerServer("my-csi-driver", "v1.0.0")
	resp, err := cs.ControllerGetCapabilities(context.Background(), &csi.ControllerGetCapabilitiesRequest{})
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

func TestController_CreateVolume(t *testing.T) {
	os.Setenv("CSI_BACKING_DIR", "/tmp/my-csi-driver")
	cs := NewControllerServer("test.csi", "0.1.0")

	req := &csi.CreateVolumeRequest{
		Name:          "testvol",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1024 * 1024},
	}

	resp, err := cs.CreateVolume(context.Background(), req)
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

	os.Remove(backingFile)
}

func TestController_DeleteVolume(t *testing.T) {
	os.Setenv("CSI_BACKING_DIR", "/tmp/my-csi-driver")
	cs := NewControllerServer("test.csi", "0.1.0")

	volID := "vol-test-delete"
	backingFile := "/tmp/my-csi-driver/" + volID + ".img"

	if err := os.MkdirAll("/tmp/my-csi-driver", 0750); err != nil {
		t.Fatalf("failed to create backing dir: %v", err)
	}
	f, err := os.Create(backingFile)
	if err != nil {
		t.Fatalf("failed to create backing file: %v", err)
	}
	f.Close()

	if _, err := os.Stat(backingFile); err != nil {
		t.Fatalf("backing file not found before delete: %v", err)
	}

	_, err = cs.DeleteVolume(context.Background(), &csi.DeleteVolumeRequest{VolumeId: volID})
	if err != nil {
		t.Fatalf("DeleteVolume failed: %v", err)
	}

	if _, err := os.Stat(backingFile); !os.IsNotExist(err) {
		t.Errorf("backing file still exists after delete")
	}
}

func TestController_GetVolume(t *testing.T) {
	cs := NewControllerServer("test-driver", "0.1.0")
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

	resp, err := cs.ControllerGetVolume(context.Background(), &csi.ControllerGetVolumeRequest{VolumeId: volID})
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if resp.Volume == nil || resp.Volume.VolumeId != volID {
		t.Errorf("unexpected volume info: %+v", resp.Volume)
	}
	if resp.Volume.CapacityBytes != size {
		t.Errorf("expected size %d, got %d", size, resp.Volume.CapacityBytes)
	}

	if _, err = cs.ControllerGetVolume(context.Background(), &csi.ControllerGetVolumeRequest{VolumeId: "vol-does-not-exist"}); err == nil {
		t.Errorf("expected error for missing volume, got nil")
	}
}
