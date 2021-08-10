package ultraviolet_test

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/pires/go-proxyproto"
	ultraviolet "github.com/realDragonium/Ultraviolet"
	"github.com/realDragonium/Ultraviolet/config"
	"github.com/realDragonium/Ultraviolet/mc"
)

var (
	defaultChTimeout = 25 * time.Millisecond
)

var RequestStateInfo = []struct {
	reqType         mc.HandshakeState
	denyAction      ultraviolet.BackendAction
	unknownAction   ultraviolet.BackendAction
	onlineAction    ultraviolet.BackendAction
	offlineAction   ultraviolet.BackendAction
	rateLimitAction ultraviolet.BackendAction
}{
	{
		reqType:         mc.STATUS,
		denyAction:      ultraviolet.CLOSE,
		unknownAction:   ultraviolet.SEND_STATUS,
		onlineAction:    ultraviolet.PROXY,
		offlineAction:   ultraviolet.SEND_STATUS,
		rateLimitAction: ultraviolet.CLOSE,
	},
	{
		reqType:         mc.LOGIN,
		denyAction:      ultraviolet.DISCONNECT,
		unknownAction:   ultraviolet.CLOSE,
		onlineAction:    ultraviolet.PROXY,
		offlineAction:   ultraviolet.DISCONNECT,
		rateLimitAction: ultraviolet.CLOSE,
	},
}

var ErrNoResponse = errors.New("there was no response from worker")

func defaultOfflineStatusPacket() mc.Packet {
	return defaultOfflineStatus().Marshal()
}

func defaultOfflineStatus() mc.SimpleStatus {
	return mc.SimpleStatus{
		Name:        "Ultraviolet-ff",
		Protocol:    755,
		Description: "offline proxy being tested",
	}
}

var ErrEmptyConnCreator = errors.New("this is a test conn creator which doesnt provide connections")

type testConnCreator struct {
}

func (creator testConnCreator) Conn() func() (net.Conn, error) {
	return func() (net.Conn, error) {
		return nil, ErrEmptyConnCreator
	}
}

type testHandshakeModifier struct {
	hasBeenCalled bool
}

func (modifier *testHandshakeModifier) Modify(hs *mc.ServerBoundHandshake, addr string) {
	modifier.hasBeenCalled = true
}

type testConnectionLimiter struct {
	hasBeenCalled bool
	answer        ultraviolet.ProcessAnswer
	allow         bool
}

func (limiter *testConnectionLimiter) Allow(req ultraviolet.BackendRequest) (ultraviolet.ProcessAnswer, bool) {
	limiter.hasBeenCalled = true
	return limiter.answer, limiter.allow
}

type testServerState struct {
	hasBeenCalled bool
	state         ultraviolet.ServerState
}

func (state *testServerState) State() ultraviolet.ServerState {
	state.hasBeenCalled = true
	return state.state
}

type testStatusCache struct {
	hasBeenCalled bool
	answer        ultraviolet.ProcessAnswer
	err           error
}

func (cache *testStatusCache) Status() (ultraviolet.ProcessAnswer, error) {
	cache.hasBeenCalled = true
	return cache.answer, cache.err
}

//Test Help methods
func setupBackendWorker(t *testing.T, serverCfg config.ServerConfig) ultraviolet.BackendWorker {
	workerServerCfg, err := config.FileToWorkerConfig(serverCfg)
	if err != nil {
		t.Fatalf("error encounterd: %v", err)
	}
	serverWorker := ultraviolet.NewBackendWorker(workerServerCfg)
	return serverWorker
}

func processRequest_TestTimeout(t *testing.T, worker ultraviolet.BackendWorker, req ultraviolet.BackendRequest) ultraviolet.ProcessAnswer {
	t.Helper()
	answerCh := make(chan ultraviolet.ProcessAnswer)
	go func() {
		answer := worker.HandleRequest(req)
		answerCh <- answer
	}()

	select {
	case answer := <-answerCh:
		t.Log("worker has successfully responded")
		return answer
	case <-time.After(defaultChTimeout):
		t.Fatal("timed out")
	}
	return ultraviolet.ProcessAnswer{}
}

