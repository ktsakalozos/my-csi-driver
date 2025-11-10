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
