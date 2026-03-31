package websocket

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestNewHub 测试构造函数，确认各 channel 和 map 正确初始化
func TestNewHub(t *testing.T) {
	h := NewHub()
	if h == nil {
		t.Fatal("NewHub() returned nil")
	}

	// 验证 clients map 已初始化
	if h.clients == nil {
		t.Error("NewHub() clients map is nil")
	}

	// 验证 broadcast channel 已初始化
	if h.broadcast == nil {
		t.Error("NewHub() broadcast channel is nil")
	}

	// 验证 register channel 已初始化
	if h.register == nil {
		t.Error("NewHub() register channel is nil")
	}

	// 验证 unregister channel 已初始化
	if h.unregister == nil {
		t.Error("NewHub() unregister channel is nil")
	}

	// 验证 done channel 已初始化
	if h.done == nil {
		t.Error("NewHub() done channel is nil")
	}
}

// TestNewHubRun 测试带自动运行的构造函数
func TestNewHubRun(t *testing.T) {
	h := NewHubRun()
	if h == nil {
		t.Fatal("NewHubRun() returned nil")
	}

	// 给一点时间让 goroutine 启动
	time.Sleep(10 * time.Millisecond)

	// 验证 Hub 已正确初始化
	if h.clients == nil {
		t.Error("NewHubRun() clients map is nil")
	}

	// 清理
	h.Close()
}

// TestHubRegisterUnregister 测试客户端注册和注销流程
func TestHubRegisterUnregister(t *testing.T) {
	h := NewHub()
	go h.Run()
	defer h.Close()

	// 创建一个测试服务器用于 WebSocket 连接
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Logf("Upgrade error: %v", err)
			return
		}
		// 保持连接打开直到测试结束
		defer conn.Close()
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				return
			}
		}
	}))
	defer server.Close()

	// 创建 WebSocket 连接
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to connect to WebSocket server: %v", err)
	}

	// 创建客户端
	client := &Client{
		hub:  h,
		id:   "test-client-1",
		send: make(chan []byte, 256),
		conn: conn,
	}

	// 注册客户端
	h.register <- client

	// 给一点时间处理
	time.Sleep(10 * time.Millisecond)

	// 验证客户端已注册
	if _, ok := h.Client(client.id); !ok {
		t.Error("Client was not registered")
	}

	// 注销客户端
	h.unregister <- client

	// 给一点时间处理
	time.Sleep(10 * time.Millisecond)

	// 验证客户端已注销
	if _, ok := h.Client(client.id); ok {
		t.Error("Client was not unregistered")
	}
}

// TestHubClient 测试 Client() 方法（存在/不存在的客户端）
func TestHubClient(t *testing.T) {
	h := NewHub()
	go h.Run()
	defer h.Close()

	// 测试获取不存在的客户端
	cli, ok := h.Client("nonexistent")
	if ok {
		t.Error("Client() should return false for nonexistent client")
	}
	if cli != nil {
		t.Error("Client() should return nil for nonexistent client")
	}

	// 创建测试服务器
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
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

	// 创建 WebSocket 连接
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to connect to WebSocket server: %v", err)
	}

	// 创建并注册一个客户端
	client := &Client{
		hub:  h,
		id:   "test-client-2",
		send: make(chan []byte, 256),
		conn: conn,
	}
	h.register <- client
	time.Sleep(10 * time.Millisecond)

	// 测试获取存在的客户端
	cli, ok = h.Client(client.id)
	if !ok {
		t.Error("Client() should return true for existing client")
	}
	if cli == nil {
		t.Error("Client() should return the client for existing client")
	}
	if cli.id != client.id {
		t.Errorf("Client() returned wrong client, got %v want %v", cli.id, client.id)
	}
}

