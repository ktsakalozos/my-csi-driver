//go:build integration
// +build integration

package integration

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

	// Identity with retry
	{
		deadline := time.Now().Add(5 * time.Second)
		var out []byte
		var err error
		for time.Now().Before(deadline) {
			cmd := exec.Command("csc", "identity", "plugin-info", "--endpoint", endpoint)
			out, err = cmd.CombinedOutput()
			if err == nil {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if err != nil {
			t.Fatalf("plugin-info failed: %v\n%s", err, string(out))
		}
	}

	// Controller capabilities
	{
		cmd := exec.Command("csc", "controller", "get-capabilities", "--endpoint", endpoint)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("get-capabilities failed: %v\n%s", err, string(out))
		}
	}

	listImgs := func(dir string) map[string]struct{} {
		m := map[string]struct{}{}
		ents, _ := os.ReadDir(dir)
		for _, e := range ents {
			if filepath.Ext(e.Name()) == ".img" {
				m[e.Name()] = struct{}{}
			}
		}
		return m
	}
	before := listImgs(backingDir)

	volName := fmt.Sprintf("itest-%d", time.Now().UnixNano())
	cmdCreate := exec.Command("csc", "controller", "create-volume", "--endpoint", endpoint, "--cap", "SINGLE_NODE_WRITER,mount,ext4", "--req-bytes", "1048576", volName)
	createOut, err := cmdCreate.CombinedOutput()
	if err != nil {
		t.Fatalf("create-volume failed: %v\n%s", err, string(createOut))
	}

	var volID string
	deadline := time.Now().Add(2 * time.Second)
	for volID == "" && time.Now().Before(deadline) {
		after := listImgs(backingDir)
		for name := range after {
			if _, ok := before[name]; !ok {
				volID = name[:len(name)-len(".img")]
				break
			}
		}
		if volID == "" {
			time.Sleep(50 * time.Millisecond)
		}
	}
	if volID == "" {
		t.Fatalf("created volume file not found")
	}
	backingFile := filepath.Join(backingDir, volID+".img")
	fi, statErr := os.Stat(backingFile)
	if statErr != nil {
		t.Fatalf("backing file missing: %v", statErr)
	}
	if fi.Size() != 1048576 {
		t.Fatalf("unexpected size %d", fi.Size())
	}

	cmdVal := exec.Command("csc", "controller", "validate-volume-capabilities", "--endpoint", endpoint, "--cap", "SINGLE_NODE_WRITER,mount,ext4", volID)
	valOut, err := cmdVal.CombinedOutput()
	if err != nil {
		t.Fatalf("validate failed: %v\n%s", err, string(valOut))
	}

	cmdDel := exec.Command("csc", "controller", "delete-volume", "--endpoint", endpoint, volID)
	delOut, err := cmdDel.CombinedOutput()
	if err != nil {
		t.Fatalf("delete-volume failed: %v\n%s", err, string(delOut))
	}
	if _, err := os.Stat(backingFile); err == nil {
		t.Fatalf("backing file still exists")
	}
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
	f, err := os.Create(backingFile)
	if err != nil {
		t.Fatalf("create backing file: %v", err)
	}
	if err := f.Truncate(1 << 20); err != nil {
		t.Fatalf("truncate backing file: %v", err)
	}
	f.Close()

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
	pubReq := &csi.NodePublishVolumeRequest{VolumeId: volID, TargetPath: targetPath, VolumeCapability: capability, VolumeContext: map[string]string{"backingFile": backingFile}}
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
