module github.com/xgfone/websocket/vncproxy/vncproxy

require (
	github.com/go-redis/redis/v7 v7.2.0
	github.com/xgfone/gconf/v4 v4.2.0
	github.com/xgfone/klog/v3 v3.0.0
	github.com/xgfone/ship/v2 v2.4.0
	github.com/xgfone/websocket v1.8.1
)

replace github.com/xgfone/websocket => ../../

go 1.11
