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

func TestController_GetCapabilities_Snapshot(t *testing.T) {
	cs := NewControllerServer("my-csi-driver", "v1.0.0")
	resp, err := cs.ControllerGetCapabilities(context.Background(), &csi.ControllerGetCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	foundCreateDelete := false
	foundList := false
	for _, cap := range resp.Capabilities {
		rpcType := cap.GetRpc().GetType()
		if rpcType == csi.ControllerServiceCapability_RPC_CREATE_DELETE_SNAPSHOT {
			foundCreateDelete = true
		}
		if rpcType == csi.ControllerServiceCapability_RPC_LIST_SNAPSHOTS {
			foundList = true
		}
	}

	if !foundCreateDelete {
		t.Errorf("Create/Delete snapshot capability not reported")
	}
	if !foundList {
		t.Errorf("List snapshots capability not reported")
	}
}

func TestController_CreateSnapshot(t *testing.T) {
	os.Setenv("CSI_BACKING_DIR", "/tmp/my-csi-driver")
	cs := NewControllerServer("test.csi", "0.1.0")
	backingDir := "/tmp/my-csi-driver"
	_ = os.MkdirAll(backingDir, 0750)

	// Create a source volume first
	volID := "vol-test-snapshot-source"
	backingFile := backingDir + "/" + volID + ".img"
	f, err := os.Create(backingFile)
	if err != nil {
		t.Fatalf("failed to create backing file: %v", err)
	}
	size := int64(1024 * 1024)
	if err := f.Truncate(size); err != nil {
		f.Close()
		t.Fatalf("failed to truncate backing file: %v", err)
	}
	// Write some test data
	testData := []byte("test data for snapshot")
	if _, err := f.Write(testData); err != nil {
		f.Close()
		t.Fatalf("failed to write test data: %v", err)
	}
	f.Close()

	// Create snapshot
	req := &csi.CreateSnapshotRequest{
		SourceVolumeId: volID,
		Name:           "test-snapshot",
	}

	resp, err := cs.CreateSnapshot(context.Background(), req)
	if err != nil {
		t.Fatalf("CreateSnapshot failed: %v", err)
	}

	if resp.Snapshot == nil {
		t.Fatalf("Snapshot not returned")
	}

	snapshotID := resp.Snapshot.SnapshotId
	if snapshotID == "" {
		t.Fatalf("SnapshotId is empty")
	}

	if resp.Snapshot.SourceVolumeId != volID {
		t.Errorf("expected source volume %s, got %s", volID, resp.Snapshot.SourceVolumeId)
	}

	if resp.Snapshot.SizeBytes != size {
		t.Errorf("expected size %d, got %d", size, resp.Snapshot.SizeBytes)
	}

	if !resp.Snapshot.ReadyToUse {
		t.Errorf("snapshot should be ready to use")
	}

	// Verify snapshot file exists
	snapshotFile := backingDir + "/" + snapshotID + ".snap"
	if _, err := os.Stat(snapshotFile); err != nil {
		t.Fatalf("snapshot file not created: %v", err)
	}

	// Verify snapshot data matches source
	snapData := make([]byte, len(testData))
	snapFile, err := os.Open(snapshotFile)
	if err != nil {
		t.Fatalf("failed to open snapshot file: %v", err)
	}
	defer snapFile.Close()
	if _, err := snapFile.Read(snapData); err != nil {
		t.Fatalf("failed to read snapshot data: %v", err)
	}
	if string(snapData) != string(testData) {
		t.Errorf("snapshot data mismatch: expected %s, got %s", testData, snapData)
	}

	// Clean up
	os.Remove(backingFile)
	os.Remove(snapshotFile)
}

func TestController_DeleteSnapshot(t *testing.T) {
	os.Setenv("CSI_BACKING_DIR", "/tmp/my-csi-driver")
	cs := NewControllerServer("test.csi", "0.1.0")
	backingDir := "/tmp/my-csi-driver"
	_ = os.MkdirAll(backingDir, 0750)

	snapshotID := "snap-test-delete"
	snapshotFile := backingDir + "/" + snapshotID + ".snap"

	// Create a snapshot file
	f, err := os.Create(snapshotFile)
	if err != nil {
		t.Fatalf("failed to create snapshot file: %v", err)
	}
	f.Close()

	// Verify file exists
	if _, err := os.Stat(snapshotFile); err != nil {
		t.Fatalf("snapshot file not found before delete: %v", err)
	}

	// Delete snapshot
	_, err = cs.DeleteSnapshot(context.Background(), &csi.DeleteSnapshotRequest{SnapshotId: snapshotID})
	if err != nil {
		t.Fatalf("DeleteSnapshot failed: %v", err)
	}

	// Verify file is deleted
	if _, err := os.Stat(snapshotFile); !os.IsNotExist(err) {
		t.Errorf("snapshot file still exists after delete")
	}

	// Delete non-existent snapshot should succeed
	_, err = cs.DeleteSnapshot(context.Background(), &csi.DeleteSnapshotRequest{SnapshotId: "snap-does-not-exist"})
	if err != nil {
		t.Fatalf("DeleteSnapshot for non-existent snapshot should succeed: %v", err)
	}
}

func TestController_ListSnapshots(t *testing.T) {
	os.Setenv("CSI_BACKING_DIR", "/tmp/my-csi-driver-list")
	cs := NewControllerServer("test.csi", "0.1.0")
	backingDir := "/tmp/my-csi-driver-list"
	_ = os.MkdirAll(backingDir, 0750)
	defer os.RemoveAll(backingDir)

	// Create some test snapshots
	snapshots := []string{"snap-1", "snap-2", "snap-3"}
	for _, snapID := range snapshots {
		snapFile := backingDir + "/" + snapID + ".snap"
		f, err := os.Create(snapFile)
		if err != nil {
			t.Fatalf("failed to create snapshot file: %v", err)
		}
		f.Truncate(1024)
		f.Close()
	}

	// Create a volume file (should not be listed)
	volFile := backingDir + "/vol-test.img"
	f, err := os.Create(volFile)
	if err != nil {
		t.Fatalf("failed to create volume file: %v", err)
	}
	f.Close()

	// List all snapshots
	resp, err := cs.ListSnapshots(context.Background(), &csi.ListSnapshotsRequest{})
	if err != nil {
		t.Fatalf("ListSnapshots failed: %v", err)
	}

	if len(resp.Entries) != len(snapshots) {
		t.Errorf("expected %d snapshots, got %d", len(snapshots), len(resp.Entries))
	}

	// Verify all snapshot IDs are present
	foundSnaps := make(map[string]bool)
	for _, entry := range resp.Entries {
		foundSnaps[entry.Snapshot.SnapshotId] = true
	}
	for _, snapID := range snapshots {
		if !foundSnaps[snapID] {
			t.Errorf("snapshot %s not found in list", snapID)
		}
	}

	// List specific snapshot
	resp, err = cs.ListSnapshots(context.Background(), &csi.ListSnapshotsRequest{SnapshotId: "snap-1"})
	if err != nil {
		t.Fatalf("ListSnapshots for specific snapshot failed: %v", err)
	}

	if len(resp.Entries) != 1 {
		t.Errorf("expected 1 snapshot, got %d", len(resp.Entries))
	}

	if len(resp.Entries) > 0 && resp.Entries[0].Snapshot.SnapshotId != "snap-1" {
		t.Errorf("expected snapshot snap-1, got %s", resp.Entries[0].Snapshot.SnapshotId)
	}

	// List non-existent snapshot
	resp, err = cs.ListSnapshots(context.Background(), &csi.ListSnapshotsRequest{SnapshotId: "snap-does-not-exist"})
	if err != nil {
		t.Fatalf("ListSnapshots for non-existent snapshot should not fail: %v", err)
	}

	if len(resp.Entries) != 0 {
		t.Errorf("expected 0 snapshots for non-existent ID, got %d", len(resp.Entries))
	}
}

func TestController_CreateVolumeFromSnapshot(t *testing.T) {
	os.Setenv("CSI_BACKING_DIR", "/tmp/my-csi-driver-restore")
	cs := NewControllerServer("test.csi", "0.1.0")
	backingDir := "/tmp/my-csi-driver-restore"
	_ = os.MkdirAll(backingDir, 0750)
	defer os.RemoveAll(backingDir)

	// Create a snapshot file with test data
	snapshotID := "snap-test-restore"
	snapshotFile := backingDir + "/" + snapshotID + ".snap"
	testData := []byte("snapshot data for restore")
	
	f, err := os.Create(snapshotFile)
	if err != nil {
		t.Fatalf("failed to create snapshot file: %v", err)
	}
	if err := f.Truncate(1024 * 1024); err != nil {
		f.Close()
		t.Fatalf("failed to truncate snapshot file: %v", err)
	}
	if _, err := f.Write(testData); err != nil {
		f.Close()
		t.Fatalf("failed to write test data: %v", err)
	}
	f.Close()

	// Create volume from snapshot
	req := &csi.CreateVolumeRequest{
		Name: "test-restored-volume",
		VolumeContentSource: &csi.VolumeContentSource{
			Type: &csi.VolumeContentSource_Snapshot{
				Snapshot: &csi.VolumeContentSource_SnapshotSource{
					SnapshotId: snapshotID,
				},
			},
		},
	}

	resp, err := cs.CreateVolume(context.Background(), req)
	if err != nil {
		t.Fatalf("CreateVolume from snapshot failed: %v", err)
	}

	if resp.Volume == nil {
		t.Fatalf("Volume not returned")
	}

	volID := resp.Volume.VolumeId
	if volID == "" {
		t.Fatalf("VolumeId is empty")
	}

	// Verify volume file exists
	volumeFile := backingDir + "/" + volID + ".img"
	if _, err := os.Stat(volumeFile); err != nil {
		t.Fatalf("volume file not created: %v", err)
	}

	// Verify volume data matches snapshot
	volData := make([]byte, len(testData))
	volFile, err := os.Open(volumeFile)
	if err != nil {
		t.Fatalf("failed to open volume file: %v", err)
	}
	defer volFile.Close()
	if _, err := volFile.Read(volData); err != nil {
		t.Fatalf("failed to read volume data: %v", err)
	}
	if string(volData) != string(testData) {
		t.Errorf("volume data mismatch: expected %s, got %s", testData, volData)
	}

	// Test creating volume from non-existent snapshot
	req.VolumeContentSource.GetSnapshot().SnapshotId = "snap-does-not-exist"
	_, err = cs.CreateVolume(context.Background(), req)
	if err == nil {
		t.Errorf("expected error for non-existent snapshot, got nil")
	}
}
