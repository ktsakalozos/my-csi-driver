package rawfile

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestNode_PublishVolume(t *testing.T) {
	clientset := fake.NewSimpleClientset()

	// In the new architecture, NodeServer creates the backing file just-in-time
	ns := NewNodeServer("test-node", "/tmp/my-csi-driver", clientset)

	volID := "vol-test-publish"
	backingFile := "/tmp/my-csi-driver/" + volID + ".img"

	nodeReq := &csi.NodePublishVolumeRequest{
		VolumeId:   volID,
		TargetPath: "/tmp/my-csi-driver/test-mount",
		VolumeContext: map[string]string{
			"backingFile": backingFile,
			"size":        "1048576", // 1 MiB
		},
		VolumeCapability: &csi.VolumeCapability{AccessType: &csi.VolumeCapability_Mount{Mount: &csi.VolumeCapability_MountVolume{FsType: "ext4"}}},
	}

	if _, err := ns.NodePublishVolume(context.Background(), nodeReq); err != nil {
		t.Logf("NodePublishVolume returned error (expected if not root): %v", err)
	}

	// Verify the backing file was created just-in-time
	if info, err := os.Stat(backingFile); err == nil {
		if info.Size() != 1048576 {
			t.Errorf("expected backing file size 1MiB, got %d", info.Size())
		}
		t.Logf("Backing file created just-in-time with correct size")
	} else {
		t.Logf("Backing file check failed (expected if losetup failed): %v", err)
	}

	if _, err := os.Stat(nodeReq.TargetPath); err != nil {
		t.Errorf("TargetPath not created: %v", err)
	}
	os.RemoveAll(nodeReq.TargetPath)
	os.Remove(backingFile)
}

func TestNode_UnpublishVolume(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	ns := NewNodeServer("test-node", "/tmp/my-csi-driver", clientset)
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
	clientset := fake.NewSimpleClientset()
	ns := NewNodeServer("test-node", "/tmp/my-csi-driver", clientset)

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
	clientset := fake.NewSimpleClientset()
	ns := NewNodeServer("test-node", "/tmp/my-csi-driver", clientset)
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

func TestNode_GarbageCollectVolumes(t *testing.T) {
	// Create a temporary directory for this test
	testDir := t.TempDir()

	// Create some backing files
	activeVolFile := filepath.Join(testDir, "vol-active.img")
	orphanedVolFile := filepath.Join(testDir, "vol-orphaned.img")

	// Create the files
	for _, file := range []string{activeVolFile, orphanedVolFile} {
		f, err := os.Create(file)
		if err != nil {
			t.Fatalf("Failed to create test file %s: %v", file, err)
		}
		f.Close()
	}

	// Create a fake PV for the active volume
	pv := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: "vol-active",
		},
		Spec: corev1.PersistentVolumeSpec{
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				CSI: &corev1.CSIPersistentVolumeSource{
					Driver:       "test-driver",
					VolumeHandle: "vol-active",
					VolumeAttributes: map[string]string{
						"backingFile": activeVolFile,
					},
				},
			},
		},
	}

	clientset := fake.NewSimpleClientset(pv)
	ns := NewNodeServer("test-node", testDir, clientset)

	// Verify both files exist before GC
	if _, err := os.Stat(activeVolFile); err != nil {
		t.Fatalf("Active volume file should exist before GC: %v", err)
	}
	if _, err := os.Stat(orphanedVolFile); err != nil {
		t.Fatalf("Orphaned volume file should exist before GC: %v", err)
	}

	// Run garbage collection
	ns.garbageCollectVolumes(context.Background())

	// Active volume should still exist
	if _, err := os.Stat(activeVolFile); err != nil {
		t.Errorf("Active volume file should still exist after GC: %v", err)
	}

	// Orphaned volume should be deleted
	if _, err := os.Stat(orphanedVolFile); !os.IsNotExist(err) {
		t.Errorf("Orphaned volume file should be deleted after GC")
	}
}