// TestHubBroadcast 测试广播消息到多个客户端
func TestHubBroadcast(t *testing.T) {
	h := NewHub()
	go h.Run()
	defer h.Close()

	// 创建测试服务器
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
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

	// 创建两个客户端
	conn1, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	client1 := &Client{
		hub:  h,
		id:   "broadcast-client-1",
		send: make(chan []byte, 256),
		conn: conn1,
	}

	conn2, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	client2 := &Client{
		hub:  h,
		id:   "broadcast-client-2",
		send: make(chan []byte, 256),
		conn: conn2,
	}

	// 注册两个客户端
	h.register <- client1
	h.register <- client2
	time.Sleep(10 * time.Millisecond)

	// 广播消息
	message := []byte("hello broadcast")
	h.Broadcast(message)

	// 给一点时间处理
	time.Sleep(50 * time.Millisecond)

	// 验证两个客户端都收到了消息
	select {
	case msg := <-client1.send:
		if string(msg) != string(message) {
			t.Errorf("Client1 received wrong message: got %v want %v", string(msg), string(message))
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Client1 did not receive broadcast message")
	}

	select {
	case msg := <-client2.send:
		if string(msg) != string(message) {
			t.Errorf("Client2 received wrong message: got %v want %v", string(msg), string(message))
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Client2 did not receive broadcast message")
	}
}

// TestHubCloseIdempotent 测试 Close() 可以安全地多次调用而不 panic
func TestHubCloseIdempotent(t *testing.T) {
	h := NewHub()
	go h.Run()

	// 等待 Run 启动
	time.Sleep(10 * time.Millisecond)

	// 第一次关闭
	h.Close()

	// 给一点时间处理
	time.Sleep(10 * time.Millisecond)

	// 第二次关闭 - 不应该 panic
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Close() panicked on second call: %v", r)
		}
	}()
	h.Close()
}

// TestHubConcurrentAccess 100 goroutine 并发注册/注销/广播，验证无竞态条件
func TestHubConcurrentAccess(t *testing.T) {
	h := NewHub()
	go h.Run()
	defer h.Close()

	// 创建测试服务器
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
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

	var wg sync.WaitGroup
	numGoroutines := 100

	// 并发注册
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
			if err != nil {
				t.Logf("Dial error: %v", err)
				return
			}
			client := &Client{
				hub:  h,
				id:   "concurrent-client-" + string(rune('0'+i%10)),
				send: make(chan []byte, 256),
				conn: conn,
			}
			h.register <- client
		}(i)
	}

	// 并发查询
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "concurrent-client-" + string(rune('0'+i%10))
			h.Client(id)
		}(i)
	}

	// 并发广播
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			msg := []byte("message " + string(rune('0'+i%10)))
			h.Broadcast(msg)
		}(i)
	}

	// 并发注销
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
			if err != nil {
				t.Logf("Dial error: %v", err)
				return
			}
			client := &Client{
				hub:  h,
				id:   "concurrent-client-" + string(rune('0'+i%10)),
				send: make(chan []byte, 256),
				conn: conn,
			}
			h.unregister <- client
		}(i)
	}

	wg.Wait()
}

// TestHubRunExitCleanup 测试 Run() 退出时正确清理资源
func TestHubRunExitCleanup(t *testing.T) {
	h := NewHub()

	// 创建测试服务器
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
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

	// 创建并注册一些客户端
	conn1, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	client1 := &Client{
		hub:  h,
		id:   "cleanup-client-1",
		send: make(chan []byte, 256),
		conn: conn1,
	}

	conn2, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	client2 := &Client{
		hub:  h,
		id:   "cleanup-client-2",
		send: make(chan []byte, 256),
		conn: conn2,
	}

	// 手动添加到 clients map（模拟注册）
	h.clientsLock.Lock()
	h.clients[client1.id] = client1
	h.clients[client2.id] = client2
	h.clientsLock.Unlock()

	// 启动 Run
	done := make(chan struct{})
	go func() {
		h.Run()
		close(done)
	}()

	// 等待 Run 启动
	time.Sleep(10 * time.Millisecond)

	// 关闭 Hub
	h.Close()

	// 等待 Run 退出
	select {
	case <-done:
		// 预期行为
	case <-time.After(2 * time.Second):
		t.Error("Run() did not exit after Close()")
	}

	// 验证 clients map 已被清空
	h.clientsLock.RLock()
	if len(h.clients) != 0 {
		t.Errorf("clients map not cleared, len = %d", len(h.clients))
	}
	h.clientsLock.RUnlock()
}

