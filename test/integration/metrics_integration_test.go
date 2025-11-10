//go:build integration
// +build integration

package integration

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestCSI_Metrics tests that the metrics endpoint works correctly during CSI operations
func TestCSI_Metrics(t *testing.T) {
	root := findProjectRoot(t)
	bin := buildBinary(t, root)

	sockDir := filepath.Join(os.TempDir(), "csi-test-metrics")
	if err := os.MkdirAll(sockDir, 0o755); err != nil {
		t.Fatalf("failed to create socket directory %s: %v", sockDir, err)
	}
	sock := filepath.Join(sockDir, "csi.sock")
	endpoint := fmt.Sprintf("unix://%s", sock)

	backingDir := filepath.Join(os.TempDir(), "my-csi-driver-metrics")
	_ = os.MkdirAll(backingDir, 0o755)
	defer os.RemoveAll(backingDir)

	// Start driver with metrics enabled on a custom port
	metricsPort := 19898
	driverCmd := exec.Command(bin,
		"-endpoint", endpoint,
		"-drivername", "itest-driver",
		"-working-mount-dir", os.TempDir(),
		"-mode", "controller",
		"-metrics-port", fmt.Sprintf("%d", metricsPort),
		"-nodeid", "metrics-test-node",
		"-standalone",
	)
	driverCmd.Env = append(os.Environ(), "CSI_BACKING_DIR="+backingDir)
	driverCmd.Stdout = os.Stdout
	driverCmd.Stderr = os.Stderr
	if err := driverCmd.Start(); err != nil {
		t.Fatalf("start driver: %v", err)
	}
	defer func() { _ = driverCmd.Process.Kill(); _, _ = driverCmd.Process.Wait() }()

	// Wait for socket to be ready
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

	// Test 1: Verify metrics endpoint is accessible
	metricsURL := fmt.Sprintf("http://localhost:%d/metrics", metricsPort)
	resp, err := http.Get(metricsURL)
	if err != nil {
		t.Fatalf("Failed to fetch metrics: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read metrics response: %v", err)
	}

	metrics := string(body)

	// Test 2: Verify basic metrics structure is present
	// Note: volume_used and volume_total only appear when volumes exist
	requiredMetrics := []string{
		"rawfile_remaining_capacity",
		"metrics-test-node",
	}

	for _, expected := range requiredMetrics {
		if !strings.Contains(metrics, expected) {
			t.Errorf("Expected metric output to contain '%s', but it was not found", expected)
		}
	}

	// Test 3: Verify remaining capacity metric has a value
	if !strings.Contains(metrics, "rawfile_remaining_capacity{node=\"metrics-test-node\"}") {
		t.Error("Expected rawfile_remaining_capacity metric with node label")
	}

	// Test 4: Verify HELP and TYPE annotations are present
	if !strings.Contains(metrics, "# HELP rawfile_remaining_capacity") {
		t.Error("Expected HELP annotation for rawfile_remaining_capacity")
	}
	if !strings.Contains(metrics, "# TYPE rawfile_remaining_capacity gauge") {
		t.Error("Expected TYPE annotation for rawfile_remaining_capacity")
	}

	if _, err := exec.LookPath("csc"); err != nil {
		t.Logf("✓ Metrics endpoint is accessible and working")
		t.Skip("csc (csi-cli) not found; skipping volume creation/deletion test")
	}

	// Note: With the new topology-aware architecture, the controller no longer creates
	// backing files. Files are only created on nodes during NodePublishVolume.
	// This test runs in controller-only mode, so we skip the volume file checks.
	t.Logf("✓ Metrics endpoint is accessible and working")
	t.Skip("Controller-only mode doesn't create backing files in topology-aware architecture")
}


