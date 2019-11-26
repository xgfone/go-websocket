module github.com/xgfone/websocket/vncproxy/cmd

require (
	github.com/go-redis/redis v6.6.0+incompatible
	github.com/onsi/ginkgo v1.8.0 // indirect
	github.com/onsi/gomega v1.5.0 // indirect
	github.com/xgfone/gconf/v4 v4.2.0
	github.com/xgfone/klog/v2 v2.2.0
	github.com/xgfone/ship/v2 v2.0.0
	github.com/xgfone/websocket v1.6.0
)

replace github.com/xgfone/websocket => ../../

go 1.11
