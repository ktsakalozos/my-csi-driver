package rawfile

import (
	"context"
	"os"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestController_GetCapabilities_CreateDeleteVolume(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	cs := NewControllerServer("my-csi-driver", "v1.0.0", clientset)
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
	clientset := fake.NewSimpleClientset()
	cs := NewControllerServerWithBackingDir("test.csi", "0.1.0", "/tmp/my-csi-driver", clientset)

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

	// Verify size is in context
	sizeStr := resp.Volume.VolumeContext["size"]
	if sizeStr == "" {
		t.Fatalf("size not set in VolumeContext")
	}

	// In the new architecture, the file is NOT created by the controller
	// It will be created just-in-time by the node server
	if _, err := os.Stat(backingFile); err == nil {
		t.Errorf("backing file should not be created by controller in new architecture")
		os.Remove(backingFile)
	}
}

func TestController_DeleteVolume(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	cs := NewControllerServerWithBackingDir("test.csi", "0.1.0", "/tmp/my-csi-driver", clientset)

	volID := "vol-test-delete"
	backingFile := "/tmp/my-csi-driver/" + volID + ".img"

	// Create a backing file to simulate existing volume
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

	// In the new architecture, DeleteVolume is a no-op
	_, err = cs.DeleteVolume(context.Background(), &csi.DeleteVolumeRequest{VolumeId: volID})
	if err != nil {
		t.Fatalf("DeleteVolume failed: %v", err)
	}

	// File should still exist - garbage collector will clean it up
	if _, err := os.Stat(backingFile); err != nil {
		t.Errorf("backing file should still exist after logical delete (will be cleaned by GC)")
	}

	// Clean up the test file
	os.Remove(backingFile)
}

func TestController_GetVolume(t *testing.T) {
	// Create a fake PV
	pv := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: "vol-test-getvolume",
		},
		Spec: corev1.PersistentVolumeSpec{
			Capacity: corev1.ResourceList{
				corev1.ResourceStorage: resource.MustParse("123456"),
			},
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				CSI: &corev1.CSIPersistentVolumeSource{
					Driver:       "test-driver",
					VolumeHandle: "vol-test-getvolume",
					VolumeAttributes: map[string]string{
						"backingFile": "/tmp/my-csi-driver/vol-test-getvolume.img",
					},
				},
			},
		},
	}

	clientset := fake.NewSimpleClientset(pv)
	cs := NewControllerServerWithBackingDir("test-driver", "0.1.0", "/tmp/my-csi-driver", clientset)

	volID := "vol-test-getvolume"
	resp, err := cs.ControllerGetVolume(context.Background(), &csi.ControllerGetVolumeRequest{VolumeId: volID})
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if resp.Volume == nil || resp.Volume.VolumeId != volID {
		t.Errorf("unexpected volume info: %+v", resp.Volume)
	}
	if resp.Volume.CapacityBytes != 123456 {
		t.Errorf("expected size %d, got %d", 123456, resp.Volume.CapacityBytes)
	}

	// Test non-existent volume
	if _, err = cs.ControllerGetVolume(context.Background(), &csi.ControllerGetVolumeRequest{VolumeId: "vol-does-not-exist"}); err == nil {
		t.Errorf("expected error for missing volume, got nil")
	}
}

func TestController_CreateVolume_WithTopology(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	cs := NewControllerServerWithBackingDir("test.csi", "0.1.0", "/tmp/my-csi-driver", clientset)

	// Test with preferred topology
	req := &csi.CreateVolumeRequest{
		Name:          "testvol-topology",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1024 * 1024},
		AccessibilityRequirements: &csi.TopologyRequirement{
			Preferred: []*csi.Topology{
				{
					Segments: map[string]string{
						"topology.kubernetes.io/hostname": "test-node-1",
					},
				},
			},
		},
	}

	resp, err := cs.CreateVolume(context.Background(), req)
	if err != nil {
		t.Fatalf("CreateVolume failed: %v", err)
	}

	if resp.Volume == nil {
		t.Fatalf("Volume not returned")
	}

	// Check that topology is set from preferred
	if resp.Volume.AccessibleTopology == nil || len(resp.Volume.AccessibleTopology) == 0 {
		t.Fatalf("AccessibleTopology not set")
	}

	topology := resp.Volume.AccessibleTopology[0]
	if topology.Segments == nil {
		t.Fatalf("Topology segments not set")
	}

	hostname, ok := topology.Segments["topology.kubernetes.io/hostname"]
	if !ok {
		t.Fatalf("topology.kubernetes.io/hostname not found in topology segments")
	}

	if hostname != "test-node-1" {
		t.Errorf("expected hostname 'test-node-1', got '%s'", hostname)
	}
}

func TestController_CreateVolume_WithRequisiteTopology(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	cs := NewControllerServerWithBackingDir("test.csi", "0.1.0", "/tmp/my-csi-driver", clientset)

	// Test with requisite topology (no preferred)
	req := &csi.CreateVolumeRequest{
		Name:          "testvol-requisite",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1024 * 1024},
		AccessibilityRequirements: &csi.TopologyRequirement{
			Requisite: []*csi.Topology{
				{
					Segments: map[string]string{
						"topology.kubernetes.io/hostname": "test-node-2",
					},
				},
			},
		},
	}

	resp, err := cs.CreateVolume(context.Background(), req)
	if err != nil {
		t.Fatalf("CreateVolume failed: %v", err)
	}

	if resp.Volume == nil {
		t.Fatalf("Volume not returned")
	}

	// Check that topology is set from requisite
	if resp.Volume.AccessibleTopology == nil || len(resp.Volume.AccessibleTopology) == 0 {
		t.Fatalf("AccessibleTopology not set")
	}

	topology := resp.Volume.AccessibleTopology[0]
	hostname := topology.Segments["topology.kubernetes.io/hostname"]
	if hostname != "test-node-2" {
		t.Errorf("expected hostname 'test-node-2', got '%s'", hostname)
	}
}

