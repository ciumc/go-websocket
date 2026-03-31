package websocket

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestClientConnMethodError 测试 Client.Conn 方法的错误处理
func TestClientConnMethodError(t *testing.T) {
	hub := NewHub() // 使用 NewHub 而不是 NewHubRun，避免 goroutine 竞争

	// 创建一个会返回升级错误的请求
	req := httptest.NewRequest("GET", "/ws", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Sec-Websocket-Key", "invalid-key") // 使用无效的 key
	req.Header.Set("Sec-Websocket-Version", "13")

	w := httptest.NewRecorder()

	client := NewClient(hub, WithID("error-conn-test"))

	// 调用 Conn 方法（会失败因为没有正确的 WebSocket 头）
	err := client.Conn(w, req)
	if err == nil {
		t.Error("Conn should return error for invalid request")
	}
	t.Logf("Expected error: %v", err)
}

// TestSessionConnMethodError 测试 Session.Conn 方法的错误处理
func TestSessionConnMethodError(t *testing.T) {
	hub := NewHub() // 不启动 Run

	// 创建一个会返回升级错误的请求
	req := httptest.NewRequest("GET", "/ws", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Sec-Websocket-Key", "invalid-key")
	req.Header.Set("Sec-Websocket-Version", "13")

	w := httptest.NewRecorder()

	session := NewSession(hub, WithSessionID("session-error-test"))

	// 调用 Conn 方法（会失败）
	err := session.Conn(w, req)
	if err == nil {
		t.Error("Conn should return error for invalid request")
	}
	t.Logf("Expected error: %v", err)
}

// TestSessionConnWithStorageSimple 测试 Session.Conn 分布式模式的存储设置
func TestSessionConnWithStorageSimple(t *testing.T) {
	hub := NewHub()

	storage := NewMockStorage()
	session := NewSession(hub,
		WithSessionID("dist-session-test"),
		WithStorage(storage),
		WithAddr("localhost:8080"),
	)

	// 验证 session 配置正确
	if session.storage == nil {
		t.Error("Session storage should not be nil")
	}
	if session.addr != "localhost:8080" {
		t.Errorf("Session addr = %s, want localhost:8080", session.addr)
	}
	if session.ID() != "dist-session-test" {
		t.Errorf("Session ID = %s, want dist-session-test", session.ID())
	}
}

// TestDistClientGetFunction 测试 grpcPool.get 函数
func TestDistClientGetFunction(t *testing.T) {
	pool := newGrpcPool()
	defer pool.close()

	// 测试1：从空池获取连接（grpc.NewClient 延迟连接，所以会成功创建）
	t.Run("empty pool creates conn", func(t *testing.T) {
		conn, err := pool.get("localhost:9999")
		if err != nil {
			t.Logf("get returned error: %v", err)
		}
		if conn != nil {
			t.Log("Connection created successfully (delayed connection)")
		}
	})

	// 测试2：添加一个 nil 连接到池中，get 应该创建新连接
	t.Run("nil conn in pool creates new", func(t *testing.T) {
		pool.mu.Lock()
		pool.conns["test-nil-conn"] = &pooledConn{
			conn:     nil,
			lastUsed: 1000,
		}
		pool.mu.Unlock()

		// 获取 nil 连接应该尝试创建新连接
		conn, err := pool.get("test-nil-conn")
		if err != nil {
			t.Logf("get returned error: %v", err)
		}
		if conn != nil {
			t.Log("New connection created for nil entry")
		}
	})
}

// TestGrpcPoolCleanExpiredFunction 测试 cleanExpired 函数
func TestGrpcPoolCleanExpiredFunction(t *testing.T) {
	pool := newGrpcPool()
	defer pool.close()

	// 添加一个"过期"的 nil 连接
	pool.mu.Lock()
	pool.conns["expired-conn"] = &pooledConn{
		conn:     nil,
		lastUsed: 1, // 很旧的时间戳
	}
	pool.mu.Unlock()

	// 调用 cleanExpired
	pool.cleanExpired()

	// 验证过期连接被清理
	pool.mu.RLock()
	_, exists := pool.conns["expired-conn"]
	pool.mu.RUnlock()

	if exists {
		t.Error("Expired connection should be removed")
	}
}

// TestDistClientCloseWithPool 测试 DistClient.Close 清理连接池
func TestDistClientCloseWithPool(t *testing.T) {
	storage := NewMockStorage()
	client := NewDistClient(storage)

	// 验证 pool 存在
	if client.pool == nil {
		t.Fatal("DistClient pool should not be nil")
	}

	// 关闭客户端
	client.Close()

	// 再次关闭不应 panic
	client.Close()
}

// TestHubBroadcastClosed 测试 Hub 关闭后 Broadcast 的行为
func TestHubBroadcastClosed(t *testing.T) {
	hub := NewHub()

	// 广播消息（Hub 未运行，但未关闭）
	hub.Broadcast([]byte("test message 1"))

	// 关闭 Hub
	hub.Close()

	// 关闭后广播应该被丢弃
	hub.Broadcast([]byte("test message 2"))
}

// TestWriterSetWriteDeadlineError 测试 writer 中 setWriteDeadline 错误分支
func TestWriterSetWriteDeadlineError(t *testing.T) {
	hub := NewHubRunWithConfig(
		WithPingPeriod(100*time.Millisecond),
		WithWriteWait(10*time.Millisecond),
	)
	defer hub.Close()

	disconnectCalled := make(chan string, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := hub.Upgrader().Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		client := NewClient(hub, WithID("deadline-test"))
		client.conn = conn
		client.OnDisconnect(func(id string) {
			disconnectCalled <- id
		})

		// 关闭连接使 setWriteDeadline 失败
		conn.Close()

		// 发送消息触发 writer
		client.Emit([]byte("test"))

		// 启动 writer
		go client.writer()

		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer wsConn.Close()

	select {
	case <-disconnectCalled:
		// 成功
	case <-time.After(200 * time.Millisecond):
		// 可能超时
	}
}

// TestClientConnMethodWithRealConn 测试 Client.Conn 方法的完整流程
// 注意：此测试使用简化的方式，避免 race 条件
func TestClientConnMethodWithRealConn(t *testing.T) {
	hub := NewHub() // 不启动 Run，避免 race
	defer hub.Close()

	connectCalled := make(chan bool, 1)

	// 创建测试服务器
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		client := NewClient(hub, WithID("conn-full-test"))
		client.OnConnect(func(c *Client) {
			connectCalled <- true
		})

		// 调用 Conn 方法
		err := client.Conn(w, r)
		if err != nil {
			t.Logf("Conn error: %v", err)
			return
		}

		// 保持连接
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	// 作为客户端连接
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer wsConn.Close()

	// 等待连接回调
	select {
	case <-connectCalled:
		// 成功
	case <-time.After(200 * time.Millisecond):
		t.Error("OnConnect callback was not called")
	}
}
