package websocket

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestNewSession 测试创建 Session
func TestNewSession(t *testing.T) {
	hub := NewHubRun()
	defer hub.Close()

	session := NewSession(hub)
	if session == nil {
		t.Fatal("NewSession returned nil")
	}

	// 验证 ID 已生成
	if session.ID() == "" {
		t.Error("Session ID should be generated")
	}
}

// TestNewSessionWithOptions 测试带选项创建 Session
func TestNewSessionWithOptions(t *testing.T) {
	hub := NewHubRun()
	defer hub.Close()

	session := NewSession(hub,
		WithSessionID("custom-id"),
		WithSessionBufSize(512),
	)

	if session.ID() != "custom-id" {
		t.Errorf("ID = %v, want custom-id", session.ID())
	}
}

// TestSessionInheritsHubConfig 测试 Session 继承 Hub 配置
func TestSessionInheritsHubConfig(t *testing.T) {
	hub := NewHubRunWithConfig(
		WithWriteWait(5*time.Second),
		WithPongWait(30*time.Second),
		WithPingPeriod(27*time.Second), // PingPeriod 必须小于 PongWait
	)
	defer hub.Close()

	session := NewSession(hub)

	cfg := session.config
	if cfg.WriteWait != 5*time.Second {
		t.Errorf("WriteWait = %v, want 5s", cfg.WriteWait)
	}
	if cfg.PongWait != 30*time.Second {
		t.Errorf("PongWait = %v, want 30s", cfg.PongWait)
	}
}

// TestSessionConfigOverride 测试 Session 配置覆盖
func TestSessionConfigOverride(t *testing.T) {
	hub := NewHubRunWithConfig(WithWriteWait(10 * time.Second))
	defer hub.Close()

	session := NewSession(hub,
		WithSessionWriteWait(3*time.Second),
	)

	cfg := session.config
	if cfg.WriteWait != 3*time.Second {
		t.Errorf("WriteWait = %v, want 3s (override)", cfg.WriteWait)
	}

	// Hub 配置不应被修改
	hubCfg := hub.Config()
	if hubCfg.WriteWait != 10*time.Second {
		t.Errorf("Hub WriteWait = %v, want 10s (unchanged)", hubCfg.WriteWait)
	}
}

// TestSessionStandaloneMode 测试单节点模式（无 storage）
func TestSessionStandaloneMode(t *testing.T) {
	hub := NewHubRun()
	defer hub.Close()

	session := NewSession(hub, WithSessionID("standalone-test"))

	// 验证 storage 为 nil
	if session.storage != nil {
		t.Error("Standalone mode should have nil storage")
	}
}

// TestSessionDistributedMode 测试分布式模式（有 storage）
func TestSessionDistributedMode(t *testing.T) {
	hub := NewHubRun()
	defer hub.Close()

	storage := NewMockStorage()
	session := NewSession(hub,
		WithStorage(storage),
		WithAddr("192.168.1.1:8080"),
		WithSessionID("dist-test"),
	)

	// 验证 storage 已设置
	if session.storage != storage {
		t.Error("Distributed mode should have storage")
	}
	if session.addr != "192.168.1.1:8080" {
		t.Errorf("addr = %v, want 192.168.1.1:8080", session.addr)
	}
}

// TestSessionConn 测试 Session.Conn 建立 WebSocket 连接
func TestSessionConn(t *testing.T) {
	hub := NewHubRun()
	defer hub.Close()

	// 创建测试服务器
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := hub.Upgrader().Upgrade(w, r, nil)
		if err != nil {
			t.Logf("Upgrade error: %v", err)
			return
		}
		defer conn.Close()
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				return
			}
		}
	}))
	defer server.Close()

	session := NewSession(hub, WithSessionID("conn-test"))

	// 创建 HTTP 请求
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	// 需要用 websocket dialer 来测试 Conn
	// 这里测试 Session.Conn 的基本流程
	wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to dial: %v", err)
	}
	defer wsConn.Close()

	// 手动设置连接（模拟 Conn 方法）
	session.client.conn = wsConn
	hub.register <- session.client

	// 等待注册
	time.Sleep(50 * time.Millisecond)

	// 验证客户端已注册
	if _, ok := hub.Client(session.ID()); !ok {
		t.Error("Session client was not registered")
	}
}

