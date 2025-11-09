package rawfile

import (
	"context"
	"os"
	"path/filepath"
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

func TestNode_GetVolumeStats(t *testing.T) {
	ns := NewNodeServer("test-node")

	// Test 1: Missing volume path should return error
	t.Run("MissingVolumePath", func(t *testing.T) {
		req := &csi.NodeGetVolumeStatsRequest{
			VolumeId:   "test-vol",
			VolumePath: "",
		}
		_, err := ns.NodeGetVolumeStats(context.Background(), req)
		if err == nil {
			t.Error("Expected error for missing volume path, got nil")
		}
	})

	// Test 2: Non-existent path should return error
	t.Run("NonExistentPath", func(t *testing.T) {
		// Use a path that doesn't exist within a temp directory
		nonExistentPath := filepath.Join(t.TempDir(), "nonexistent-subdir")
		req := &csi.NodeGetVolumeStatsRequest{
			VolumeId:   "test-vol",
			VolumePath: nonExistentPath,
		}
		_, err := ns.NodeGetVolumeStats(context.Background(), req)
		if err == nil {
			t.Error("Expected error for non-existent path, got nil")
		}
	})

	// Test 3: Valid path should return stats
	t.Run("ValidPath", func(t *testing.T) {
		// Use t.TempDir() for automatic cleanup and test isolation
		testPath := t.TempDir()

		req := &csi.NodeGetVolumeStatsRequest{
			VolumeId:   "test-vol",
			VolumePath: testPath,
		}
		resp, err := ns.NodeGetVolumeStats(context.Background(), req)
		if err != nil {
			t.Fatalf("NodeGetVolumeStats failed: %v", err)
		}

		// Verify response structure
		if resp == nil {
			t.Fatal("Expected non-nil response")
		}
		if len(resp.Usage) != 1 {
			t.Fatalf("Expected 1 usage entry, got %d", len(resp.Usage))
		}

		usage := resp.Usage[0]
		if usage.Unit != csi.VolumeUsage_BYTES {
			t.Errorf("Expected unit BYTES, got %v", usage.Unit)
		}
		if usage.Total <= 0 {
			t.Errorf("Expected positive total, got %d", usage.Total)
		}
		if usage.Available < 0 {
			t.Errorf("Expected non-negative available, got %d", usage.Available)
		}
		if usage.Available > usage.Total {
			t.Errorf("Expected available <= total, got available=%d, total=%d", usage.Available, usage.Total)
		}
		t.Logf("Stats: total=%d bytes, available=%d bytes", usage.Total, usage.Available)
	})
}

func TestNode_GetCapabilities(t *testing.T) {
	ns := NewNodeServer("test-node")
	resp, err := ns.NodeGetCapabilities(context.Background(), &csi.NodeGetCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("NodeGetCapabilities failed: %v", err)
	}

	// Check that GET_VOLUME_STATS capability is advertised
	found := false
	for _, cap := range resp.Capabilities {
		if cap.GetRpc() != nil && cap.GetRpc().Type == csi.NodeServiceCapability_RPC_GET_VOLUME_STATS {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected GET_VOLUME_STATS capability to be advertised")
	}
}
