package proxy_test

import (
	"net"
	"testing"
	"time"

	"github.com/realDragonium/Ultraviolet/config"
	"github.com/realDragonium/Ultraviolet/mc"
	"github.com/realDragonium/Ultraviolet/proxy"
)

var basicServerCfgs = []config.ServerConfig{
	{
		Domains: []string{"uv1"},
		ProxyTo: testAddr(),
	},
	{
		Domains: []string{"uv2", "uve1"},
		ProxyTo: testAddr(),
	},
	{
		Domains: []string{"uv3", "uve2"},
		ProxyTo: testAddr(),
	},
	{
		Domains: []string{"uv4", "uve3", "uve4"},
		ProxyTo: testAddr(),
	},
}

type benchLogger struct {
	b *testing.B
}

func (bLog *benchLogger) Write(b []byte) (n int, err error) {
	bLog.b.Logf(string(b))
	return 0, nil
}

func basicBenchUVConfig(b *testing.B, w, cw, sw int) config.UltravioletConfig {
	return config.UltravioletConfig{
		NumberOfWorkers:       w,
		NumberOfConnWorkers:   cw,
		NumberOfStatusWorkers: sw,
		DefaultStatus: mc.AnotherStatusResponse{
			Name:        "Ultraviolet",
			Protocol:    755,
			Description: "Another proxy server",
		},
		LogOutput: &benchLogger{b: b},
	}
}

var serverWorkerCfg = proxy.WorkerServerConfig{
	StateUpdateCooldown: time.Second * 10,
	OfflineStatus: mc.AnotherStatusResponse{
		Name:        "Ultraviolet",
		Protocol:    755,
		Description: "Some benchmark status",
	}.Marshal(),
	ProxyTo: "127.0.0.1:29870",
	DisconnectPacket: mc.ClientBoundDisconnect{
		Reason: "Benchmarking stay out!",
	}.Marshal(),
}

func BenchmarkPublicWorker_ProcessMcRequest_UnknownAddress_ReturnsValues(b *testing.B) {
	serverAddr := "ultraviolet"
	servers := make(map[int]proxy.ServerWorkerData)
	serverDict := make(map[string]int)
	reqCh := make(chan proxy.McRequest)
	publicWorker := proxy.NewPublicWorker(servers, serverDict, mc.Packet{}, reqCh)
	answerCh := make(chan proxy.McAnswer)
	req := proxy.McRequest{
		Type:       proxy.STATUS,
		ServerAddr: serverAddr,
		Ch:         answerCh,
	}
	go publicWorker.Work()

	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		reqCh <- req
		<-answerCh
	}
}

func BenchmarkPrivateWorker_HandleRequest_Status_Offline(b *testing.B) {
	privateWorker := proxy.NewPrivateWorker(0, serverWorkerCfg)

	answerCh := make(chan proxy.McAnswer)
	req := proxy.McRequest{
		Type: proxy.STATUS,
		Ch:   answerCh,
	}

	go func() {
		for {
			<-answerCh
		}
	}()

	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		privateWorker.HandleRequest(req)
	}
}

func BenchmarkPrivateWorker_HandleRequest_Status_Online(b *testing.B) {
	serverCfg := serverWorkerCfg
	serverCfg.ProxyTo = testAddr()
	privateWorker := proxy.NewPrivateWorker(0, serverCfg)
	answerCh := make(chan proxy.McAnswer)
	req := proxy.McRequest{
		Type: proxy.STATUS,
		Ch:   answerCh,
	}

	listener, err := net.Listen("tcp", serverCfg.ProxyTo)
	if err != nil {
		b.Fatal(err)
	}

	go func() {
		for {
			listener.Accept()
		}
	}()

	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		privateWorker.HandleRequest(req)
	}
}

func BenchmarkPrivateWorker_HandleRequest_Login_Offline(b *testing.B) {
	privateWorker := proxy.NewPrivateWorker(0, serverWorkerCfg)

	answerCh := make(chan proxy.McAnswer)
	req := proxy.McRequest{
		Type: proxy.STATUS,
		Ch:   answerCh,
	}

	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		privateWorker.HandleRequest(req)
	}
}

func BenchmarkPrivateWorker_HandleRequest_Login_Online_StateUpdate100us(b *testing.B) {
	benchmarkPrivateWorker_HandleRequest_Login_Online_StateUpdate(b, 100*time.Microsecond)
}

func BenchmarkPrivateWorker_HandleRequest_Login_Online_StateUpdate10ms(b *testing.B) {
	benchmarkPrivateWorker_HandleRequest_Login_Online_StateUpdate(b, 10*time.Millisecond)
}

func BenchmarkPrivateWorker_HandleRequest_Login_Online_StateUpdate1us(b *testing.B) {
	benchmarkPrivateWorker_HandleRequest_Login_Online_StateUpdate(b, time.Microsecond)
}

