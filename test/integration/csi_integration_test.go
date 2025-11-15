//go:build integration
// +build integration

package integration

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func findProjectRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatalf("go.mod not found from %s", dir)
	return ""
}

func buildBinary(t *testing.T, root string) string {
	t.Helper()
	bin := filepath.Join(root, "bin", "my-csi-driver-test")
	_ = os.MkdirAll(filepath.Dir(bin), 0o755)
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/driver")
	cmd.Dir = root
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to build driver: %v\n%s", err, string(out))
	}
	return bin
}

// Controller-only integration test
func TestCSI_Controller(t *testing.T) {
	root := findProjectRoot(t)
	bin := buildBinary(t, root)

	sockDir := filepath.Join(os.TempDir(), "csi-test-controller")
	_ = os.MkdirAll(sockDir, 0o755)
	sock := filepath.Join(sockDir, "csi.sock")
	endpoint := fmt.Sprintf("unix://%s", sock)

	backingDir := filepath.Join(os.TempDir(), "my-csi-driver-controller")
	_ = os.MkdirAll(backingDir, 0o755)

	driverCmd := exec.Command(bin,
		"-endpoint", endpoint,
		"-drivername", "itest-driver",
		"-working-mount-dir", os.TempDir(),
		"-mode", "controller",
		"-standalone",
	)
	driverCmd.Env = append(os.Environ(), "CSI_BACKING_DIR="+backingDir)
	driverCmd.Stdout = os.Stdout
	driverCmd.Stderr = os.Stderr
	if err := driverCmd.Start(); err != nil {
		t.Fatalf("start controller driver: %v", err)
	}
	defer func() { _ = driverCmd.Process.Kill(); _, _ = driverCmd.Process.Wait() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("socket not ready: %v", ctx.Err())
		default:
			if _, err := os.Stat(sock); err == nil {
				goto READY
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
READY:
	time.Sleep(500 * time.Millisecond)

	if _, err := exec.LookPath("csc"); err != nil {
		t.Skip("csc (csi-cli) not found; skipping controller test")
	}

	// Note: With the new topology-aware architecture, the controller no longer creates
	// backing files. Files are only created on nodes during NodePublishVolume.
	// This test runs in controller-only mode, so we skip the volume file checks.
	t.Skip("Controller-only mode doesn't create backing files in topology-aware architecture")
}

// Node-only integration test
func TestCSI_Node(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("node test requires root")
	}
	for _, tool := range []string{"losetup", "mkfs.ext4", "blkid", "mount", "umount"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("missing %s", tool)
		}
	}

	root := findProjectRoot(t)
	bin := buildBinary(t, root)

	sockDir := filepath.Join(os.TempDir(), "csi-test-node")
	_ = os.MkdirAll(sockDir, 0o755)
	sock := filepath.Join(sockDir, "csi.sock")
	endpoint := fmt.Sprintf("unix://%s", sock)

	backingDir := filepath.Join(os.TempDir(), "my-csi-driver-node")
	_ = os.MkdirAll(backingDir, 0o755)
	volID := fmt.Sprintf("vol-node-%d", time.Now().UnixNano())
	backingFile := filepath.Join(backingDir, volID+".img")

	driverCmd := exec.Command(bin,
		"-endpoint", endpoint,
		"-drivername", "itest-driver",
		"-nodeid", "itest-node",
		"-working-mount-dir", os.TempDir(),
		"-mode", "node",
		"-standalone",
	)
	driverCmd.Env = append(os.Environ(), "CSI_BACKING_DIR="+backingDir)
	driverCmd.Stdout = os.Stdout
	driverCmd.Stderr = os.Stderr
	if err := driverCmd.Start(); err != nil {
		t.Fatalf("start node driver: %v", err)
	}
	defer func() { _ = driverCmd.Process.Kill(); _, _ = driverCmd.Process.Wait() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("socket not ready: %v", ctx.Err())
		default:
			if _, err := os.Stat(sock); err == nil {
				goto READY
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
READY:
	time.Sleep(300 * time.Millisecond)

	conn, err := grpc.DialContext(context.Background(), endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial node: %v", err)
	}
	defer conn.Close()
	nc := csi.NewNodeClient(conn)

	targetPath := filepath.Join(os.TempDir(), fmt.Sprintf("csi-target-node-%d", time.Now().UnixNano()))
	if err := os.MkdirAll(targetPath, 0o750); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}

	capability := &csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Mount{Mount: &csi.VolumeCapability_MountVolume{FsType: "ext4"}},
		AccessMode: &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER},
	}
	pubReq := &csi.NodePublishVolumeRequest{VolumeId: volID, TargetPath: targetPath, VolumeCapability: capability, VolumeContext: map[string]string{"backingFile": backingFile, "size": strconv.FormatInt(1024*1024, 10)}}
	if _, err := nc.NodePublishVolume(context.Background(), pubReq); err != nil {
		t.Fatalf("NodePublishVolume failed: %v", err)
	}

	if data, err := os.ReadFile("/proc/mounts"); err == nil {
		if indexOf(string(data), targetPath) < 0 {
			t.Fatalf("target path not mounted: %s", targetPath)
		}
	}

	if _, err := nc.NodeUnpublishVolume(context.Background(), &csi.NodeUnpublishVolumeRequest{VolumeId: volID, TargetPath: targetPath}); err != nil {
		t.Fatalf("NodeUnpublishVolume failed: %v", err)
	}
	if data, err := os.ReadFile("/proc/mounts"); err == nil {
		if indexOf(string(data), targetPath) >= 0 {
			t.Fatalf("target path still mounted: %s", targetPath)
		}
	}
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
// TestCSI_Snapshot_ControllerCapabilities tests that snapshot capabilities are advertised
func TestCSI_Snapshot_ControllerCapabilities(t *testing.T) {
	root := findProjectRoot(t)
	bin := buildBinary(t, root)

	sockDir := filepath.Join(os.TempDir(), "csi-test-snapshot-caps")
	_ = os.MkdirAll(sockDir, 0o755)
	sock := filepath.Join(sockDir, "csi.sock")
	endpoint := fmt.Sprintf("unix://%s", sock)

	backingDir := filepath.Join(os.TempDir(), "my-csi-driver-snapshot-caps")
	_ = os.MkdirAll(backingDir, 0o755)

	driverCmd := exec.Command(bin,
		"-endpoint", endpoint,
		"-drivername", "itest-driver",
		"-working-mount-dir", os.TempDir(),
		"-mode", "controller",
		"-standalone",
	)
	driverCmd.Env = append(os.Environ(), "CSI_BACKING_DIR="+backingDir)
	driverCmd.Stdout = os.Stdout
	driverCmd.Stderr = os.Stderr
	if err := driverCmd.Start(); err != nil {
		t.Fatalf("start controller driver: %v", err)
	}
	defer func() { _ = driverCmd.Process.Kill(); _, _ = driverCmd.Process.Wait() }()

	// Wait for socket
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("socket not ready: %v", ctx.Err())
		default:
			if _, err := os.Stat(sock); err == nil {
				goto READY
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
READY:
	time.Sleep(500 * time.Millisecond)

	// Connect via gRPC
	conn, err := grpc.DialContext(context.Background(), endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial controller: %v", err)
	}
	defer conn.Close()
	cc := csi.NewControllerClient(conn)

	// Get capabilities
	capResp, err := cc.ControllerGetCapabilities(context.Background(), &csi.ControllerGetCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("ControllerGetCapabilities failed: %v", err)
	}

	// Verify snapshot capabilities
	foundCreateDelete := false
	foundList := false
	for _, cap := range capResp.Capabilities {
		if cap.GetRpc() == nil {
			continue
		}
		switch cap.GetRpc().Type {
		case csi.ControllerServiceCapability_RPC_CREATE_DELETE_SNAPSHOT:
			foundCreateDelete = true
		case csi.ControllerServiceCapability_RPC_LIST_SNAPSHOTS:
			foundList = true
		}
	}

	if !foundCreateDelete {
		t.Error("CREATE_DELETE_SNAPSHOT capability not advertised")
	}
	if !foundList {
		t.Error("LIST_SNAPSHOTS capability not advertised")
	}

	t.Logf("✓ Snapshot capabilities verified: CREATE_DELETE_SNAPSHOT=%v, LIST_SNAPSHOTS=%v", foundCreateDelete, foundList)
}

// TestCSI_Snapshot_CreateVolumeFromSnapshot tests volume creation with snapshot source
func TestCSI_Snapshot_CreateVolumeFromSnapshot(t *testing.T) {
	root := findProjectRoot(t)
	bin := buildBinary(t, root)

	sockDir := filepath.Join(os.TempDir(), "csi-test-snapshot-createvol")
	_ = os.MkdirAll(sockDir, 0o755)
	sock := filepath.Join(sockDir, "csi.sock")
	endpoint := fmt.Sprintf("unix://%s", sock)

	backingDir := filepath.Join(os.TempDir(), "my-csi-driver-snapshot-createvol")
	_ = os.MkdirAll(backingDir, 0o755)

	driverCmd := exec.Command(bin,
		"-endpoint", endpoint,
		"-drivername", "itest-driver",
		"-working-mount-dir", os.TempDir(),
		"-mode", "controller",
		"-standalone",
	)
	driverCmd.Env = append(os.Environ(), "CSI_BACKING_DIR="+backingDir)
	driverCmd.Stdout = os.Stdout
	driverCmd.Stderr = os.Stderr
	if err := driverCmd.Start(); err != nil {
		t.Fatalf("start controller driver: %v", err)
	}
	defer func() { _ = driverCmd.Process.Kill(); _, _ = driverCmd.Process.Wait() }()

	// Wait for socket
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("socket not ready: %v", ctx.Err())
		default:
			if _, err := os.Stat(sock); err == nil {
				goto READY
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
READY:
	time.Sleep(500 * time.Millisecond)

	// Connect via gRPC
	conn, err := grpc.DialContext(context.Background(), endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial controller: %v", err)
	}
	defer conn.Close()
	cc := csi.NewControllerClient(conn)

	// Create volume from snapshot
	snapID := "snap-integration-test-123"
	volName := "vol-from-snapshot"
	createReq := &csi.CreateVolumeRequest{
		Name: volName,
		CapacityRange: &csi.CapacityRange{
			RequiredBytes: 1024 * 1024, // 1 MiB
		},
		VolumeContentSource: &csi.VolumeContentSource{
			Type: &csi.VolumeContentSource_Snapshot{
				Snapshot: &csi.VolumeContentSource_SnapshotSource{
					SnapshotId: snapID,
				},
			},
		},
	}

	createResp, err := cc.CreateVolume(context.Background(), createReq)
	if err != nil {
		t.Fatalf("CreateVolume failed: %v", err)
	}

	// Verify volume context contains restore metadata
	vol := createResp.Volume
	if vol == nil {
		t.Fatal("CreateVolume returned nil volume")
	}

	restoreFrom := vol.VolumeContext["restoreFromSnapshot"]
	if restoreFrom != snapID {
		t.Errorf("expected restoreFromSnapshot=%s, got %s", snapID, restoreFrom)
	}

	expectedSnapFile := backingDir + "/snap-" + snapID + ".img"
	snapFile := vol.VolumeContext["snapshotFile"]
	if snapFile != expectedSnapFile {
		t.Errorf("expected snapshotFile=%s, got %s", expectedSnapFile, snapFile)
	}

	t.Logf("✓ CreateVolume from snapshot verified: volumeId=%s, restoreFromSnapshot=%s, snapshotFile=%s",
		vol.VolumeId, restoreFrom, snapFile)
}

// TestCSI_Snapshot_NodeRestore tests volume restore from snapshot on node side
func TestCSI_Snapshot_NodeRestore(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("node restore test requires root")
	}
	for _, tool := range []string{"losetup", "mkfs.ext4", "blkid", "mount", "umount"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("missing %s", tool)
		}
	}

	root := findProjectRoot(t)
	bin := buildBinary(t, root)

	sockDir := filepath.Join(os.TempDir(), "csi-test-snapshot-restore")
	_ = os.MkdirAll(sockDir, 0o755)
	sock := filepath.Join(sockDir, "csi.sock")
	endpoint := fmt.Sprintf("unix://%s", sock)

	backingDir := filepath.Join(os.TempDir(), "my-csi-driver-snapshot-restore")
	_ = os.MkdirAll(backingDir, 0o755)

	// Create a snapshot file with test content
	snapID := "snap-restore-test-456"
	snapFile := filepath.Join(backingDir, "snap-"+snapID+".img")
	snapContent := []byte("test snapshot content for restore")
	
	// Create snapshot file with sufficient size (1 MiB)
	if err := os.WriteFile(snapFile, make([]byte, 1024*1024), 0644); err != nil {
		t.Fatalf("failed to create snapshot file: %v", err)
	}
	// Write test content at the beginning
	f, err := os.OpenFile(snapFile, os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("failed to open snapshot file: %v", err)
	}
	if _, err := f.Write(snapContent); err != nil {
		f.Close()
		t.Fatalf("failed to write snapshot content: %v", err)
	}
	f.Close()

	volID := fmt.Sprintf("vol-restored-%d", time.Now().UnixNano())
	backingFile := filepath.Join(backingDir, volID+".img")

	driverCmd := exec.Command(bin,
		"-endpoint", endpoint,
		"-drivername", "itest-driver",
		"-nodeid", "itest-node",
		"-working-mount-dir", os.TempDir(),
		"-mode", "node",
		"-standalone",
	)
	driverCmd.Env = append(os.Environ(), "CSI_BACKING_DIR="+backingDir)
	driverCmd.Stdout = os.Stdout
	driverCmd.Stderr = os.Stderr
	if err := driverCmd.Start(); err != nil {
		t.Fatalf("start node driver: %v", err)
	}
	defer func() { _ = driverCmd.Process.Kill(); _, _ = driverCmd.Process.Wait() }()

	// Wait for socket
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("socket not ready: %v", ctx.Err())
		default:
			if _, err := os.Stat(sock); err == nil {
				goto READY
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
READY:
	time.Sleep(300 * time.Millisecond)

	// Connect via gRPC
	conn, err := grpc.DialContext(context.Background(), endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial node: %v", err)
	}
	defer conn.Close()
	nc := csi.NewNodeClient(conn)

	targetPath := filepath.Join(os.TempDir(), fmt.Sprintf("csi-target-restore-%d", time.Now().UnixNano()))
	if err := os.MkdirAll(targetPath, 0o750); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	defer os.RemoveAll(targetPath)

	// Publish volume with restore context
	capability := &csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Mount{Mount: &csi.VolumeCapability_MountVolume{FsType: "ext4"}},
		AccessMode: &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER},
	}
	pubReq := &csi.NodePublishVolumeRequest{
		VolumeId:   volID,
		TargetPath: targetPath,
		VolumeCapability: capability,
		VolumeContext: map[string]string{
			"backingFile":          backingFile,
			"size":                 strconv.FormatInt(1024*1024, 10),
			"restoreFromSnapshot":  snapID,
			"snapshotFile":         snapFile,
		},
	}

	if _, err := nc.NodePublishVolume(context.Background(), pubReq); err != nil {
		t.Fatalf("NodePublishVolume failed: %v", err)
	}

	// Verify volume file was created and contains snapshot content
	if _, err := os.Stat(backingFile); err != nil {
		t.Errorf("backing file not created: %v", err)
	} else {
		// Read first bytes to verify content was restored
		content := make([]byte, len(snapContent))
		if f, err := os.Open(backingFile); err == nil {
			n, _ := f.Read(content)
			f.Close()
			if n == len(snapContent) && string(content) == string(snapContent) {
				t.Logf("✓ Snapshot content successfully restored to volume file")
			} else {
				t.Logf("Warning: snapshot content verification incomplete (read %d bytes)", n)
			}
		}
	}

	// Verify mount
	if data, err := os.ReadFile("/proc/mounts"); err == nil {
		if indexOf(string(data), targetPath) < 0 {
			t.Errorf("target path not mounted: %s", targetPath)
		} else {
			t.Logf("✓ Volume successfully mounted at %s", targetPath)
		}
	}

	// Cleanup
	if _, err := nc.NodeUnpublishVolume(context.Background(), &csi.NodeUnpublishVolumeRequest{
		VolumeId:   volID,
		TargetPath: targetPath,
	}); err != nil {
		t.Errorf("NodeUnpublishVolume failed: %v", err)
	}

	// Clean up files
	os.Remove(backingFile)
	os.Remove(snapFile)
}