// TestCSI_MetricsBasic tests the metrics endpoint without requiring CSC
func TestCSI_MetricsBasic(t *testing.T) {
	root := findProjectRoot(t)
	bin := buildBinary(t, root)

	sockDir := filepath.Join(os.TempDir(), "csi-test-metrics-basic")
	if err := os.MkdirAll(sockDir, 0o755); err != nil {
		t.Fatalf("failed to create socket directory %s: %v", sockDir, err)
	}
	sock := filepath.Join(sockDir, "csi.sock")
	endpoint := fmt.Sprintf("unix://%s", sock)

	backingDir := filepath.Join(os.TempDir(), "my-csi-driver-metrics-basic")
	_ = os.MkdirAll(backingDir, 0o755)
	defer os.RemoveAll(backingDir)

	// Create a test volume file manually to simulate an existing volume
	testVolID := "vol-test-12345"
	testVolPath := filepath.Join(backingDir, testVolID+".img")
	if err := os.WriteFile(testVolPath, make([]byte, 1024*1024), 0644); err != nil {
		t.Fatalf("failed to create test volume: %v", err)
	}

	// Start driver with metrics enabled
	metricsPort := 19897
	driverCmd := exec.Command(bin,
		"-endpoint", endpoint,
		"-drivername", "itest-driver",
		"-working-mount-dir", os.TempDir(),
		"-mode", "controller",
		"-metrics-port", fmt.Sprintf("%d", metricsPort),
		"-nodeid", "basic-test-node",
		"-standalone",
	)
	driverCmd.Env = append(os.Environ(), "CSI_BACKING_DIR="+backingDir)
	driverCmd.Stdout = os.Stdout
	driverCmd.Stderr = os.Stderr
	if err := driverCmd.Start(); err != nil {
		t.Fatalf("start driver: %v", err)
	}
	defer func() { _ = driverCmd.Process.Kill(); _, _ = driverCmd.Process.Wait() }()

	// Wait for driver to start
	time.Sleep(1 * time.Second)

	// Fetch metrics
	metricsURL := fmt.Sprintf("http://localhost:%d/metrics", metricsPort)
	resp, err := http.Get(metricsURL)
	if err != nil {
		t.Fatalf("Failed to fetch metrics: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read metrics response: %v", err)
	}

	metrics := string(body)

	// Verify metrics are present
	checks := []struct {
		name  string
		match string
	}{
		{"remaining capacity metric", "rawfile_remaining_capacity{node=\"basic-test-node\"}"},
		{"volume total metric", fmt.Sprintf("rawfile_volume_total{node=\"basic-test-node\",volume=\"%s\"}", testVolID)},
		{"volume used metric", fmt.Sprintf("rawfile_volume_used{node=\"basic-test-node\",volume=\"%s\"}", testVolID)},
		{"help annotation", "# HELP rawfile_remaining_capacity"},
		{"type annotation", "# TYPE rawfile_remaining_capacity gauge"},
	}

	for _, check := range checks {
		if !strings.Contains(metrics, check.match) {
			t.Errorf("Expected to find %s: %s", check.name, check.match)
		}
	}

	// Verify the volume_total metric reports the correct size (1 MB = 1048576 bytes)
	expectedVolumeTotal := fmt.Sprintf("rawfile_volume_total{node=\"basic-test-node\",volume=\"%s\"}", testVolID)
	volumeTotalValue, err := parseMetricValue(metrics, expectedVolumeTotal)
	if err != nil {
		t.Errorf("Failed to parse volume_total metric: %v", err)
	} else if volumeTotalValue != 1048576 {
		t.Errorf("Expected volume_total to be 1048576 bytes (1 MB), got %f", volumeTotalValue)
	} else {
		t.Logf("✓ Volume total metric correctly reports %f bytes (1 MB)", volumeTotalValue)
	}

	t.Logf("✓ All basic metrics checks passed")
}

// parseMetricValue extracts the numeric value from a Prometheus metric line
// Example input: rawfile_volume_total{node="test",volume="vol-123"} 1048576
// Returns: 1048576
func parseMetricValue(metrics, metricLine string) (float64, error) {
	lines := strings.Split(metrics, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, metricLine) {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				return strconv.ParseFloat(parts[1], 64)
			}
		}
	}
	return 0, fmt.Errorf("metric line not found: %s", metricLine)
}
