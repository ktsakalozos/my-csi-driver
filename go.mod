module github.com/ktsakalozos/my-csi-driver

go 1.23.11

require (
	github.com/container-storage-interface/spec v1.9.0
	github.com/google/uuid v1.6.0
	google.golang.org/grpc v1.65.0
)

require (
	github.com/golang/protobuf v1.5.4 // indirect
	golang.org/x/net v0.25.0 // indirect
	golang.org/x/sys v0.20.0 // indirect
	golang.org/x/text v0.15.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240528184218-531527333157 // indirect
	google.golang.org/protobuf v1.34.1 // indirect
)

replace github.com/ktsakalozos/my-csi-driver => ./
