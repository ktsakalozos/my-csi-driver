package rawfile

import (
	"github.com/container-storage-interface/spec/lib/go/csi"
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
}

type Driver struct {
	name       string
	nodeID     string
	version    string
	endpoint   string
	backingDir string
	mode       string
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
	}

	return d
}

func (d *Driver) Run(testMode bool) {

	klog.V(2).Infof("Starting CSI driver %s at %s", d.name, d.endpoint)

	s := NewNonBlockingGRPCServer()

	// Decide which servers to run based on mode
	var csServer csi.ControllerServer
	var nsServer csi.NodeServer
	if d.mode == "controller" || d.mode == "both" {
		csServer = NewControllerServerWithNodeID(d.name, d.version, d.backingDir, d.nodeID)
	}
	if d.mode == "node" || d.mode == "both" {
		nsServer = NewNodeServer(d.nodeID)
	}

	s.Start(d.endpoint,
		NewIdentityServer(d.name, d.version),
		csServer,
		nsServer,
		testMode)
	s.Wait()
}