func testCloseConnection(t *testing.T, conn net.Conn) {
	if _, err := conn.Write([]byte{0}); err != nil {
		t.Errorf("Got an unexpected error: %v", err)
	}
}

func samePk(expected, received mc.Packet) bool {
	sameID := expected.ID == received.ID
	sameData := bytes.Equal(expected.Data, received.Data)
	return sameID && sameData
}

func netAddrToIp(addr net.Addr) string {
	return strings.Split(addr.String(), ":")[0]
}

func TestBackendWorker_OfflineServer(t *testing.T) {
	for _, tc := range RequestStateInfo {
		t.Run(fmt.Sprintf("reqType-%v", tc.reqType), func(t *testing.T) {
			serverAddr := "ultraviolet"
			disconnectMessage := "Some disconnect message right here"
			disconPacket := mc.ClientBoundDisconnect{
				Reason: mc.Chat(disconnectMessage),
			}.Marshal()
			serverCfg := config.ServerConfig{
				Domains:           []string{serverAddr},
				ProxyTo:           "1",
				OfflineStatus:     defaultOfflineStatus(),
				DisconnectMessage: disconnectMessage,
			}
			req := ultraviolet.BackendRequest{
				Type: tc.reqType,
			}
			offlineStatusPk := defaultOfflineStatusPacket()
			worker := setupBackendWorker(t, serverCfg)
			answer := processRequest_TestTimeout(t, worker, req)
			if answer.Action() != tc.offlineAction {
				t.Errorf("expected: %v - got: %v", tc.offlineAction, answer.Action())
			}
			receivedPacket := answer.Response()
			if tc.reqType == mc.STATUS {
				if !samePk(offlineStatusPk, receivedPacket) {
					offlineStatus, _ := mc.UnmarshalClientBoundResponse(offlineStatusPk)
					receivedStatus, _ := mc.UnmarshalClientBoundResponse(receivedPacket)
					t.Errorf("expected: %v - got: %v", offlineStatus, receivedStatus)
				}
			} else if tc.reqType == mc.LOGIN {
				if !samePk(disconPacket, receivedPacket) {
					expected, _ := mc.UnmarshalClientDisconnect(disconPacket)
					received, _ := mc.UnmarshalClientDisconnect(receivedPacket)
					t.Errorf("expected: %v - got: %v", expected, received)
				}
			}
		})
	}
}

func TestBackendWorker_OnlineServer(t *testing.T) {
	for _, tc := range RequestStateInfo {
		t.Run(fmt.Sprintf("reqType-%v", tc.reqType), func(t *testing.T) {
			serverAddr := "ultraviolet"
			targetAddr := testAddr()
			serverCfg := config.ServerConfig{
				Domains: []string{serverAddr},
				ProxyTo: targetAddr,
			}
			req := ultraviolet.BackendRequest{
				Type: tc.reqType,
			}
			listener, err := net.Listen("tcp", targetAddr)
			if err != nil {
				t.Fatal(err)
			}
			go func() {
				listener.Accept()

			}()
			worker := setupBackendWorker(t, serverCfg)
			answer := processRequest_TestTimeout(t, worker, req)
			if answer.Action() != tc.onlineAction {
				t.Fatalf("expected: %v - got: %v", tc.onlineAction, answer.Action())
			}
			serverConn, _ := answer.ServerConn()
			testCloseConnection(t, serverConn)
			if answer.ProxyCh() == nil {
				t.Error("No proxy channel provided")
			}
		})
	}
}

func TestBackendWorker_HandshakeModifier(t *testing.T) {
	hsModifier := testHandshakeModifier{}
	worker := ultraviolet.BackendWorker{
		HsModifier:  &hsModifier,
		ServerState: ultraviolet.AlwaysOnlineState{},
		ConnCreator: testConnCreator{},
	}
	req := ultraviolet.BackendRequest{
		Type: mc.LOGIN,
		Handshake: mc.ServerBoundHandshake{
			ServerAddress: "Something",
		},
		Addr: &net.TCPAddr{
			IP:   net.ParseIP("1.1.1.1"),
			Port: 25560,
		},
	}
	answer := processRequest_TestTimeout(t, worker, req)
	if answer.Action() != ultraviolet.PROXY {
		t.Fatalf("expected: %v - got: %v", ultraviolet.PROXY, answer.Action())
	}

	if !hsModifier.hasBeenCalled {
		t.Error("expected handshake modifier to be called but wasnt")
	}

}

