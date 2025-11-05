package metrics

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/prometheus/client_golang/prometheus"
	klog "k8s.io/klog/v2"
)

// VolumeStatsCollector collects metrics for CSI volumes
type VolumeStatsCollector struct {
	nodeID     string
	backingDir string

	remainingCapacity *prometheus.Desc
	volumeUsed        *prometheus.Desc
	volumeTotal       *prometheus.Desc
}

// NewVolumeStatsCollector creates a new volume stats collector
func NewVolumeStatsCollector(nodeID, backingDir string) *VolumeStatsCollector {
	return &VolumeStatsCollector{
		nodeID:     nodeID,
		backingDir: backingDir,
		remainingCapacity: prometheus.NewDesc(
			"rawfile_remaining_capacity",
			"Free capacity for new volumes on this node (excluding reserved storage).",
			[]string{"node"},
			nil,
		),
		volumeUsed: prometheus.NewDesc(
			"rawfile_volume_used",
			"Actual amount of disk used space by volume",
			[]string{"node", "volume"},
			nil,
		),
		volumeTotal: prometheus.NewDesc(
			"rawfile_volume_total",
			"Amount of disk allocated to this volume",
			[]string{"node", "volume"},
			nil,
		),
	}
}

// Describe sends the descriptors of each metric to the provided channel
func (c *VolumeStatsCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.remainingCapacity
	ch <- c.volumeUsed
	ch <- c.volumeTotal
}

// Collect fetches the stats from the backing directory and sends them to the provided channel
func (c *VolumeStatsCollector) Collect(ch chan<- prometheus.Metric) {
	// Get remaining capacity from filesystem
	capacity, err := c.getRemainingCapacity()
	if err != nil {
		klog.Errorf("Failed to get remaining capacity: %v", err)
	} else {
		ch <- prometheus.MustNewConstMetric(
			c.remainingCapacity,
			prometheus.GaugeValue,
			float64(capacity),
			c.nodeID,
		)
	}

	// Get stats for each volume
	volumeStats, err := c.getAllVolumeStats()
	if err != nil {
		klog.Errorf("Failed to get volume stats: %v", err)
		return
	}

	for volumeID, stats := range volumeStats {
		ch <- prometheus.MustNewConstMetric(
			c.volumeUsed,
			prometheus.GaugeValue,
			float64(stats.Used),
			c.nodeID,
			volumeID,
		)
		ch <- prometheus.MustNewConstMetric(
			c.volumeTotal,
			prometheus.GaugeValue,
			float64(stats.Total),
			c.nodeID,
			volumeID,
		)
	}
}

// VolumeStats represents statistics for a single volume
type VolumeStats struct {
	Used  int64
	Total int64
}

// getRemainingCapacity returns the available capacity in the backing directory
func (c *VolumeStatsCollector) getRemainingCapacity() (int64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(c.backingDir, &stat); err != nil {
		return 0, err
	}

	// Available capacity = available blocks * block size
	availableBytes := int64(stat.Bavail) * int64(stat.Bsize)
	return availableBytes, nil
}

// getAllVolumeStats returns stats for all volumes in the backing directory
func (c *VolumeStatsCollector) getAllVolumeStats() (map[string]VolumeStats, error) {
	stats := make(map[string]VolumeStats)

	// Check if backing directory exists
	if _, err := os.Stat(c.backingDir); os.IsNotExist(err) {
		return stats, nil // No volumes yet
	}

	// Walk through backing directory to find .img files
	err := filepath.Walk(c.backingDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories and non-.img files
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".img") {
			return nil
		}

		// Extract volume ID from filename (vol-xxx.img -> vol-xxx)
		volumeID := strings.TrimSuffix(info.Name(), ".img")

		// Get actual disk usage (blocks allocated)
		var stat syscall.Stat_t
		if err := syscall.Stat(path, &stat); err != nil {
			klog.Warningf("Failed to stat volume file %s: %v", path, err)
			return nil
		}

		// Used space is blocks * block size
		// Note: stat.Blocks is typically in 512-byte units on most Unix-like systems,
		// but we use stat.Blksize for better portability across platforms
		usedBytes := stat.Blocks * stat.Blksize

		stats[volumeID] = VolumeStats{
			Used:  usedBytes,
			Total: info.Size(),
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return stats, nil
}
