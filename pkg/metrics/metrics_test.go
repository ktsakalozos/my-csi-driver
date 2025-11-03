package metrics

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestVolumeStatsCollector(t *testing.T) {
	// Create a temporary backing directory
	tmpDir, err := os.MkdirTemp("", "metrics-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create some test volume files
	vol1Path := filepath.Join(tmpDir, "vol-test-1.img")
	vol2Path := filepath.Join(tmpDir, "vol-test-2.img")

	// Create volume files with specific sizes
	if err := createTestFile(vol1Path, 1024*1024); err != nil { // 1 MB
		t.Fatalf("Failed to create test file: %v", err)
	}
	if err := createTestFile(vol2Path, 2*1024*1024); err != nil { // 2 MB
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create collector
	collector := NewVolumeStatsCollector("test-node", tmpDir)

	// Register collector with a test registry
	registry := prometheus.NewRegistry()
	if err := registry.Register(collector); err != nil {
		t.Fatalf("Failed to register collector: %v", err)
	}

	// Collect metrics
	metricFamilies, err := registry.Gather()
	if err != nil {
		t.Fatalf("Failed to gather metrics: %v", err)
	}

	// Verify we have metrics
	if len(metricFamilies) == 0 {
		t.Error("No metrics collected")
	}

	// Check for expected metrics
	foundMetrics := make(map[string]bool)
	for _, mf := range metricFamilies {
		foundMetrics[mf.GetName()] = true
	}

	expectedMetrics := []string{
		"rawfile_remaining_capacity",
		"rawfile_volume_used",
		"rawfile_volume_total",
	}

	for _, metricName := range expectedMetrics {
		if !foundMetrics[metricName] {
			t.Errorf("Expected metric %s not found", metricName)
		}
	}

	// Verify metric count - we should have 2 volumes
	volumeTotalCount := testutil.CollectAndCount(collector, "rawfile_volume_total")
	if volumeTotalCount != 2 {
		t.Errorf("Expected 2 volume_total metrics, got %d", volumeTotalCount)
	}

	volumeUsedCount := testutil.CollectAndCount(collector, "rawfile_volume_used")
	if volumeUsedCount != 2 {
		t.Errorf("Expected 2 volume_used metrics, got %d", volumeUsedCount)
	}
}

func TestGetRemainingCapacity(t *testing.T) {
	// Create a temporary directory
	tmpDir, err := os.MkdirTemp("", "capacity-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	collector := NewVolumeStatsCollector("test-node", tmpDir)

	capacity, err := collector.getRemainingCapacity()
	if err != nil {
		t.Fatalf("Failed to get remaining capacity: %v", err)
	}

	if capacity <= 0 {
		t.Errorf("Expected positive capacity, got %d", capacity)
	}
}

func TestGetAllVolumeStats(t *testing.T) {
	// Create a temporary backing directory
	tmpDir, err := os.MkdirTemp("", "volume-stats-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test volume files
	vol1Path := filepath.Join(tmpDir, "vol-abc.img")
	vol2Path := filepath.Join(tmpDir, "vol-xyz.img")

	if err := createTestFile(vol1Path, 1024*1024); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	if err := createTestFile(vol2Path, 512*1024); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	collector := NewVolumeStatsCollector("test-node", tmpDir)

	stats, err := collector.getAllVolumeStats()
	if err != nil {
		t.Fatalf("Failed to get volume stats: %v", err)
	}

	// Check that we got stats for both volumes
	if len(stats) != 2 {
		t.Errorf("Expected 2 volumes, got %d", len(stats))
	}

	// Check vol-abc
	if stat, ok := stats["vol-abc"]; !ok {
		t.Error("Expected vol-abc in stats")
	} else {
		if stat.Total != 1024*1024 {
			t.Errorf("Expected vol-abc total to be 1048576, got %d", stat.Total)
		}
		// Sparse files may have zero blocks allocated, so used can be 0
		if stat.Used < 0 {
			t.Errorf("Expected vol-abc used to be non-negative, got %d", stat.Used)
		}
	}

	// Check vol-xyz
	if stat, ok := stats["vol-xyz"]; !ok {
		t.Error("Expected vol-xyz in stats")
	} else {
		if stat.Total != 512*1024 {
			t.Errorf("Expected vol-xyz total to be 524288, got %d", stat.Total)
		}
		// Sparse files may have zero blocks allocated, so used can be 0
		if stat.Used < 0 {
			t.Errorf("Expected vol-xyz used to be non-negative, got %d", stat.Used)
		}
	}
}

func TestGetAllVolumeStats_EmptyDirectory(t *testing.T) {
	// Create a temporary backing directory
	tmpDir, err := os.MkdirTemp("", "empty-stats-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	collector := NewVolumeStatsCollector("test-node", tmpDir)

	stats, err := collector.getAllVolumeStats()
	if err != nil {
		t.Fatalf("Failed to get volume stats: %v", err)
	}

	// Should return empty map
	if len(stats) != 0 {
		t.Errorf("Expected 0 volumes in empty directory, got %d", len(stats))
	}
}

func TestGetAllVolumeStats_NonExistentDirectory(t *testing.T) {
	collector := NewVolumeStatsCollector("test-node", "/nonexistent/directory")

	stats, err := collector.getAllVolumeStats()
	if err != nil {
		t.Fatalf("Failed to get volume stats: %v", err)
	}

	// Should return empty map for non-existent directory
	if len(stats) != 0 {
		t.Errorf("Expected 0 volumes for non-existent directory, got %d", len(stats))
	}
}

// Helper function to create a test file with a specific size
func createTestFile(path string, size int64) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	return f.Truncate(size)
}