func TestBackendWorker_ProxyBind(t *testing.T) {
	for _, tc := range RequestStateInfo {
		t.Run(fmt.Sprintf("reqType-%v", tc.reqType), func(t *testing.T) {
			serverAddr := "ultraviolet"
			targetAddr := testAddr()
			proxyBind := "127.0.0.2"
			serverCfg := config.ServerConfig{
				Domains:   []string{serverAddr},
				ProxyTo:   targetAddr,
				ProxyBind: proxyBind,
			}
			req := ultraviolet.BackendRequest{
				Type: tc.reqType,
			}

			go func() {
				worker := setupBackendWorker(t, serverCfg)
				answer := processRequest_TestTimeout(t, worker, req)
				answer.ServerConn() // Calling it instead of the player's goroutine
			}()

			listener, err := net.Listen("tcp", targetAddr)
			if err != nil {
				t.Fatal(err)
			}

			conn, err := listener.Accept() // State check call (proxy bind should be used here too)
			if err != nil {
				t.Fatal(err)
			}
			if netAddrToIp(conn.RemoteAddr()) != proxyBind {
				t.Errorf("expected: %v - got: %v", proxyBind, netAddrToIp(conn.RemoteAddr()))
			}

		})
	}
}

func TestBackendWorker_ProxyProtocol(t *testing.T) {
	for _, tc := range RequestStateInfo {
		t.Run(fmt.Sprintf("reqType-%v", tc.reqType), func(t *testing.T) {
			serverAddr := "ultraviolet"
			targetAddr := testAddr()
			t.Log(targetAddr)
			playerAddr := &net.TCPAddr{
				IP:   net.ParseIP("187.34.26.123"),
				Port: 49473,
			}
			serverCfg := config.ServerConfig{
				Domains:           []string{serverAddr},
				ProxyTo:           targetAddr,
				SendProxyProtocol: true,
			}
			req := ultraviolet.BackendRequest{
				Type: tc.reqType,
				Addr: playerAddr,
			}
			listener, err := net.Listen("tcp", targetAddr)
			if err != nil {
				t.Fatal(err)
			}
			proxyListener := &proxyproto.Listener{Listener: listener}
			connCh := make(chan net.Conn)
			errorCh := make(chan error)
			go func() {
				for i := 0; i < 2; i++ {
					conn, err := proxyListener.Accept()
					if err != nil {
						errorCh <- err
						return
					}
					connCh <- conn
				}
			}()

			go func() {
				worker := setupBackendWorker(t, serverCfg)
				answer := processRequest_TestTimeout(t, worker, req)
				answer.ServerConn() // Calling it instead of the player's goroutine
			}()

			<-connCh // State check call (no proxy protocol in here)
			select {
			case err := <-errorCh:
				t.Fatalf("error while accepting connection: %v", err)
			case conn := <-connCh:
				t.Log("connection has been created")
				if conn.RemoteAddr().String() != playerAddr.String() {
					t.Errorf("expected: %v - got: %v", playerAddr, conn.RemoteAddr())
				}
			case <-time.After(defaultChTimeout):
				t.Error("timed out")
			}
		})
	}
}