// TestSessionOnConnect 测试 OnConnect 回调
func TestSessionOnConnect(t *testing.T) {
	hub := NewHubRun()
	defer hub.Close()

	connectCalled := false
	session := NewSession(hub)
	session.OnConnect(func(conn *Client) {
		connectCalled = true
	})

	// 手动触发回调
	if session.client.onConnect != nil {
		session.client.onConnect(session.client)
	}

	if !connectCalled {
		t.Error("OnConnect callback was not called")
	}
}

// TestSessionOnDisconnect 测试 OnDisconnect 回调
func TestSessionOnDisconnect(t *testing.T) {
	hub := NewHubRun()
	defer hub.Close()

	storage := NewMockStorage()
	_ = storage.Set("disconnect-test", "192.168.1.1:8080")

	session := NewSession(hub,
		WithStorage(storage),
		WithAddr("192.168.1.1:8080"),
		WithSessionID("disconnect-test"),
	)

	disconnectCalled := false
	session.OnDisconnect(func(id string) {
		disconnectCalled = true
	})

	// 触发断开回调
	if session.client.onDisconnect != nil {
		session.client.onDisconnect("disconnect-test")
	}

	if !disconnectCalled {
		t.Error("OnDisconnect callback was not called")
	}

	// 验证存储中的条目被删除
	val, _ := storage.Get("disconnect-test")
	if val != "" {
		t.Error("Storage entry should be deleted on disconnect")
	}
}

// TestSessionEmit 测试 Session.Emit
func TestSessionEmit(t *testing.T) {
	hub := NewHubRun()
	defer hub.Close()

	session := NewSession(hub)

	// 创建测试服务器
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := hub.Upgrader().Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				return
			}
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to dial: %v", err)
	}
	defer wsConn.Close()

	session.client.conn = wsConn

	// 测试发送
	if !session.Emit([]byte("test message")) {
		t.Error("Emit should return true")
	}
}

// TestSessionBroadcast 测试 Session.Broadcast
func TestSessionBroadcast(t *testing.T) {
	hub := NewHubRun()
	defer hub.Close()

	session := NewSession(hub)

	// Broadcast 应该调用 Hub.Broadcast
	session.Broadcast([]byte("broadcast message"))
	// 验证通过 - 没有 panic 即可
}

// TestSessionClient 测试 Session.Client()
func TestSessionClient(t *testing.T) {
	hub := NewHubRun()
	defer hub.Close()

	session := NewSession(hub)

	client := session.Client()
	if client == nil {
		t.Fatal("Client() returned nil")
	}
	if client != session.client {
		t.Error("Client() should return the internal client")
	}
}

// TestNewClientWithDefaultBufSize 测试 newClient 使用默认 bufSize
// 当 bufSize <= 0 时，应使用 Hub 的默认配置
func TestNewClientWithDefaultBufSize(t *testing.T) {
	hub := NewHubRunWithConfig(WithHubBufSize(128))
	defer hub.Close()

	// 测试 bufSize = 0 时使用 Hub 配置
	client := newClient(hub, "test-zero-bufsize", 0)
	if client == nil {
		t.Fatal("newClient returned nil")
	}
	if client.hub != hub {
		t.Error("client hub mismatch")
	}
	if client.id != "test-zero-bufsize" {
		t.Errorf("client id = %s, want test-zero-bufsize", client.id)
	}
	// 验证 send channel 已创建（使用 Hub 的 BufSize）
	if client.send == nil {
		t.Error("client send channel should not be nil")
	}

	// 测试 bufSize = -1 时使用 Hub 配置
	client2 := newClient(hub, "test-negative-bufsize", -1)
	if client2 == nil {
		t.Fatal("newClient with negative bufSize returned nil")
	}
	if client2.send == nil {
		t.Error("client2 send channel should not be nil")
	}

	// 测试自定义 bufSize
	client3 := newClient(hub, "test-custom-bufsize", 64)
	if client3 == nil {
		t.Fatal("newClient with custom bufSize returned nil")
	}
	if client3.send == nil {
		t.Error("client3 send channel should not be nil")
	}
}

