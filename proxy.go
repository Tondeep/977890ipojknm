package Ultraviolet

import (
	"errors"
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/cloudflare/tableflip"
	"github.com/pires/go-proxyproto"
	"github.com/realDragonium/Ultraviolet/config"
	"github.com/realDragonium/Ultraviolet/server"
)

var (
	// Isnt this the proper path to put config files into (for execution without docker)
	defaultCfgPath = "/etc/ultraviolet"
)

func RunProxy() {
	log.Println("Starting up Alpha-v0.12")
	var (
		cfgDir = flag.String("configs", defaultCfgPath, "`Path` to config directory")
	)
	flag.Parse()

	mainCfgPath := filepath.Join(*cfgDir, "ultraviolet.json")
	mainCfg, err := config.ReadUltravioletConfig(mainCfgPath)
	if err != nil {
		log.Fatalf("Read main config file at '%s' - error: %v", mainCfgPath, err)
	}

	serverCfgsPath := filepath.Join(*cfgDir, "config")
	serverCfgs, err := config.ReadServerConfigs(serverCfgsPath)
	if err != nil {
		log.Fatalf("Something went wrong while reading config files: %v", err)
	}

	StartWorkers(mainCfg, serverCfgs)

	log.Printf("Finished starting up")
	select {}
}

func createListener(listenAddr string, useProxyProtocol bool) net.Listener {
	pidFile := "/var/run/Ultraviolet.pid"
	upg, err := tableflip.New(tableflip.Options{
		PIDFile: pidFile,
	})
	if err != nil {
		panic(err)
	}
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGHUP)
		for range sig {
			err := upg.Upgrade()
			if err != nil {
				log.Println("upgrade failed:", err)
			}
		}
	}()

	ln, err := upg.Listen("tcp", listenAddr)
	if err != nil {
		log.Fatalf("Can't listen: %v", err)
	}
	if useProxyProtocol {
		proxyListener := &proxyproto.Listener{
			Listener:          ln,
			ReadHeaderTimeout: 1 * time.Second,
		}
		return proxyListener
	}
	return ln
}

func serveListener(listener net.Listener, reqCh chan net.Conn) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				log.Printf("net.Listener was closed, stopping with accepting calls")
				break
			}
			log.Println(err)
			continue
		}
		log.Printf("received connection from: %v", conn.RemoteAddr())
		reqCh <- conn
	}
}

func StartWorkers(cfg config.UltravioletConfig, serverCfgs []config.ServerConfig) {
	reqCh := make(chan net.Conn, 50)
	if cfg.LogOutput != nil {
		log.SetOutput(cfg.LogOutput)
	}

	listener := createListener(cfg.ListenTo, cfg.UseProxyProtocol)
	for i := 0; i < cfg.NumberOfListeners; i++ {
		go func(listener net.Listener, reqCh chan net.Conn) {
			serveListener(listener, reqCh)
		}(listener, reqCh)
	}
	log.Printf("Running %v listener(s)", cfg.NumberOfListeners)

	statusPk := cfg.DefaultStatus.Marshal()
	defaultStatus := statusPk.Marshal()
	worker := server.NewBasicWorker(defaultStatus, reqCh)
	for id, serverCfg := range serverCfgs {
		workerServerCfg, _ := config.FileToWorkerConfig2(serverCfg)
		serverWorker := server.NewBasicBackendWorker(id, workerServerCfg)
		for _, domain := range serverCfg.Domains {
			worker.RegisterBackendWorker(domain, serverWorker)
		}
		go serverWorker.Work()
	}
	log.Printf("Registered %v backend(s)", len(serverCfgs))

	for i := 0; i < cfg.NumberOfWorkers; i++ {
		go func(worker server.BasicWorker) {
			worker.Work()
		}(worker)
	}
	log.Printf("Running %v worker(s)", cfg.NumberOfWorkers)
}
