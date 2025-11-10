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