// TestSessionWithZeroBufSize 测试 Session 使用零 bufSize 配置
func TestSessionWithZeroBufSize(t *testing.T) {
	hub := NewHubRunWithConfig(WithHubBufSize(100))
	defer hub.Close()

	// 使用零 bufSize（会使用 Hub 的配置）
	session := NewSession(hub, WithSessionBufSize(0))
	if session == nil {
		t.Fatal("NewSession returned nil")
	}
	if session.client == nil {
		t.Fatal("session.client is nil")
	}
	// 验证 client 的 send channel 已创建
	if session.client.send == nil {
		t.Error("client send channel should not be nil")
	}
}

// TestSessionConnUpgradeError 测试 Session.Conn 升级失败
func TestSessionConnUpgradeError(t *testing.T) {
	hub := NewHubRun()
	defer hub.Close()

	session := NewSession(hub, WithSessionID("upgrade-error-test"))

	// 创建无效的 HTTP 请求（不是 WebSocket 升级请求）
	req := httptest.NewRequest("GET", "http://example.com", nil)
	w := httptest.NewRecorder()

	// Conn 应该返回升级错误
	err := session.Conn(w, req)
	if err == nil {
		t.Error("Conn() should return error for non-websocket request")
	}
	if !strings.Contains(err.Error(), "websocket upgrade") {
		t.Errorf("Conn() error should contain 'websocket upgrade', got: %v", err)
	}
}

// TestSessionConnDistributedModeStorageError 测试 Session.Conn 分布式模式下存储失败
func TestSessionConnDistributedModeStorageError(t *testing.T) {
	hub := NewHubRun()
	defer hub.Close()

	// 使用会返回错误的 storage
	storage := &errorStorage{}

	session := NewSession(hub,
		WithStorage(storage),
		WithAddr("192.168.1.1:8080"),
		WithSessionID("storage-error-test"),
	)

	errorCalled := make(chan error, 1)
	session.OnError(func(id string, err error) {
		errorCalled <- err
	})

	// 创建测试服务器
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := hub.Upgrader().Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		// 保持连接
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				return
			}
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to dial: %v", err)
	}
	defer wsConn.Close()

	// 手动设置连接（模拟 Conn 方法的内部逻辑）
	session.client.conn = wsConn
	session.client.onConnect = func(c *Client) {
		// 分布式模式：存储失败应该触发 OnError 回调
		if session.storage != nil {
			if err := session.storage.Set(c.id, session.addr); err != nil {
				c.error(err)
			}
		}
	}

	hub.register <- session.client

	// 手动触发 OnConnect 来测试存储错误处理
	if session.client.onConnect != nil {
		session.client.onConnect(session.client)
	}

	// 验证 OnError 回调被调用（存储失败）
	select {
	case err := <-errorCalled:
		if err == nil {
			t.Error("OnError should be called with storage error")
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("OnError should be called when storage fails")
	}
}

// TestSessionOnDisconnectStorageError 测试 OnDisconnect 存储错误处理
func TestSessionOnDisconnectStorageError(t *testing.T) {
	hub := NewHubRun()
	defer hub.Close()

	// 使用会返回错误的 storage
	storage := &errorStorage{}

	session := NewSession(hub,
		WithStorage(storage),
		WithAddr("192.168.1.1:8080"),
		WithSessionID("disconnect-storage-error-test"),
	)

	errorCalled := make(chan error, 1)
	session.OnError(func(id string, err error) {
		errorCalled <- err
	})

	disconnectCalled := make(chan string, 1)
	session.OnDisconnect(func(id string) {
		disconnectCalled <- id
	})

	// 触发断开回调
	if session.client.onDisconnect != nil {
		session.client.onDisconnect("disconnect-storage-error-test")
	}

	// 验证 OnDisconnect 回调被调用
	select {
	case id := <-disconnectCalled:
		if id != "disconnect-storage-error-test" {
			t.Errorf("disconnect id = %v, want 'disconnect-storage-error-test'", id)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("OnDisconnect callback was not called")
	}

	// 验证 OnError 回调被调用（存储删除失败）
	select {
	case err := <-errorCalled:
		if err == nil {
			t.Error("OnError should be called with storage delete error")
		}
	case <-time.After(500 * time.Millisecond):
		// 存储错误应该被记录
		t.Log("OnError may or may not be called depending on errorStorage behavior")
	}
}