func BenchmarkPrivateWorker_HandleRequest_Login_Online_StateUpdate10s(b *testing.B) {
	benchmarkPrivateWorker_HandleRequest_Login_Online_StateUpdate(b, 10*time.Second)
}

func benchmarkPrivateWorker_HandleRequest_Login_Online_StateUpdate(b *testing.B, stateCooldown time.Duration) {
	testAddr := testAddr()
	serverCfg := serverWorkerCfg
	serverCfg.ProxyTo = testAddr
	serverCfg.StateUpdateCooldown = stateCooldown
	privateWorker := proxy.NewPrivateWorker(0, serverCfg)

	req := proxy.McRequest{
		Type: proxy.LOGIN,
	}

	benchmarkPrivateWorker_HandleRequest_Online(b, privateWorker, req, testAddr)
}

func benchmarkPrivateWorker_HandleRequest_Online(b *testing.B, pWorker proxy.PrivateWorker, req proxy.McRequest, addr string) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		b.Fatal(err)
	}

	go func() {
		for {
			listener.Accept()
		}
	}()

	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		pWorker.HandleRequest(req)
	}
}

// func basicWorkerServerConfigMap(cfgs []config.ServerConfig) map[string]proxy.WorkerServerConfig {
// 	servers := make(map[string]proxy.WorkerServerConfig)
// 	for _, cfg := range cfgs {
// 		workerCfg := proxy.FileToWorkerConfig(cfg)
// 		servers[cfg.MainDomain] = workerCfg
// 		for _, extraDomains := range cfg.ExtraDomains {
// 			servers[extraDomains] = workerCfg
// 		}
// 	}

// 	return servers
// }

// func BenchmarkStatusWorker_dialTimeout_5s_stateCooldown_5s(b *testing.B) {
// 	benchmarkStatusWorker_StatusRequest_OfflineServer_WithConnWorker(b, "5s", "5s")
// }

// func BenchmarkStatusWorker_dialTimeout_1s_stateCooldown_1s(b *testing.B) {
// 	benchmarkStatusWorker_StatusRequest_OfflineServer_WithConnWorker(b, "1s", "1s")
// }

// func BenchmarkStatusWorker_dialTimeout_100ms_stateCooldown_1s(b *testing.B) {
// 	benchmarkStatusWorker_StatusRequest_OfflineServer_WithConnWorker(b, "100ms", "1s")
// }

// func BenchmarkStatusWorker_dialTimeout_1s_stateCooldown_100ms(b *testing.B) {
// 	benchmarkStatusWorker_StatusRequest_OfflineServer_WithConnWorker(b, "1s", "100ms")
// }

// func BenchmarkStatusWorker_dialTimeout_100ms_stateCooldown_100ms(b *testing.B) {
// 	benchmarkStatusWorker_StatusRequest_OfflineServer_WithConnWorker(b, "100ms", "100ms")
// }

// func BenchmarkStatusWorker_dialTimeout_100ms_stateCooldown_10ms(b *testing.B) {
// 	benchmarkStatusWorker_StatusRequest_OfflineServer_WithConnWorker(b, "100ms", "10ms")
// }

// func BenchmarkStatusWorker_dialTimeout_10ms_stateCooldown_10ms(b *testing.B) {
// 	benchmarkStatusWorker_StatusRequest_OfflineServer_WithConnWorker(b, "10ms", "10ms")
// }

// func BenchmarkStatusWorker_dialTimeout_1ms_stateCooldown_10ms(b *testing.B) {
// 	benchmarkStatusWorker_StatusRequest_OfflineServer_WithConnWorker(b, "1ms", "10ms")
// }

// func benchmarkStatusWorker_StatusRequest_OfflineServer_WithConnWorker(b *testing.B, dialTime, cooldown string) {
// 	connCh := make(chan proxy.ConnRequest)
// 	statusCh := make(chan proxy.StatusRequest)
// 	serverCfgs := basicServerCfgs
// 	for _, cfg := range serverCfgs {
// 		cfg.DialTimeout = dialTime
// 		cfg.UpdateCooldown = cooldown
// 	}
// 	servers := basicWorkerServerConfigMap(serverCfgs)

// 	proxy.RunConnWorkers(10, connCh, statusCh, servers)
// 	statusWorker := proxy.NewStatusWorker(statusCh, connCh, servers)
// 	req := proxy.StatusRequest{
// 		ServerId: "uv1",
// 		Type:     proxy.STATUS_REQUEST,
// 	}
// 	b.ResetTimer()
// 	b.ReportAllocs()
// 	for n := 0; n < b.N; n++ {
// 		statusWorker.WorkSingle(req)
// 	}
// }

// func BenchmarkStatusWorker_StatusUpdate_To_Online(b *testing.B) {
// 	connCh := make(chan proxy.ConnRequest)
// 	statusCh := make(chan proxy.StatusRequest)
// 	servers := basicWorkerServerConfigMap(basicServerCfgs)
// 	statusWorker := proxy.NewStatusWorker(statusCh, connCh, servers)
// 	req := proxy.StatusRequest{
// 		ServerId: "uv1",
// 		Type:     proxy.STATE_UPDATE,
// 		State:    proxy.ONLINE,
// 	}