func TestBackendWorker_ConnLimiter(t *testing.T) {
	tt := []struct {
		allowConn         bool
		shouldAnswerMatch bool
		processAnswer     ultraviolet.ProcessAnswer
	}{
		{
			allowConn:         true,
			shouldAnswerMatch: false,
			processAnswer:     ultraviolet.NewCloseAnswer(),
		},
		{
			allowConn:         false,
			shouldAnswerMatch: true,
			processAnswer:     ultraviolet.NewCloseAnswer(),
		},
	}
	for _, tc := range tt {
		name := fmt.Sprintf("allows connection: %v", tc.allowConn)
		t.Run(name, func(t *testing.T) {
			connLimiter := testConnectionLimiter{
				answer: tc.processAnswer,
				allow:  tc.allowConn,
			}
			worker := ultraviolet.BackendWorker{
				ServerState: ultraviolet.AlwaysOnlineState{},
				ConnCreator: testConnCreator{},
				ConnLimiter: &connLimiter,
			}
			req := ultraviolet.BackendRequest{}
			answer := processRequest_TestTimeout(t, worker, req)

			if !connLimiter.hasBeenCalled {
				t.Error("expected conn limiter to be called but it wasnt")
			}

			answerMatch := answer.Action() == tc.processAnswer.Action()
			if answerMatch != tc.shouldAnswerMatch {
				if tc.shouldAnswerMatch {
					t.Error("answer action didnt match to conn limiter answer action")
					t.Logf("received answer: %v", answer)
				} else {
					t.Error("answer action was equal to conn limiter answer action which shouldnt happen")
					t.Logf("received answer: %v", answer)
				}
			}
		})
	}
}

func TestBackendWorker_ServerState(t *testing.T) {
	tt := []struct {
		reqType        mc.HandshakeState
		serverState    ultraviolet.ServerState
		expectedAction ultraviolet.BackendAction
	}{
		{
			reqType:        mc.UNKNOWN_STATE,
			serverState:    ultraviolet.ONLINE,
			expectedAction: ultraviolet.PROXY,
		},
		{
			reqType:        mc.LOGIN,
			serverState:    ultraviolet.OFFLINE,
			expectedAction: ultraviolet.DISCONNECT,
		},
		{
			reqType:        mc.STATUS,
			serverState:    ultraviolet.OFFLINE,
			expectedAction: ultraviolet.SEND_STATUS,
		},
	}
	for _, tc := range tt {
		name := fmt.Sprintf("reqType:%v - serverState:%v", tc.reqType, tc.serverState)
		t.Run(name, func(t *testing.T) {
			serverState := testServerState{
				state: tc.serverState,
			}
			worker := ultraviolet.BackendWorker{
				ServerState: &serverState,
				ConnCreator: testConnCreator{},
			}
			req := ultraviolet.BackendRequest{
				Type: tc.reqType,
			}
			answer := processRequest_TestTimeout(t, worker, req)

			if answer.Action() != tc.expectedAction {
				t.Errorf("expected %v but got %v instead", tc.expectedAction, answer.Action())
			}
			if !serverState.hasBeenCalled {
				t.Error("Expected serverstate to be called but wasnt")
			}
		})
	}
}

func TestBackendWorker_StatusCache(t *testing.T) {
	tt := []struct {
		reqType        mc.HandshakeState
		callsCache     bool
		errToReturn    error
		answer         ultraviolet.ProcessAnswer
		expectedAction ultraviolet.BackendAction
	}{
		{
			reqType:        mc.STATUS,
			callsCache:     true,
			answer:         ultraviolet.ProcessAnswer{},
			expectedAction: ultraviolet.ERROR,
		},
		{
			reqType:        mc.LOGIN,
			callsCache:     false,
			expectedAction: ultraviolet.PROXY,
		},
		{
			reqType:        mc.STATUS,
			callsCache:     true,
			answer:         ultraviolet.NewDisconnectAnswer(mc.Packet{}),
			errToReturn:    nil,
			expectedAction: ultraviolet.DISCONNECT,
		},
		{
			reqType:        mc.STATUS,
			callsCache:     true,
			answer:         ultraviolet.NewDisconnectAnswer(mc.Packet{}),
			errToReturn:    errors.New("random error for testing"),
			expectedAction: ultraviolet.SEND_STATUS,
		},
	}

	for _, tc := range tt {
		name := fmt.Sprintf("reqType:%v - shouldCall:%v - returnErr:%v", tc.reqType, tc.callsCache, tc.errToReturn)
		t.Run(name, func(t *testing.T) {
			cache := testStatusCache{
				answer: tc.answer,
				err:    tc.errToReturn,
			}
			worker := ultraviolet.BackendWorker{
				ServerState: ultraviolet.AlwaysOnlineState{},
				ConnCreator: testConnCreator{},
				StatusCache: &cache,
			}
			req := ultraviolet.BackendRequest{
				Type: tc.reqType,
			}
			answer := processRequest_TestTimeout(t, worker, req)
			if answer.Action() != tc.expectedAction {
				t.Errorf("expected %v but got %v instead", tc.expectedAction, answer.Action())
			}
			if cache.hasBeenCalled != tc.callsCache {
				t.Error("Expected cache to be called but wasnt")
			}
		})
	}
}

