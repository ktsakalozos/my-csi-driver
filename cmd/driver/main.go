package main

import (
	"flag"
	"os"

	"github.com/ktsakalozos/my-csi-driver/pkg/metrics"
	"github.com/ktsakalozos/my-csi-driver/pkg/rawfile"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	klog "k8s.io/klog/v2"
)

var (
	endpoint        = flag.String("endpoint", "unix:///var/lib/kubelet/plugins/my-csi-driver/csi.sock", "CSI endpoint")
	nodeID          = flag.String("nodeid", "", "node id")
	driverName      = flag.String("drivername", "my-csi-driver", "name of the driver")
	workingMountDir = flag.String("working-mount-dir", "/var/lib/my-csi-driver", "directory for image files backing the volumes")
	mode            = flag.String("mode", "both", "driver mode: controller | node | both")
	metricsPort     = flag.Int("metrics-port", 9898, "port for prometheus metrics endpoint")
	standaloneMode  = flag.Bool("standalone", false, "run without Kubernetes API (for testing only)")
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
	// Create Kubernetes clientset for in-cluster configuration
	var clientset kubernetes.Interface
	if *standaloneMode {
		klog.Warningf("Running in standalone mode without Kubernetes API (testing only)")
		clientset = nil
	} else {
		config, err := clientcmd.BuildConfigFromFlags("", "") // Use in-cluster config
		if err != nil {
			klog.Fatalf("Error building kubeconfig: %s", err.Error())
		}
		var err2 error
		clientset, err2 = kubernetes.NewForConfig(config)
		if err2 != nil {
			klog.Fatalf("Error building kubernetes clientset: %s", err2.Error())
		}
	}

	// Resolve backing directory with precedence: env -> flag -> default
	backingDir := os.Getenv("CSI_BACKING_DIR")
	if backingDir == "" {
		if *workingMountDir != "" {
			backingDir = *workingMountDir
		} else {
			backingDir = "/var/lib/my-csi-driver"
		}
	}

	// Start metrics server
	if *metricsPort > 0 {
		metricsServer := metrics.NewServer(*metricsPort)
		collector := metrics.NewVolumeStatsCollector(*nodeID, backingDir)
		if err := metricsServer.RegisterCollector(collector); err != nil {
			klog.Warningf("Failed to register metrics collector: %v", err)
		} else {
			if err := metricsServer.Start(); err != nil {
				klog.Warningf("Failed to start metrics server: %v", err)
			}
		}
	}

	driverOptions := rawfile.DriverOptions{
		NodeID:     *nodeID,
		DriverName: *driverName,
		Endpoint:   *endpoint,
		BackingDir: backingDir,
		Mode:       *mode,
		Clientset:  clientset,
	}
	d := rawfile.NewDriver(&driverOptions)
	d.Run(false)
}