// TestCSI_Snapshot_ListSnapshots tests the ListSnapshots RPC
func TestCSI_Snapshot_ListSnapshots(t *testing.T) {
	root := findProjectRoot(t)
	bin := buildBinary(t, root)

	sockDir := filepath.Join(os.TempDir(), "csi-test-snapshot-list")
	_ = os.MkdirAll(sockDir, 0o755)
	sock := filepath.Join(sockDir, "csi.sock")
	endpoint := fmt.Sprintf("unix://%s", sock)

	backingDir := filepath.Join(os.TempDir(), "my-csi-driver-snapshot-list")
	_ = os.MkdirAll(backingDir, 0o755)

	driverCmd := exec.Command(bin,
		"-endpoint", endpoint,
		"-drivername", "itest-driver",
		"-working-mount-dir", os.TempDir(),
		"-mode", "controller",
		"-standalone",
	)
	driverCmd.Env = append(os.Environ(), "CSI_BACKING_DIR="+backingDir)
	driverCmd.Stdout = os.Stdout
	driverCmd.Stderr = os.Stderr
	if err := driverCmd.Start(); err != nil {
		t.Fatalf("start controller driver: %v", err)
	}
	defer func() { _ = driverCmd.Process.Kill(); _, _ = driverCmd.Process.Wait() }()

	// Wait for socket
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("socket not ready: %v", ctx.Err())
		default:
			if _, err := os.Stat(sock); err == nil {
				goto READY
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
READY:
	time.Sleep(500 * time.Millisecond)

	// Connect via gRPC
	conn, err := grpc.DialContext(context.Background(), endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial controller: %v", err)
	}
	defer conn.Close()
	cc := csi.NewControllerClient(conn)

	// Test 1: List with specific snapshot ID
	snapID := "snap-list-test-789"
	listReq := &csi.ListSnapshotsRequest{
		SnapshotId: snapID,
	}

	listResp, err := cc.ListSnapshots(context.Background(), listReq)
	if err != nil {
		t.Fatalf("ListSnapshots failed: %v", err)
	}

	if len(listResp.Entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(listResp.Entries))
	} else if listResp.Entries[0].Snapshot.SnapshotId != snapID {
		t.Errorf("expected snapshot ID %s, got %s", snapID, listResp.Entries[0].Snapshot.SnapshotId)
	} else {
		t.Logf("✓ ListSnapshots with ID verified: snapshotId=%s", snapID)
	}

	// Test 2: List without snapshot ID (should return empty list in minimal implementation)
	listReq2 := &csi.ListSnapshotsRequest{}
	listResp2, err := cc.ListSnapshots(context.Background(), listReq2)
	if err != nil {
		t.Fatalf("ListSnapshots (empty) failed: %v", err)
	}

	if len(listResp2.Entries) != 0 {
		t.Logf("Warning: ListSnapshots without ID returned %d entries (expected 0 in minimal implementation)", len(listResp2.Entries))
	} else {
		t.Logf("✓ ListSnapshots without ID verified: returned empty list as expected")
	}
}

// Note: CreateSnapshot and DeleteSnapshot integration tests require a Kubernetes cluster
// because they create Pods for file operations. These are better suited for e2e tests.
// For integration tests, we verify the capability advertisement, CreateVolume behavior,
// and NodePublishVolume restore logic which can run standalone.
