package websocket

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestNewHubWithConfig 测试带配置创建 Hub
func TestNewHubWithConfig(t *testing.T) {
	opts := []HubOption{
		WithWriteWait(5 * time.Second),
		WithPongWait(30 * time.Second),
		WithPingPeriod(20 * time.Second),
		WithMaxMessageSize(1024),
		WithHubBufSize(512),
	}
	hub := NewHubWithConfig(opts...)
	defer hub.Close()

	cfg := hub.Config()
	if cfg.WriteWait != 5*time.Second {
		t.Errorf("WriteWait = %v, want 5s", cfg.WriteWait)
	}
	if cfg.PongWait != 30*time.Second {
		t.Errorf("PongWait = %v, want 30s", cfg.PongWait)
	}
	if cfg.PingPeriod != 20*time.Second {
		t.Errorf("PingPeriod = %v, want 20s", cfg.PingPeriod)
	}
	if cfg.MaxMessageSize != 1024 {
		t.Errorf("MaxMessageSize = %v, want 1024", cfg.MaxMessageSize)
	}
	if cfg.BufSize != 512 {
		t.Errorf("BufSize = %v, want 512", cfg.BufSize)
	}
}

// TestNewHubRunWithConfig 测试带配置创建并运行 Hub
func TestNewHubRunWithConfig(t *testing.T) {
	hub := NewHubRunWithConfig(WithWriteWait(3 * time.Second))
	defer hub.Close()

	// 给 Hub 时间启动
	time.Sleep(10 * time.Millisecond)

	cfg := hub.Config()
	if cfg.WriteWait != 3*time.Second {
		t.Errorf("WriteWait = %v, want 3s", cfg.WriteWait)
	}
}

// TestHubUpgrader 测试 Hub 内置 upgrader
func TestHubUpgrader(t *testing.T) {
	// 测试默认 CheckOrigin
	hub := NewHubWithConfig()
	defer hub.Close()

	upgrader := hub.Upgrader()
	if upgrader == nil {
		t.Fatal("Upgrader is nil")
	}

	req := httptest.NewRequest("GET", "http://example.com/ws", nil)
	if !upgrader.CheckOrigin(req) {
		t.Error("Default CheckOrigin should return true")
	}
}

// TestHubUpgraderCustomOrigin 测试自定义 CheckOrigin
func TestHubUpgraderCustomOrigin(t *testing.T) {
	customCheck := func(r *http.Request) bool {
		return r.Header.Get("Origin") == "https://trusted.com"
	}

	hub := NewHubWithConfig(WithHubCheckOrigin(customCheck))
	defer hub.Close()

	upgrader := hub.Upgrader()

	// 测试受信任来源
	trustedReq := httptest.NewRequest("GET", "https://trusted.com/ws", nil)
	trustedReq.Header.Set("Origin", "https://trusted.com")
	if !upgrader.CheckOrigin(trustedReq) {
		t.Error("CheckOrigin should return true for trusted origin")
	}

	// 测试不受信任来源
	untrustedReq := httptest.NewRequest("GET", "https://untrusted.com/ws", nil)
	untrustedReq.Header.Set("Origin", "https://untrusted.com")
	if upgrader.CheckOrigin(untrustedReq) {
		t.Error("CheckOrigin should return false for untrusted origin")
	}
}

// TestHubConfigReturnsCopy 测试 Config 返回副本
func TestHubConfigReturnsCopy(t *testing.T) {
	hub := NewHubWithConfig(WithWriteWait(5 * time.Second))
	defer hub.Close()

	cfg1 := hub.Config()
	cfg2 := hub.Config()

	// 修改 cfg1 不应影响 cfg2
	cfg1.WriteWait = 10 * time.Second

	if cfg2.WriteWait == 10*time.Second {
		t.Error("Config() should return a copy")
	}
}