func TestBackendWorker_Update(t *testing.T) {
	t.Run("change when update config contains new value", func(t *testing.T) {
		worker := ultraviolet.BackendWorker{}
		cfg := ultraviolet.BackendConfig{
			Name:                "UV",
			UpdateProxyProtocol: true,
			SendProxyProtocol:   true,
			DisconnectPacket:   mc.Packet{ID: 0x45, Data: []byte{0x11, 0x12, 0x13}},
			OfflineStatusPacket:       mc.Packet{ID: 0x45, Data: []byte{0x11, 0x12, 0x14}},
			HsModifier:          &testHandshakeModifier{},
			ConnCreator:         &stateConnCreator{},
			ConnLimiter:         &testConnectionLimiter{},
			ServerState:         &testServerState{},
			StatusCache:         &testStatusCache{},
		}
		worker.UpdateSameGoroutine(cfg)
		if worker.Name != cfg.Name {
			t.Errorf("expected: %v - got: %v", cfg.Name, worker.Name)
		}
		if worker.SendProxyProtocol != cfg.SendProxyProtocol {
			t.Errorf("expected: %v - got: %v", cfg.SendProxyProtocol, worker.SendProxyProtocol)
		}
		if !samePk(worker.DisconnectPacket, cfg.DisconnectPacket) {
			t.Errorf("expected: %v - got: %v", cfg.DisconnectPacket, worker.DisconnectPacket)
		}
		if !samePk(worker.OfflineStatusPacket, cfg.OfflineStatusPacket) {
			t.Errorf("expected: %v - got: %v", cfg.OfflineStatusPacket, worker.OfflineStatusPacket)
		}
		if worker.HsModifier != cfg.HsModifier {
			t.Errorf("expected: %v - got: %v", cfg.HsModifier, worker.HsModifier)
		}
		if worker.ConnCreator != cfg.ConnCreator {
			t.Errorf("expected: %v - got: %v", cfg.ConnCreator, worker.ConnCreator)
		}
		if worker.ConnLimiter != cfg.ConnLimiter {
			t.Errorf("expected: %v - got: %v", cfg.ConnLimiter, worker.ConnLimiter)
		}
		if worker.StatusCache != cfg.StatusCache {
			t.Errorf("expected: %v - got: %v", cfg.StatusCache, worker.StatusCache)
		}
		if worker.ServerState != cfg.ServerState {
			t.Errorf("expected: %v - got: %v", cfg.ServerState, worker.ServerState)
		}
	})

	t.Run("change when update config contains new value", func(t *testing.T) {
		worker := ultraviolet.BackendWorker{
			Name:                     "UV",
			SendProxyProtocol:        true,
			DisconnectPacket: mc.Packet{ID: 0x45, Data: []byte{0x11, 0x12, 0x13}},
			OfflineStatusPacket:            mc.Packet{ID: 0x45, Data: []byte{0x11, 0x12, 0x14}},
			HsModifier:               &testHandshakeModifier{},
			ConnCreator:              &stateConnCreator{},
			ConnLimiter:              &testConnectionLimiter{},
			ServerState:              &testServerState{},
			StatusCache:              &testStatusCache{},
		}
		cfg := ultraviolet.BackendConfig{}
		worker.UpdateSameGoroutine(cfg)
		if worker.Name == cfg.Name {
			t.Errorf("didnt expect: %v", worker.Name)
		}
		if worker.SendProxyProtocol == cfg.SendProxyProtocol {
			t.Errorf("didnt expect: %v", worker.SendProxyProtocol)
		}
		if samePk(worker.DisconnectPacket, cfg.DisconnectPacket) {
			t.Errorf("didnt expect: %v", worker.DisconnectPacket)
		}
		if samePk(worker.OfflineStatusPacket, cfg.OfflineStatusPacket) {
			t.Errorf("didnt expect: %v", worker.OfflineStatusPacket)
		}
		if worker.HsModifier == cfg.HsModifier {
			t.Errorf("didnt expect: %v", worker.HsModifier)
		}
		if worker.ConnCreator == cfg.ConnCreator {
			t.Errorf("didnt expect: %v", worker.ConnCreator)
		}
		if worker.ConnLimiter == cfg.ConnLimiter {
			t.Errorf("didnt expect: %v", worker.ConnLimiter)
		}
		if worker.StatusCache == cfg.StatusCache {
			t.Errorf("didnt expect: %v", worker.StatusCache)
		}
		if worker.ServerState == cfg.ServerState {
			t.Errorf("didnt expect: %v", worker.ServerState)
		}
	})

	t.Run("can update while running", func(t *testing.T) {
		worker := ultraviolet.NewEmptyBackendWorker()
		worker.ServerState = ultraviolet.AlwaysOfflineState{}
		reqCh := worker.ReqCh()
		worker.Run()
		testPk := mc.ClientBoundDisconnect{
			Reason: mc.String("some text here"),
		}.Marshal()
		cfg := ultraviolet.BackendConfig{
			DisconnectPacket: testPk,
		}
		err := worker.Update(cfg)
		if err != nil {
			t.Fatalf("got error: %v", err)
		}
		ansCh := make(chan ultraviolet.ProcessAnswer)
		reqCh <- ultraviolet.BackendRequest{
			Type: mc.LOGIN,
			Ch:   ansCh,
		}
		ans := <-ansCh

		if !samePk(testPk, ans.Response()) {
			t.Errorf("expected: %v - got: %v", testPk, ans.Response())
		}
		worker.Close()
	})
}

