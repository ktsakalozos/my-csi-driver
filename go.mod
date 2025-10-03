module github.com/ktsakalozos/my-csi-driver

go 1.24.0

toolchain go1.24.7

require (
	github.com/container-storage-interface/spec v1.11.0
	github.com/google/uuid v1.6.0
	github.com/kubernetes-csi/csi-lib-utils v0.22.0
	google.golang.org/grpc v1.69.0
)

require github.com/go-logr/logr v1.4.2 // indirect

require (
	golang.org/x/net v0.38.0 // indirect
	golang.org/x/sys v0.31.0 // indirect
	golang.org/x/text v0.23.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20241216192217-9240e9c98484 // indirect
	google.golang.org/protobuf v1.36.5 // indirect
	k8s.io/klog/v2 v2.130.1
)

replace github.com/ktsakalozos/my-csi-driver => ./
