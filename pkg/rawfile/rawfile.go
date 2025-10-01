package rawfile

import (
	"k8s.io/klog/v2"
)

// DriverOptions defines driver parameters specified in driver deployment
type DriverOptions struct {
	NodeID                       string
	DriverName                   string
	Endpoint                     string
	MountPermissions             uint64
	BackingDir                   string
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
}

func NewDriver(options *DriverOptions) *Driver {
	klog.V(2).Infof("Driver: rawfile")

	d := &Driver{
		name:       options.DriverName,
		version:    "dev",
		nodeID:     options.NodeID,
		endpoint:   options.Endpoint,
		backingDir: options.BackingDir,
	}

	return d
}

func (d *Driver) Run(testMode bool) {

	klog.V(2).Infof("Starting CSI driver %s at %s", d.name, d.endpoint)

	s := NewNonBlockingGRPCServer()
	s.Start(d.endpoint,
		NewIdentityServer(d.name, d.version),
		NewControllerServerWithBackingDir(d.name, d.version, d.backingDir),
		NewNodeServer(d.nodeID),
		testMode)
	s.Wait()
}
