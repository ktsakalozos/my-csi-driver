package rawfile

import (
	"context"

	"github.com/container-storage-interface/spec/lib/go/csi"
)

// IdentityServer implements the CSI Identity service endpoints.
type IdentityServer struct {
	name    string
	version string
	csi.UnimplementedIdentityServer
}

// Compile-time assertion
var _ csi.IdentityServer = (*IdentityServer)(nil)

func NewIdentityServer(name, version string) *IdentityServer {
	return &IdentityServer{name: name, version: version}
}

func (is *IdentityServer) GetPluginInfo(ctx context.Context, req *csi.GetPluginInfoRequest) (*csi.GetPluginInfoResponse, error) {
	return &csi.GetPluginInfoResponse{
		Name:          is.name,
		VendorVersion: is.version,
	}, nil
}

func (is *IdentityServer) GetPluginCapabilities(ctx context.Context, req *csi.GetPluginCapabilitiesRequest) (*csi.GetPluginCapabilitiesResponse, error) {
	caps := []*csi.PluginCapability{}
	// Indicate controller service is available
	caps = append(caps, &csi.PluginCapability{
		Type: &csi.PluginCapability_Service_{
			Service: &csi.PluginCapability_Service{
				Type: csi.PluginCapability_Service_CONTROLLER_SERVICE,
			},
		},
	})
	return &csi.GetPluginCapabilitiesResponse{Capabilities: caps}, nil
}

func (is *IdentityServer) Probe(ctx context.Context, req *csi.ProbeRequest) (*csi.ProbeResponse, error) {
	return &csi.ProbeResponse{}, nil
}