// 	b.ResetTimer()
// 	for n := 0; n < b.N; n++ {
// 		statusWorker.WorkSingle(req)
// 	}
// }

// func BenchmarkStatusWorker_StatusUpdate_To_Online_CHANNEL(b *testing.B) {
// 	connCh := make(chan proxy.ConnRequest)
// 	statusCh := make(chan proxy.StatusRequest)
// 	servers := basicWorkerServerConfigMap(basicServerCfgs)
// 	statusWorker := proxy.NewStatusWorker(statusCh, connCh, servers)
// 	go statusWorker.Work()

// 	req := proxy.StatusRequest{
// 		ServerId: "uv1",
// 		Type:     proxy.STATE_UPDATE,
// 		State:    proxy.ONLINE,
// 	}

// 	b.ResetTimer()
// 	for n := 0; n < b.N; n++ {
// 		statusCh <- req
// 	}
// }

// func BenchmarkStatusWorker_StatusUpdate_To_Offline(b *testing.B) {
// 	connCh := make(chan proxy.ConnRequest)
// 	statusCh := make(chan proxy.StatusRequest)
// 	servers := basicWorkerServerConfigMap(basicServerCfgs)
// 	statusWorker := proxy.NewStatusWorker(statusCh, connCh, servers)
// 	req := proxy.StatusRequest{
// 		ServerId: "uv4",
// 		Type:     proxy.STATE_UPDATE,
// 		State:    proxy.OFFLINE,
// 	}

// 	b.ResetTimer()
// 	for n := 0; n < b.N; n++ {
// 		statusWorker.WorkSingle(req)
// 	}
// }

func BenchmarkWorkerStatusRequest_KnownServer_Offline_CHANNEL(b *testing.B) {
	req := proxy.McRequest{
		ServerAddr: "something",
		Type:       proxy.STATUS,
	}
	benchmarkWorker(b, req)
}
func BenchmarkWorkerStatusRequest_UnknownServer_CHANNEL(b *testing.B) {
	req := proxy.McRequest{
		ServerAddr: "something",
		Type:       proxy.STATUS,
	}
	benchmarkWorker(b, req)
}

func benchmarkWorker(b *testing.B, req proxy.McRequest) {
	cfg := basicBenchUVConfig(b, 1, 1, 1)
	servers := basicServerCfgs
	reqCh := make(chan proxy.McRequest)
	proxy.SetupWorkers(cfg, servers, reqCh, nil)

	answerCh := make(chan proxy.McAnswer)
	req.Ch = answerCh

	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		reqCh <- req
		<-answerCh
	}
}

// func BenchmarkNetworkStatusRequest_UnknownServer(b *testing.B) {
// 	targetAddr := testAddr()
// 	cfg := config.UltravioletConfig{
// 		NumberOfWorkers:       1,
// 		NumberOfConnWorkers:   1,
// 		NumberOfStatusWorkers: 1,
// 		DefaultStatus: mc.AnotherStatusResponse{
// 			Name:        "Ultraviolet",
// 			Protocol:    755,
// 			Description: "Another proxy server",
// 		},
// 	}
// 	servers := []config.ServerConfig{}
// 	reqCh := make(chan proxy.McRequest)
// 	ln, err := net.Listen("tcp", targetAddr)
// 	if err != nil {
// 		b.Fatalf("Can't listen: %v", err)
// 	}
// 	go proxy.ServeListener(ln, reqCh)

// 	proxy.SetupWorkers(cfg, servers, reqCh, nil)

// 	handshakePk := mc.ServerBoundHandshake{
// 		ProtocolVersion: 755,
// 		ServerAddress:   "unknown",
// 		ServerPort:      25565,
// 		NextState:       mc.HandshakeStatusState,
// 	}.Marshal()
// 	handshakeBytes, _ := handshakePk.Marshal()

// 	statusRequestPk := mc.ServerBoundRequest{}.Marshal()
// 	statusRequestBytes, _ := statusRequestPk.Marshal()
// 	pingPk := mc.NewServerBoundPing().Marshal()
// 	pingBytes, _ := pingPk.Marshal()

// 	readBuffer := make([]byte, 0xffff)

// 	b.ResetTimer()
// 	for n := 0; n < b.N; n++ {
// 		conn, err := net.Dial("tcp", targetAddr)
// 		if err != nil {
// 			b.Fatalf("error while trying to connect: %v", err)
// 		}
// 		conn.Write(handshakeBytes)
// 		conn.Write(statusRequestBytes)
// 		conn.Read(readBuffer)
// 		conn.Write(pingBytes)
// 		conn.Read(readBuffer)
// 	}
// }