# wsvnc

This is an executable program, which may be as the server of [noVNC](https://github.com/novnc/noVNC) instead of [websockify](https://github.com/novnc/websockify).

### Build

```shell
$ dep ensure
$ go build
```

### Run

```shell
$ ./vncproxy
```

Notice: The current host must run a redis server listening on `127.0.0.1:6379`, or you can modify it by the cli option `redis`.

```shell
[root@localhost opt]# ./vncproxy -h
Usage of ./vncproxy:
  -certfile string
        The path of cert file.
  -config-file string
        The path of the ini config file.
  -expiration string
        The expiration time of the token. (default "0s")
  -keyfile string
        The path of the key file.
  -listenaddr string
        The address that VNC proxy listens to. (default ":5900")
  -logfile string
        The path of the log file.
  -loglevel string
        The level of logging, such as debug, info, etc. (default "debug")
  -manageraddr string
        The address that the manager listens to. (default "127.0.0.1:9999")
  -redisurl string
        The url to connect to redis. (default "redis://localhost:6379/0")
  -version
        Print the version and exit.
```