// TestHubBroadcastToClosedClient 测试向已关闭的客户端广播
func TestHubBroadcastToClosedClient(t *testing.T) {
	h := NewHub()
	go h.Run()
	defer h.Close()

	// 创建一个没有 writer goroutine 的客户端（模拟已关闭）
	client := NewClient(h, WithID("closed-client"))
	// 填满 send channel
	for i := 0; i < cap(client.send); i++ {
		client.send <- []byte("filler")
	}

	// 注册客户端
	h.register <- client
	time.Sleep(10 * time.Millisecond)

	// 广播消息 - 应该跳过已满的客户端
	message := []byte("test broadcast")
	h.Broadcast(message)

	// 给一点时间处理
	time.Sleep(50 * time.Millisecond)

	// 验证通过 - 没有 panic 即可
}

// TestHubBroadcastEmptyHub 测试向空 Hub 广播
func TestHubBroadcastEmptyHub(t *testing.T) {
	h := NewHub()
	go h.Run()
	defer h.Close()

	// 向空 Hub 广播
	message := []byte("test broadcast")
	h.Broadcast(message)

	// 给一点时间处理
	time.Sleep(50 * time.Millisecond)

	// 验证通过 - 没有 panic 即可
}

// TestHubRegisterNilClient 测试注册 nil 客户端
func TestHubRegisterNilClient(t *testing.T) {
	h := NewHub()
	go h.Run()
	defer h.Close()

	// 注册 nil 客户端 - 应该被忽略
	h.register <- nil

	// 给一点时间处理
	time.Sleep(50 * time.Millisecond)

	// 验证通过 - 没有 panic 即可
}

// TestHubUnregisterNilClient 测试注销 nil 客户端
func TestHubUnregisterNilClient(t *testing.T) {
	h := NewHub()
	go h.Run()
	defer h.Close()

	// 注销 nil 客户端 - 应该被忽略
	h.unregister <- nil

	// 给一点时间处理
	time.Sleep(50 * time.Millisecond)

	// 验证通过 - 没有 panic 即可
}

// TestHubUnregisterNonExistentClient 测试注销不存在的客户端
func TestHubUnregisterNonExistentClient(t *testing.T) {
	h := NewHub()
	go h.Run()
	defer h.Close()

	// 创建一个未注册的客户端
	client := NewClient(h, WithID("non-existent"))

	// 注销未注册的客户端 - 应该被忽略
	h.unregister <- client

	// 给一点时间处理
	time.Sleep(50 * time.Millisecond)

	// 验证通过 - 没有 panic 即可
}

// TestHubBroadcastToFullChannel 测试广播到满的 channel
func TestHubBroadcastToFullChannel(t *testing.T) {
	h := NewHub()
	go h.Run()
	defer h.Close()

	// 创建测试服务器
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
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
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	// 创建客户端并填满 send channel
	client := &Client{
		hub:  h,
		id:   "full-channel-client",
		send: make(chan []byte, 2),
		conn: conn,
	}

	// 填满 channel
	client.send <- []byte("filler1")
	client.send <- []byte("filler2")

	// 注册客户端
	h.register <- client
	time.Sleep(10 * time.Millisecond)

	// 广播消息 - 应该跳过已满的 channel
	h.Broadcast([]byte("test message"))

	// 给一点时间处理
	time.Sleep(50 * time.Millisecond)

	// 验证通过 - 没有 panic 即可
}
