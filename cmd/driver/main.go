package main

import (
	"flag"
	"log"

	"github.com/ktsakalozos/my-csi-driver/pkg"
)

func main() {
	var endpoint string
	flag.StringVar(&endpoint, "endpoint", "/var/lib/kubelet/plugins/my-csi-driver/csi.sock", "CSI unix socket endpoint")
	flag.Parse()

	driver := pkg.NewMyCSIDriver("my-csi-driver", "0.1.0", "node-1")
	if err := driver.Run(endpoint); err != nil {
		log.Fatalf("driver exited: %v", err)
	}
}
