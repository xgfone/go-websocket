# wsvnc

This is an executable program, which may be as the server of [noVNC](https://github.com/novnc/noVNC) instead of [websockify](https://github.com/novnc/websockify).

### Build

```shell
$ go build
```

### Run

```shell
$ ./vncproxy
```

Notice: The current host must run a redis server listening on `127.0.0.1:6379`, or you can modify it by the cli option `redis`.

```shell
[root@localhost ~]# ./vncproxy -h
Usage of ./vncproxy:
  --certfile string
        The path of cert file. (default "")
  --config-file string
        The path of the INI config file. (default "")
  --expiration duration
        The expiration time of the token. (default 0s)
  --keyfile string
        The path of the key file. (default "")
  --listenaddr string
        The address that VNC proxy listens to. (default ":5900")
  --logfile string
        The path of the log file. (default "")
  --loglevel string
        The level of logging, such as debug, info, etc. (default "debug")
  --manageraddr string
        The address that the manager listens to. It is disabled by default.
  --redisurl string
        The url to connect to redis. (default "redis://localhost:6379/0")
  --version bool
        Print the version and exit. (default false)
```

It will recognize the format of the configuration file appointed by `--config-file` by the extension name. If the configuration file is updated, it will be loaded automatically. See [`github.com/xgfone/gconf`](https://github.com/xgfone/gconf#use-the-config-file).
