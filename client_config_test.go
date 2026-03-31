package websocket

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestClientUsesHubConfig 测试 Client 使用 Hub 的配置
func TestClientUsesHubConfig(t *testing.T) {
	opts := []HubOption{
		WithPongWait(30 * time.Second),
		WithPingPeriod(20 * time.Second),
		WithMaxMessageSize(1024),
		WithHubBufSize(512),
	}
	hub := NewHubRunWithConfig(opts...)
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

	// 创建客户端
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to dial: %v", err)
	}
	defer wsConn.Close()

	client := &Client{
		hub:  hub,
		id:   "config-test-client",
		send: make(chan []byte, hub.Config().BufSize),
		conn: wsConn,
	}

	// 验证有效配置
	cfg := client.effectiveConfig()
	if cfg.PongWait != 30*time.Second {
		t.Errorf("PongWait = %v, want 30s", cfg.PongWait)
	}
	if cfg.PingPeriod != 20*time.Second {
		t.Errorf("PingPeriod = %v, want 20s", cfg.PingPeriod)
	}
	if cfg.MaxMessageSize != 1024 {
		t.Errorf("MaxMessageSize = %v, want 1024", cfg.MaxMessageSize)
	}
}

// TestClientConfigOverride 测试 Client 配置覆盖
func TestClientConfigOverride(t *testing.T) {
	hub := NewHubRunWithConfig(WithPongWait(60 * time.Second))
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
	wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to dial: %v", err)
	}
	defer wsConn.Close()

	// 创建带配置覆盖的客户端
	overrideConfig := hub.Config()
	overrideConfig.PongWait = 30 * time.Second

	client := &Client{
		hub:    hub,
		id:     "override-test-client",
		send:   make(chan []byte, 256),
		conn:   wsConn,
		config: &overrideConfig,
	}

	cfg := client.effectiveConfig()
	if cfg.PongWait != 30*time.Second {
		t.Errorf("PongWait = %v, want 30s (override)", cfg.PongWait)
	}

	// Hub 的配置不应被修改
	hubCfg := hub.Config()
	if hubCfg.PongWait != 60*time.Second {
		t.Errorf("Hub PongWait = %v, want 60s (unchanged)", hubCfg.PongWait)
	}
}

// TestClientEffectiveConfigFallback 测试无覆盖时使用 Hub 配置
func TestClientEffectiveConfigFallback(t *testing.T) {
	hub := NewHubRunWithConfig(WithWriteWait(5 * time.Second))
	defer hub.Close()

	client := &Client{
		hub:  hub,
		id:   "fallback-test-client",
		send: make(chan []byte, 256),
	}

	cfg := client.effectiveConfig()
	if cfg.WriteWait != 5*time.Second {
		t.Errorf("WriteWait = %v, want 5s (from Hub)", cfg.WriteWait)
	}
}