func TestBackendFactory(t *testing.T) {
	t.Run("lets the worker run", func(t *testing.T) {
		cfg := config.BackendWorkerConfig{}
		backend, err := ultraviolet.BackendFactory(cfg)
		if err != nil {
			t.Fatalf("got error: %v", err)
		}
		reqCh := backend.ReqCh()
		req := ultraviolet.BackendRequest{}
		select {
		case reqCh <- req:
			t.Log("backend is running in different goroutine")
		case <-time.After(defaultChTimeout):
			t.Error("backend isnt running in different goroutine...?")
		}
	})

	t.Run("doesnt share data between goroutines", func(t *testing.T) {
		msg := "some text here"
		disconPk := mc.ClientBoundDisconnect{
			Reason: mc.String(msg),
		}.Marshal()

		cfg := config.BackendWorkerConfig{
			DisconnectPacket: disconPk,
			StateOption:      config.ALWAYS_OFFLINE,
		}

		backend, err := ultraviolet.BackendFactory(cfg)
		if err != nil {
			t.Fatalf("got error: %v", err)
		}

		worker, ok := backend.(*ultraviolet.BackendWorker)
		if !ok {
			t.Fatalf("backend is different type then expected")
		}
		newCfg := ultraviolet.BackendConfig{
			DisconnectPacket: mc.Packet{ID: 0x44, Data: []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}},
		}
		worker.UpdateSameGoroutine(newCfg)

		reqCh := backend.ReqCh()
		req := ultraviolet.BackendRequest{
			Ch:   make(chan ultraviolet.ProcessAnswer),
			Type: mc.LOGIN,
		}
		reqCh <- req
		ans := <-req.Ch

		if !samePK(disconPk, ans.Response()) {
			t.Error("packets werent the same which might mean its sharing memory")
			t.Logf("expected: %v", disconPk)
			t.Logf("got: %v", ans.Response())
		}
	})

}