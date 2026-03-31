package websocket

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestHubBroadcastBlocking 测试 Hub.Broadcast 阻塞情况
func TestHubBroadcastBlocking(t *testing.T) {
	hub := NewHubRunWithConfig(WithHubBufSize(10))
	defer hub.Close()

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

	// 创建多个客户端
	for i := 0; i < 5; i++ {
		wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			t.Logf("Dial error: %v", err)
			continue
		}

		client := NewClient(hub, WithID(string(rune('A'+i))))
		client.conn = wsConn
		hub.register <- client
	}

	time.Sleep(50 * time.Millisecond)

	// 广播消息
	for i := 0; i < 10; i++ {
		hub.Broadcast([]byte("test message"))
	}

	time.Sleep(50 * time.Millisecond)
}

// TestHubBroadcastAfterClose 测试关闭后广播
func TestHubBroadcastAfterClose(t *testing.T) {
	hub := NewHubRun()

	// 广播消息
	hub.Broadcast([]byte("before close"))

	time.Sleep(10 * time.Millisecond)

	// 关闭 Hub
	hub.Close()

	// 关闭后广播应该被忽略（不会 panic）
	hub.Broadcast([]byte("after close"))
}

// TestHubMultipleClose 测试多次关闭
func TestHubMultipleClose(t *testing.T) {
	hub := NewHubRun()
	time.Sleep(10 * time.Millisecond)

	// 多次关闭不应 panic
	hub.Close()
	hub.Close()
	hub.Close()
}

// TestClientConnWithHubUpgrader 测试 Client 使用 Hub 的 Upgrader
func TestClientConnWithHubUpgrader(t *testing.T) {
	customCheck := func(r *http.Request) bool {
		return r.Header.Get("X-Auth") == "secret"
	}

	hub := NewHubRunWithConfig(WithHubCheckOrigin(customCheck))
	defer hub.Close()

	// 验证 upgrader 使用了自定义 CheckOrigin
	req := httptest.NewRequest("GET", "http://example.com/ws", nil)

	// 无认证头
	if hub.Upgrader().CheckOrigin(req) {
		t.Error("CheckOrigin should return false without auth header")
	}

	// 有认证头
	req.Header.Set("X-Auth", "secret")
	if !hub.Upgrader().CheckOrigin(req) {
		t.Error("CheckOrigin should return true with correct auth header")
	}
}

// TestSessionWithAllOptions 测试 Session 使用所有选项
func TestSessionWithAllOptions(t *testing.T) {
	hub := NewHubRun()
	defer hub.Close()

	session := NewSession(hub,
		WithSessionID("all-options-test"),
		WithSessionBufSize(1024),
		WithSessionWriteWait(5*time.Second),
		WithSessionPongWait(30*time.Second),
		WithSessionPingPeriod(20*time.Second),
		WithSessionMaxMessageSize(2048),
	)

	// 验证 ID
	if session.ID() != "all-options-test" {
		t.Errorf("ID = %v, want all-options-test", session.ID())
	}

	// 验证配置
	cfg := session.config
	if cfg.BufSize != 1024 {
		t.Errorf("BufSize = %v, want 1024", cfg.BufSize)
	}
	if cfg.WriteWait != 5*time.Second {
		t.Errorf("WriteWait = %v, want 5s", cfg.WriteWait)
	}
	if cfg.PongWait != 30*time.Second {
		t.Errorf("PongWait = %v, want 30s", cfg.PongWait)
	}
	if cfg.PingPeriod != 20*time.Second {
		t.Errorf("PingPeriod = %v, want 20s", cfg.PingPeriod)
	}
	if cfg.MaxMessageSize != 2048 {
		t.Errorf("MaxMessageSize = %v, want 2048", cfg.MaxMessageSize)
	}
}

// TestNewDistSessionCompatibility 测试向后兼容的 NewDistSession
func TestNewDistSessionCompatibility(t *testing.T) {
	hub := NewHubRun()
	defer hub.Close()

	storage := NewMockStorage()

	// 使用旧 API
	session := NewDistSession(hub, storage, "192.168.1.1:8080", WithID("compat-test"))

	// 验证返回的是 Session
	if session == nil {
		t.Fatal("NewDistSession returned nil")
	}

	// 验证 storage 和 addr 已设置
	if session.storage != storage {
		t.Error("Storage not set correctly")
	}
	if session.addr != "192.168.1.1:8080" {
		t.Errorf("Addr = %v, want 192.168.1.1:8080", session.addr)
	}
}
