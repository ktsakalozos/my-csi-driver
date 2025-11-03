package metrics

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMetricsServerIntegration(t *testing.T) {
	// Create a temporary backing directory
	tmpDir, err := os.MkdirTemp("", "metrics-integration-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test volume files
	vol1Path := filepath.Join(tmpDir, "vol-integration-1.img")
	vol2Path := filepath.Join(tmpDir, "vol-integration-2.img")

	if err := createTestFileWithData(vol1Path, 5*1024*1024); err != nil { // 5 MB
		t.Fatalf("Failed to create test file: %v", err)
	}
	if err := createTestFileWithData(vol2Path, 10*1024*1024); err != nil { // 10 MB
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create metrics server
	server := NewServer(19898) // Use a different port to avoid conflicts
	collector := NewVolumeStatsCollector("integration-test-node", tmpDir)

	if err := server.RegisterCollector(collector); err != nil {
		t.Fatalf("Failed to register collector: %v", err)
	}

	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start metrics server: %v", err)
	}
	defer server.Stop()

	// Give the server a moment to start
	time.Sleep(100 * time.Millisecond)

	// Fetch metrics from the HTTP endpoint
	resp, err := http.Get("http://localhost:19898/metrics")
	if err != nil {
		t.Fatalf("Failed to fetch metrics: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read response body: %v", err)
	}

	metrics := string(body)

	// Verify expected metrics are present
	expectedMetrics := []string{
		"rawfile_remaining_capacity",
		"rawfile_volume_used",
		"rawfile_volume_total",
		"vol-integration-1",
		"vol-integration-2",
		"integration-test-node",
	}

	for _, expected := range expectedMetrics {
		if !strings.Contains(metrics, expected) {
			t.Errorf("Expected metric output to contain '%s', but it was not found", expected)
		}
	}

	// Verify that the metrics contain the correct volume sizes
	if !strings.Contains(metrics, "rawfile_volume_total") {
		t.Error("Expected rawfile_volume_total metric")
	}
}

func TestMetricsServerStop(t *testing.T) {
	// Test that the server can be started and stopped cleanly
	server := NewServer(19899)
	
	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	if err := server.Stop(); err != nil {
		t.Errorf("Failed to stop server: %v", err)
	}

	// Try to stop again (should be idempotent)
	if err := server.Stop(); err != nil {
		t.Errorf("Second stop call failed: %v", err)
	}
}

// Helper function to create a test file with actual data (not sparse)
func createTestFileWithData(path string, size int64) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// Write zeros to ensure blocks are allocated
	buf := make([]byte, 4096)
	remaining := size
	for remaining > 0 {
		toWrite := int64(len(buf))
		if remaining < toWrite {
			toWrite = remaining
		}
		n, err := f.Write(buf[:toWrite])
		if err != nil {
			return err
		}
		remaining -= int64(n)
	}

	return f.Sync()
}

func ExampleNewServer() {
	// Create a metrics server on port 9898
	server := NewServer(9898)
	
	// Create and register a volume stats collector
	collector := NewVolumeStatsCollector("my-node", "/var/lib/my-csi-driver")
	if err := server.RegisterCollector(collector); err != nil {
		fmt.Printf("Failed to register collector: %v\n", err)
		return
	}

	// Start the metrics server
	if err := server.Start(); err != nil {
		fmt.Printf("Failed to start metrics server: %v\n", err)
		return
	}

	// Server is now running in the background
	// It can be stopped with server.Stop()
}
