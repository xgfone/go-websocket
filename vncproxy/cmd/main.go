package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/go-redis/redis"
	"github.com/xgfone/go-tools/lifecycle"
	"github.com/xgfone/go-tools/net2/http2"
	"github.com/xgfone/go-tools/signal2"
	"github.com/xgfone/miss"
	"github.com/xgfone/websocket/vncproxy"
)

var logger = miss.GetGlobalLogger()

var versionS = "1.0.0"

var (
	version     bool
	logFile     string
	logLevel    string
	listenAddr  string
	redisURL    string
	certFile    string
	keyFile     string
	managerAddr string
	expiration  int
)

func init() {
	flag.BoolVar(&version, "version", false, "Print the version and exit")
	flag.StringVar(&logFile, "logfile", "", "The path of the log file")
	flag.StringVar(&logLevel, "loglevel", "debug", "The level of the log, such as debug, info, etc")
	flag.StringVar(&listenAddr, "addr", ":5900", "The listen address")
	flag.StringVar(&redisURL, "redis", "redis://localhost:6379/0", "The redis connection url")
	flag.StringVar(&certFile, "cert", "", "The path of the cert file")
	flag.StringVar(&keyFile, "key", "", "The path of the key file")
	flag.StringVar(&managerAddr, "manager_addr", "127.0.0.1:9999", "The address to be used to manage")
	flag.IntVar(&expiration, "expire", 0, "The expiration time(s) of the token")
}

func main() {
	defer lifecycle.Stop()
	flag.Parse()
	if version {
		fmt.Println(versionS)
		return
	}
	tokenExpiration := time.Duration(expiration) * time.Second

	// Handle the logging
	level := miss.NameToLevel(logLevel)
	var out io.Writer = os.Stdout
	if logFile != "" {
		file, err := miss.SizedRotatingFileWriter(logFile, 1024*1024*1024, 30)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		lifecycle.Register(func() { file.Close() })
		out = file
	}
	encConf := miss.EncoderConfig{IsLevel: true, IsTime: true}
	encoder := miss.KvTextEncoder(out, encConf)
	logger = miss.New(encoder).Level(level).Cxt("caller", miss.Caller())

	// Handle the redis client
	redisOpt, err := redis.ParseURL(redisURL)
	if err != nil {
		logger.Error("can't parse redis URL", "url", redisURL, "err", err)
		return
	}
	redisClient := redis.NewClient(redisOpt)
	lifecycle.Register(func() { redisClient.Close() })

	wsconf := vncproxy.ProxyConfig{
		GetBackend: func(r *http.Request) string {
			if vs := r.URL.Query()["token"]; len(vs) > 0 {
				token, err := redisClient.Get(vs[0]).Result()
				if err != nil && err != redis.Nil {
					logger.Error("redis GET error", "err", err)
				}
				return token
			}
			return ""
		},
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
	handler := vncproxy.NewWebsocketVncProxyHandler(wsconf)
	http.HandleFunc("/connections", func(w http.ResponseWriter, r *http.Request) {
		http2.String(w, http.StatusOK, "%d", handler.Connections())
	})
	http.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		token := http2.GetQuery(query, "token")
		addr := http2.GetQuery(query, "addr")
		if token == "" || addr == "" {
			http2.String(w, http.StatusBadRequest, "missing token or addr")
			return
		}
		redisClient.Set(token, addr, tokenExpiration)
	})

	go signal2.HandleSignal()
	go func() {
		logger.Info("manager listening", "addr", managerAddr)
		if err := http2.ListenAndServe(managerAddr, nil); err != nil {
			logger.Error("Manager ListenAndServe", "err", err)
			lifecycle.Stop()
		}
	}()

	tlsfiles := []string{}
	if certFile != "" && keyFile != "" {
		tlsfiles = []string{certFile, keyFile}
	} else if certFile != "" || keyFile != "" {
		logger.Warn("The cert and key file is incomplete and don't use TLS")
	}

	logger.Info("Listening", "addr", listenAddr, "tls", len(tlsfiles) != 0)
	if err := http2.ListenAndServe(listenAddr, handler, tlsfiles...); err != nil {
		logger.Error("ListenAndServe", "err", err)
	}
}
