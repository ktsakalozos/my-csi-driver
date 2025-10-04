package main

import (
	"flag"
	"os"

	"github.com/ktsakalozos/my-csi-driver/pkg/rawfile"
	klog "k8s.io/klog/v2"
)

var (
	endpoint        = flag.String("endpoint", "unix:///var/lib/kubelet/plugins/my-csi-driver/csi.sock", "CSI endpoint")
	nodeID          = flag.String("nodeid", "", "node id")
	driverName      = flag.String("drivername", "my-csi-driver", "name of the driver")
	workingMountDir = flag.String("working-mount-dir", "/var/lib/my-csi-driver", "directory for image files backing the volumes")
	mode            = flag.String("mode", "both", "driver mode: controller | node | both")
)

func main() {
	klog.InitFlags(nil)
	_ = flag.Set("logtostderr", "true")
	flag.Parse()
	if *nodeID == "" {
		// Backwards compatibility fallback: try NODE_NAME env (typical Downward API) then hostname
		if envNode := os.Getenv("NODE_NAME"); envNode != "" {
			*nodeID = envNode
			klog.Infof("nodeid flag not set; using NODE_NAME env: %s", *nodeID)
		} else if hn, err := os.Hostname(); err == nil && hn != "" {
			*nodeID = hn
			klog.Infof("nodeid flag not set; using hostname: %s", *nodeID)
		} else {
			klog.Warning("nodeid is empty (no flag, NODE_NAME env, or hostname available)")
		}
	}

	handle()
	os.Exit(0)
}

func handle() {
	// Resolve backing directory with precedence: env -> flag -> default
	backingDir := os.Getenv("CSI_BACKING_DIR")
	if backingDir == "" {
		if *workingMountDir != "" {
			backingDir = *workingMountDir
		} else {
			backingDir = "/var/lib/my-csi-driver"
		}
	}

	driverOptions := rawfile.DriverOptions{
		NodeID:     *nodeID,
		DriverName: *driverName,
		Endpoint:   *endpoint,
		BackingDir: backingDir,
		Mode:       *mode,
	}
	d := rawfile.NewDriver(&driverOptions)
	d.Run(false)
}
