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
)

func main() {
	klog.InitFlags(nil)
	_ = flag.Set("logtostderr", "true")
	flag.Parse()
	if *nodeID == "" {
		klog.Warning("nodeid is empty")
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
	}
	d := rawfile.NewDriver(&driverOptions)
	d.Run(false)
}
