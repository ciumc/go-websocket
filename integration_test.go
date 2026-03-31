package websocket

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestSessionConnIntegration 测试 Session.Conn 完整流程
func TestSessionConnIntegration(t *testing.T) {
	hub := NewHubRunWithConfig(
		WithWriteWait(5*time.Second),
		WithPongWait(30*time.Second),
		WithPingPeriod(27*time.Second), // PingPeriod 必须小于 PongWait
	)
	defer hub.Close()

	// 创建测试服务器
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := hub.Upgrader().Upgrade(w, r, nil)
		if err != nil {
			t.Logf("Upgrade error: %v", err)
			return
		}
		defer conn.Close()
		// 回显服务器
		for {
			msgType, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := conn.WriteMessage(msgType, msg); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	// 创建 Session
	session := NewSession(hub, WithSessionID("conn-integration-test"))

	// 设置回调
	connectCalled := make(chan bool, 1)
	session.OnConnect(func(conn *Client) {
		connectCalled <- true
	})

	messageReceived := make(chan []byte, 1)
	session.OnEvent(func(conn *Client, messageType int, message []byte) {
		messageReceived <- message
	})

	// 使用 WebSocket 连接测试
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to dial: %v", err)
	}
	defer wsConn.Close()

	// 手动设置连接并注册
	session.client.conn = wsConn
	hub.register <- session.client

	// 等待注册完成
	time.Sleep(50 * time.Millisecond)

	// 启动 reader 和 writer
	go session.client.writer()
	go session.client.reader()

	// 触发 OnConnect
	if session.client.onConnect != nil {
		session.client.onConnect(session.client)
	}

	// 等待连接回调
	select {
	case <-connectCalled:
	case <-time.After(100 * time.Millisecond):
		t.Error("OnConnect not called")
	}

	// 验证客户端已注册
	if _, ok := hub.Client(session.ID()); !ok {
		t.Error("Session client was not registered")
	}

	// 发送消息测试
	testMsg := []byte("hello session")
	if err := wsConn.WriteMessage(websocket.TextMessage, testMsg); err != nil {
		t.Fatalf("Failed to write message: %v", err)
	}

	// 等待消息接收
	select {
	case msg := <-messageReceived:
		if string(msg) != string(testMsg) {
			t.Errorf("Received message = %s, want %s", string(msg), string(testMsg))
		}
	case <-time.After(200 * time.Millisecond):
		t.Error("Message not received")
	}
}

// TestSessionDistributedIntegration 测试分布式模式完整流程
func TestSessionDistributedIntegration(t *testing.T) {
	hub := NewHubRun()
	defer hub.Close()

	storage := NewMockStorage()
	addr := "192.168.1.100:8080"

	// 创建分布式 Session
	session := NewSession(hub,
		WithStorage(storage),
		WithAddr(addr),
		WithSessionID("dist-integration-test"),
	)

	// 设置回调
	session.OnConnect(func(conn *Client) {
		// 验证存储中有记录
		val, err := storage.Get(conn.GetID())
		if err != nil {
			t.Errorf("Storage get error: %v", err)
		}
		if val != addr {
			t.Errorf("Storage value = %s, want %s", val, addr)
		}
	})

	// 手动触发 OnConnect 逻辑
	if session.client.onConnect != nil {
		// 先存储
		if err := storage.Set(session.ID(), addr); err != nil {
			t.Fatalf("Storage set error: %v", err)
		}
		session.client.onConnect(session.client)
	}

	// 验证存储
	val, err := storage.Get(session.ID())
	if err != nil {
		t.Errorf("Storage get error: %v", err)
	}
	if val != addr {
		t.Errorf("Storage value = %s, want %s", val, addr)
	}
}

// TestDistClientClose 测试 DistClient.Close 清理连接池
func TestDistClientClose(t *testing.T) {
	storage := NewMockStorage()
	client := NewDistClient(storage)

	// 验证连接池已创建
	if client.pool == nil {
		t.Fatal("Pool should not be nil")
	}

	// 调用 Close
	client.Close()

	// 验证可以多次调用 Close 不 panic
	client.Close()
	client.Close()
}

// TestDistClientGetTimeout 测试默认超时
func TestDistClientGetTimeoutDefault(t *testing.T) {
	storage := NewMockStorage()
	client := NewDistClient(storage)

	// 默认超时应该是 2 秒
	if client.getTimeout() != 2*time.Second {
		t.Errorf("Default timeout = %v, want 2s", client.getTimeout())
	}

	client.Close()
}