func TestController_CreateVolume_WithoutTopology(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	cs := NewControllerServerWithBackingDir("test.csi", "0.1.0", "/tmp/my-csi-driver", clientset)

	// Test without topology requirements
	req := &csi.CreateVolumeRequest{
		Name:          "testvol-no-topology",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1024 * 1024},
	}

	resp, err := cs.CreateVolume(context.Background(), req)
	if err != nil {
		t.Fatalf("CreateVolume failed: %v", err)
	}

	if resp.Volume == nil {
		t.Fatalf("Volume not returned")
	}

	// Check that topology is not set when no requirements provided
	if resp.Volume.AccessibleTopology != nil && len(resp.Volume.AccessibleTopology) > 0 {
		t.Errorf("AccessibleTopology should not be set when no requirements provided")
	}
}

func TestController_GetCapabilities_Snapshots(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	cs := NewControllerServer("my-csi-driver", "v1.0.0", clientset)
	resp, err := cs.ControllerGetCapabilities(context.Background(), &csi.ControllerGetCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	foundCreateDelete := false
	foundList := false
	for _, cap := range resp.Capabilities {
		if cap.GetRpc().GetType() == csi.ControllerServiceCapability_RPC_CREATE_DELETE_SNAPSHOT {
			foundCreateDelete = true
		}
		if cap.GetRpc().GetType() == csi.ControllerServiceCapability_RPC_LIST_SNAPSHOTS {
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

func TestController_CreateVolume_FromSnapshot(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	cs := NewControllerServerWithBackingDir("test.csi", "0.1.0", "/tmp/my-csi-driver", clientset)

	snapID := "snap-test-123"
	req := &csi.CreateVolumeRequest{
		Name:          "testvol-from-snap",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1024 * 1024},
		VolumeContentSource: &csi.VolumeContentSource{
			Type: &csi.VolumeContentSource_Snapshot{
				Snapshot: &csi.VolumeContentSource_SnapshotSource{
					SnapshotId: snapID,
				},
			},
		},
	}

	resp, err := cs.CreateVolume(context.Background(), req)
	if err != nil {
		t.Fatalf("CreateVolume failed: %v", err)
	}

	if resp.Volume == nil {
		t.Fatalf("Volume not returned")
	}

	// Verify snapshot context is set
	restoreFrom := resp.Volume.VolumeContext["restoreFromSnapshot"]
	if restoreFrom != snapID {
		t.Errorf("expected restoreFromSnapshot=%s, got %s", snapID, restoreFrom)
	}

	snapFile := resp.Volume.VolumeContext["snapshotFile"]
	expectedSnapFile := "/tmp/my-csi-driver/snap-" + snapID + ".img"
	if snapFile != expectedSnapFile {
		t.Errorf("expected snapshotFile=%s, got %s", expectedSnapFile, snapFile)
	}
}

func TestController_ListSnapshots(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	cs := NewControllerServer("test.csi", "0.1.0", clientset)

	// Test with snapshot ID
	snapID := "snap-test-456"
	resp, err := cs.ListSnapshots(context.Background(), &csi.ListSnapshotsRequest{
		SnapshotId: snapID,
	})
	if err != nil {
		t.Fatalf("ListSnapshots failed: %v", err)
	}

	if len(resp.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(resp.Entries))
	}

	if resp.Entries[0].Snapshot.SnapshotId != snapID {
		t.Errorf("expected snapshot ID %s, got %s", snapID, resp.Entries[0].Snapshot.SnapshotId)
	}

	// Test without snapshot ID (should return empty list)
	resp2, err := cs.ListSnapshots(context.Background(), &csi.ListSnapshotsRequest{})
	if err != nil {
		t.Fatalf("ListSnapshots failed: %v", err)
	}

	if len(resp2.Entries) != 0 {
		t.Errorf("expected 0 entries for empty request, got %d", len(resp2.Entries))
	}
}

func TestExtractNodeHostnameFromPV(t *testing.T) {
	// Test with node affinity
	pv := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pv",
		},
		Spec: corev1.PersistentVolumeSpec{
			NodeAffinity: &corev1.VolumeNodeAffinity{
				Required: &corev1.NodeSelector{
					NodeSelectorTerms: []corev1.NodeSelectorTerm{
						{
							MatchExpressions: []corev1.NodeSelectorRequirement{
								{
									Key:      "kubernetes.io/hostname",
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{"test-node-1"},
								},
							},
						},
					},
				},
			},
		},
	}

	nodeName := extractNodeHostnameFromPV(pv)
	if nodeName != "test-node-1" {
		t.Errorf("expected node name 'test-node-1', got '%s'", nodeName)
	}

	// Test without node affinity
	pvNoAffinity := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pv-no-affinity",
		},
		Spec: corev1.PersistentVolumeSpec{},
	}

	nodeName2 := extractNodeHostnameFromPV(pvNoAffinity)
	if nodeName2 != "" {
		t.Errorf("expected empty node name for PV without affinity, got '%s'", nodeName2)
	}
}
