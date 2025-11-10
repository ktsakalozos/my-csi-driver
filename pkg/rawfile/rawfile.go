package rawfile

import (
	"context"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

// DriverOptions defines driver parameters specified in driver deployment
type DriverOptions struct {
	NodeID                       string
	DriverName                   string
	Endpoint                     string
	MountPermissions             uint64
	BackingDir                   string
	Mode                         string
	DefaultOnDeletePolicy        string
	VolStatsCacheExpireInMinutes int
	RemoveArchivedVolumePath     bool
	UseTarCommandInSnapshot      bool
	Clientset                    kubernetes.Interface
}

type Driver struct {
	name       string
	nodeID     string
	version    string
	endpoint   string
	backingDir string
	mode       string
	clientset  kubernetes.Interface
}

func NewDriver(options *DriverOptions) *Driver {
	klog.V(2).Infof("Driver: rawfile")

	d := &Driver{
		name:       options.DriverName,
		version:    "dev",
		nodeID:     options.NodeID,
		endpoint:   options.Endpoint,
		backingDir: options.BackingDir,
		mode:       options.Mode,
		clientset:  options.Clientset,
	}

	return d
}

func (d *Driver) Run(testMode bool) {

	klog.V(2).Infof("Starting CSI driver %s at %s", d.name, d.endpoint)

	s := NewNonBlockingGRPCServer()

	// Decide which servers to run based on mode
	var csServer csi.ControllerServer
	var nsServer *NodeServer
	if d.mode == "controller" || d.mode == "both" {
		csServer = NewControllerServerWithBackingDir(d.name, d.version, d.backingDir, d.clientset)
	}
	if d.mode == "node" || d.mode == "both" {
		nsServer = NewNodeServer(d.nodeID, d.name, d.backingDir, d.clientset)
		// Start garbage collector in a goroutine
		go nsServer.RunGarbageCollector(context.Background(), 5*time.Minute)
	}

	s.Start(d.endpoint,
		NewIdentityServer(d.name, d.version),
		csServer,
		nsServer,
		testMode)
	s.Wait()
}
