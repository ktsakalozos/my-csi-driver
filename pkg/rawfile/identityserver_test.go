package rawfile

import (
	"context"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
)

func TestIdentity_GetPluginCapabilities_ControllerService(t *testing.T) {
	is := NewIdentityServer("my-csi-driver", "v1.0.0")
	resp, err := is.GetPluginCapabilities(context.Background(), &csi.GetPluginCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, cap := range resp.Capabilities {
		if cap.GetService().GetType() == csi.PluginCapability_Service_CONTROLLER_SERVICE {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Controller service capability not reported")
	}
}
