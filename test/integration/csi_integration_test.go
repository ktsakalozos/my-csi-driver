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
)

// findProjectRoot returns the repo root by walking up from CWD until it finds go.mod
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

// buildBinary builds cmd/driver and returns the path to the binary
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

func TestCSI_Identity_And_Controller(t *testing.T) {
	root := findProjectRoot(t)
	bin := buildBinary(t, root)

	// Use a test socket under tmp
	sockDir := filepath.Join(os.TempDir(), "csi-test")
	_ = os.MkdirAll(sockDir, 0o755)
	sock := filepath.Join(sockDir, "csi.sock")
	endpoint := fmt.Sprintf("unix://%s", sock)

	// Start the driver in background
	driverCmd := exec.Command(bin,
		"-endpoint", endpoint,
		"-nodeid", "itest-node",
		"-drivername", "itest-driver",
		"-working-mount-dir", os.TempDir(),
	)
	driverCmd.Env = append(os.Environ(),
		"CSI_BACKING_DIR="+filepath.Join(os.TempDir(), "my-csi-driver"),
	)
	driverCmd.Stdout = os.Stdout
	driverCmd.Stderr = os.Stderr
	if err := driverCmd.Start(); err != nil {
		t.Fatalf("failed to start driver: %v", err)
	}
	defer func() {
		_ = driverCmd.Process.Kill()
		_, _ = driverCmd.Process.Wait()
	}()

	// Wait for socket to be created (basic readiness)
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
	// Small extra delay to allow gRPC to fully accept connections after socket appears
	time.Sleep(3 * time.Second)

	// Require csc (csi-cli) in PATH
	if _, err := exec.LookPath("csc"); err != nil {
		t.Skip("csc (csi-cli) not found in PATH; install it with 'go install github.com/rexray/gocsi/csc@latest', skipping integration test")
	}

	// csc identity plugin-info (with retry until the server is ready)
	{
		deadline := time.Now().Add(5 * time.Second)
		var lastOut []byte
		var lastErr error
		for time.Now().Before(deadline) {
			cmd := exec.Command("csc", "identity", "plugin-info", "--endpoint", endpoint)
			out, err := cmd.CombinedOutput()
			if err == nil {
				lastOut, lastErr = out, nil
				break
			}
			lastOut, lastErr = out, err
			time.Sleep(100 * time.Millisecond)
		}
		if lastErr != nil {
			t.Fatalf("csc plugin-info failed after retries: %v\n%s", lastErr, string(lastOut))
		}
		t.Logf("csc identity plugin-info output:\n%s", string(lastOut))
	}

	// csc controller get-capabilities
	{
		cmd := exec.Command("csc", "controller", "get-capabilities", "--endpoint", endpoint)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("csc get-capabilities failed: %v\n%s", err, string(out))
		}
		t.Logf("csc controller get-capabilities output:\n%s", string(out))
	}

	// Set the same backing dir as the driver and ensure it exists
	backingDir := filepath.Join(os.TempDir(), "my-csi-driver")
	_ = os.MkdirAll(backingDir, 0o755)

	// Helper to list current .img files
	listImgs := func(dir string) map[string]os.FileInfo {
		files := map[string]os.FileInfo{}
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			name := e.Name()
			if filepath.Ext(name) == ".img" {
				if fi, err := e.Info(); err == nil {
					files[name] = fi
				}
			}
		}
		return files
	}

	// Create a volume via csc and verify the backing file appears
	before := listImgs(backingDir)
	volName := fmt.Sprintf("itest-%d", time.Now().UnixNano())
	var createOut string
	{
		// ACCESS_MODE,ACCESS_TYPE[,FS_TYPE,MOUNT_FLAGS]
		// Use SINGLE_NODE_WRITER + mount with ext4 fs
		cmd := exec.Command("csc", "controller", "create-volume",
			"--endpoint", endpoint,
			"--cap", "SINGLE_NODE_WRITER,mount,ext4",
			"--req-bytes", "1048576",
			volName,
		)
		out, err := cmd.CombinedOutput()
		createOut = string(out)
		t.Logf("csc controller create-volume output:\n%s", createOut)
		if err != nil {
			t.Fatalf("csc create-volume failed: %v\n%s", err, string(out))
		}
	}

	// Determine created volume by diffing .img files
	var createdVolID string
	{
		deadline := time.Now().Add(2 * time.Second)
		for createdVolID == "" && time.Now().Before(deadline) {
			after := listImgs(backingDir)
			for name := range after {
				if _, ok := before[name]; !ok {
					// name is e.g. vol-<uuid>.img
					base := name
					if filepath.Ext(base) == ".img" {
						base = base[:len(base)-len(".img")]
					}
					createdVolID = base
					break
				}
			}
			if createdVolID == "" {
				time.Sleep(50 * time.Millisecond)
			}
		}
		if createdVolID == "" {
			t.Fatalf("failed to detect created volume backing file in %s", backingDir)
		}
		t.Logf("Detected created volume ID: %s", createdVolID)
	}

	// Verify details from create-volume output and filesystem
	{
		// Try to extract capacity and backingFile from createOut, falling back to expected path if needed
		var backingFileFromOut string
		var capFromOut int64 = 1048576
		if i := len(createOut); i > 0 {
			// naive parse for backingFile="..."
			if idx := indexOf(createOut, "backingFile\"=\""); idx >= 0 {
				s := createOut[idx+len("backingFile\"=\""):]
				if j := indexOf(s, "\""); j >= 0 {
					backingFileFromOut = s[:j]
				}
			}
		}
		expectedBacking := filepath.Join(backingDir, createdVolID+".img")
		if backingFileFromOut == "" {
			backingFileFromOut = expectedBacking
		}
		fi, err := os.Stat(backingFileFromOut)
		if err != nil {
			t.Fatalf("backing file not found: %s: %v", backingFileFromOut, err)
		}
		if fi.Size() != capFromOut {
			t.Fatalf("unexpected backing file size: got %d, want %d", fi.Size(), capFromOut)
		}
		// Validate volume capabilities as a proxy check that the volume id is accepted by the controller
		vcmd := exec.Command("csc", "controller", "validate-volume-capabilities",
			"--endpoint", endpoint,
			"--cap", "SINGLE_NODE_WRITER,mount,ext4",
			createdVolID,
		)
		vOut, vErr := vcmd.CombinedOutput()
		t.Logf("csc controller validate-volume-capabilities output:\n%s", string(vOut))
		if vErr != nil {
			t.Fatalf("csc validate-volume-capabilities failed: %v\n%s", vErr, string(vOut))
		}
	}

	// Delete the volume via csc and verify the backing file is removed
	{
		cmd := exec.Command("csc", "controller", "delete-volume",
			"--endpoint", endpoint,
			createdVolID,
		)
		out, err := cmd.CombinedOutput()
		t.Logf("csc controller delete-volume output:\n%s", string(out))
		if err != nil {
			t.Fatalf("csc delete-volume failed: %v\n%s", err, string(out))
		}
	}

	// Ensure file is gone
	{
		if _, err := os.Stat(filepath.Join(backingDir, createdVolID+".img")); err == nil {
			t.Fatalf("backing file still exists after deletion: %s", createdVolID+".img")
		}
	}
}

// indexOf is a small helper to avoid strings import churn above
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
