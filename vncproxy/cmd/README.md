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
Usage of ./wsvnc:
  -addr string
        The listen address (default ":5900")
  -cert string
        The path of the cert file
  -expire int
        The expiration time(s) of the token
  -key string
        The path of the key file
  -logfile string
        The path of the log file
  -loglevel string
        The level of the log, such as debug, info, etc (default "debug")
  -manager_addr string
        The address to be used to manage (default "127.0.0.1:9999")
  -redis string
        The redis connection url (default "redis://localhost:6379/0")
  -version
        Print the version and exit
```
