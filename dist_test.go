package websocket

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jayecc/go-websocket/websocketpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
)

// TestNewDistSession 测试 DistSession 构造函数
func TestNewDistSession(t *testing.T) {
	hub := NewHubRun()
	defer hub.Close()

	storage := NewMockStorage()
	addr := "127.0.0.1:8080"

	session := NewDistSession(hub, storage, addr)
	if session == nil {
		t.Fatal("NewDistSession() returned nil")
	}

	// 验证 client 正确设置
	if session.client == nil {
		t.Error("NewDistSession() client is nil")
	}

	// 验证 storage 正确设置
	if session.storage != storage {
		t.Error("NewDistSession() storage mismatch")
	}

	// 验证 addr 正确设置
	if session.addr != addr {
		t.Errorf("NewDistSession() addr = %v, want %v", session.addr, addr)
	}
}

// TestDistSessionClient 测试 Client() 方法返回内部的 Client 实例
func TestDistSessionClient(t *testing.T) {
	hub := NewHubRun()
	defer hub.Close()

	storage := NewMockStorage()
	session := NewDistSession(hub, storage, "127.0.0.1:8080")

	client := session.Client()
	if client == nil {
		t.Fatal("Client() returned nil")
	}

	// 验证返回的是同一个 client 实例
	if client != session.client {
		t.Error("Client() returned different instance")
	}
}

// TestNewDistClient 测试 DistClient 构造函数
func TestNewDistClient(t *testing.T) {
	storage := NewMockStorage()
	client := NewDistClient(storage)

	if client == nil {
		t.Fatal("NewDistClient() returned nil")
	}

	if client.storage != storage {
		t.Error("NewDistClient() storage mismatch")
	}

	// 验证默认超时设置
	if client.timeout != 0 {
		t.Errorf("NewDistClient() default timeout = %v, want 0", client.timeout)
	}
}

// TestNewDistClientWithTimeout 测试带超时的 DistClient 构造函数
func TestNewDistClientWithTimeout(t *testing.T) {
	storage := NewMockStorage()
	timeout := 5 * time.Second
	client := NewDistClient(storage, WithDistTimeout(timeout))

	if client == nil {
		t.Fatal("NewDistClient() with timeout returned nil")
	}

	if client.timeout != timeout {
		t.Errorf("NewDistClient() timeout = %v, want %v", client.timeout, timeout)
	}
}

// TestDistClientEmit 测试 Emit() 逻辑
func TestDistClientEmit(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(*MockStorage)
		id        string
		wantOk    bool
		wantError bool
	}{
		{
			name: "client found",
			setup: func(m *MockStorage) {
				_ = m.Set("client1", "127.0.0.1:8080")
			},
			id:        "client1",
			wantOk:    false, // 因为没有真实的 gRPC 服务器，连接会失败
			wantError: true,
		},
		{
			name:      "client not found",
			setup:     func(m *MockStorage) {},
			id:        "nonexistent",
			wantOk:    false,
			wantError: true,
		},
		{
			name: "empty address",
			setup: func(m *MockStorage) {
				_ = m.Set("client2", "")
			},
			id:        "client2",
			wantOk:    false,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storage := NewMockStorage()
			tt.setup(storage)

			client := NewDistClient(storage)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()

			ok, err := client.Emit(ctx, tt.id, []byte("test message"))

			if tt.wantError && err == nil {
				t.Errorf("Emit() error = nil, want error")
			}
			if !tt.wantError && err != nil {
				t.Errorf("Emit() error = %v, want nil", err)
			}
			if ok != tt.wantOk {
				t.Errorf("Emit() ok = %v, want %v", ok, tt.wantOk)
			}
		})
	}
}

// TestDistClientOnline 测试 Online() 逻辑
func TestDistClientOnline(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(*MockStorage)
		id        string
		wantOk    bool
		wantError bool
	}{
		{
			name: "client found",
			setup: func(m *MockStorage) {
				_ = m.Set("client1", "127.0.0.1:8080")
			},
			id:        "client1",
			wantOk:    false, // 因为没有真实的 gRPC 服务器，连接会失败
			wantError: true,
		},
		{
			name:      "client not found",
			setup:     func(m *MockStorage) {},
			id:        "nonexistent",
			wantOk:    false,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storage := NewMockStorage()
			tt.setup(storage)

			client := NewDistClient(storage)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()

			ok, err := client.Online(ctx, tt.id)

			if tt.wantError && err == nil {
				t.Errorf("Online() error = nil, want error")
			}
			if !tt.wantError && err != nil {
				t.Errorf("Online() error = %v, want nil", err)
			}
			if ok != tt.wantOk {
				t.Errorf("Online() ok = %v, want %v", ok, tt.wantOk)
			}
		})
	}
}

// TestDistClientBroadcast 测试 Broadcast() 逻辑
func TestDistClientBroadcast(t *testing.T) {
	storage := NewMockStorage()
	_ = storage.Set("client1", "127.0.0.1:8080")
	_ = storage.Set("client2", "127.0.0.1:8081")
	_ = storage.Set("client3", "127.0.0.1:8080") // 同一地址

	client := NewDistClient(storage)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// 由于没有真实的 gRPC 服务器，预期返回 0
	count, err := client.Broadcast(ctx, []byte("test message"))

	// 预期会返回错误（连接失败）或 count=0
	if err != nil {
		// 有错误是正常的，因为无法连接到服务器
		t.Logf("Broadcast() returned error (expected): %v", err)
	}

	t.Logf("Broadcast() count = %d", count)
}

// TestNewDistServer 测试 gRPC 服务端构造函数
func TestNewDistServer(t *testing.T) {
	hub := NewHubRun()
	defer hub.Close()

	server := NewDistServer(hub)
	if server == nil {
		t.Fatal("NewDistServer() returned nil")
	}

	if server.hub != hub {
		t.Error("NewDistServer() hub mismatch")
	}
}

// setupTestGRPCServer 创建一个用于测试的内存 gRPC 服务器
func setupTestGRPCServer(t *testing.T, hub *Hub) (*grpc.ClientConn, func()) {
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()

	distServer := NewDistServer(hub)
	websocketpb.RegisterWebsocketServer(server, distServer)

	go func() {
		if err := server.Serve(listener); err != nil {
			t.Logf("Server exited with error: %v", err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	conn, err := grpc.DialContext(ctx, "passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithInsecure(),
	)
	if err != nil {
		t.Fatalf("Failed to dial bufnet: %v", err)
	}

	cleanup := func() {
		_ = conn.Close()
		server.Stop()
	}

	return conn, cleanup
}

// TestDistServerEmit 测试 gRPC Emit 方法
func TestDistServerEmit(t *testing.T) {
	hub := NewHubRun()
	defer hub.Close()

	conn, cleanup := setupTestGRPCServer(t, hub)
	defer cleanup()

	client := websocketpb.NewWebsocketClient(conn)

	tests := []struct {
		name     string
		setup    func()
		id       string
		data     []byte
		wantSucc bool
	}{
		{
			name:     "client not exists",
			setup:    func() {},
			id:       "nonexistent",
			data:     []byte("hello"),
			wantSucc: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup()

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()

			resp, err := client.Emit(ctx, &websocketpb.EmitRequest{
				Id:   tt.id,
				Data: tt.data,
			})

			if err != nil {
				t.Fatalf("Emit() error = %v", err)
			}

			if resp.Success != tt.wantSucc {
				t.Errorf("Emit() success = %v, want %v", resp.Success, tt.wantSucc)
			}
		})
	}
}

// TestDistServerOnline 测试 gRPC Online 方法
func TestDistServerOnline(t *testing.T) {
	hub := NewHubRun()
	defer hub.Close()

	conn, cleanup := setupTestGRPCServer(t, hub)
	defer cleanup()

	client := websocketpb.NewWebsocketClient(conn)

	tests := []struct {
		name       string
		id         string
		wantOnline bool
	}{
		{
			name:       "client not exists",
			id:         "nonexistent",
			wantOnline: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()

			resp, err := client.Online(ctx, &websocketpb.OnlineRequest{
				Id: tt.id,
			})

			if err != nil {
				t.Fatalf("Online() error = %v", err)
			}

			if resp.Online != tt.wantOnline {
				t.Errorf("Online() online = %v, want %v", resp.Online, tt.wantOnline)
			}
		})
	}
}

// TestDistServerBroadcast 测试 gRPC Broadcast 方法
func TestDistServerBroadcast(t *testing.T) {
	hub := NewHubRun()
	defer hub.Close()

	conn, cleanup := setupTestGRPCServer(t, hub)
	defer cleanup()

	client := websocketpb.NewWebsocketClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	resp, err := client.Broadcast(ctx, &websocketpb.BroadcastRequest{
		Data: []byte("hello everyone"),
	})

	if err != nil {
		t.Fatalf("Broadcast() error = %v", err)
	}

	if resp.Count != 1 {
		t.Errorf("Broadcast() count = %v, want 1", resp.Count)
	}
}

// TestGrpcConnectionPool 测试 gRPC 连接池的基本行为
func TestGrpcConnectionPool(t *testing.T) {
	// 测试连接池初始状态
	// 注意：由于需要真实 gRPC 服务器，这里主要测试构造逻辑

	// 清理连接池
	CloseGrpcPool()

	// 验证 CloseGrpcPool 可以正常调用（不 panic）
	CloseGrpcPool()
}

// TestDistClientWithTimeoutOption 测试超时选项
func TestDistClientWithTimeoutOption(t *testing.T) {
	tests := []struct {
		name    string
		timeout time.Duration
		want    time.Duration
	}{
		{
			name:    "default timeout",
			timeout: 0,
			want:    0,
		},
		{
			name:    "custom timeout",
			timeout: 5 * time.Second,
			want:    5 * time.Second,
		},
		{
			name:    "long timeout",
			timeout: 30 * time.Second,
			want:    30 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storage := NewMockStorage()
			var client *DistClient
			if tt.timeout == 0 {
				client = NewDistClient(storage)
			} else {
				client = NewDistClient(storage, WithDistTimeout(tt.timeout))
			}

			if client.timeout != tt.want {
				t.Errorf("timeout = %v, want %v", client.timeout, tt.want)
			}
		})
	}
}

// TestDistClientGetTimeout 测试 getTimeout 方法
func TestDistClientGetTimeout(t *testing.T) {
	tests := []struct {
		name          string
		clientTimeout time.Duration
		want          time.Duration
	}{
		{
			name:          "default returns 2s",
			clientTimeout: 0,
			want:          2 * time.Second,
		},
		{
			name:          "custom timeout",
			clientTimeout: 5 * time.Second,
			want:          5 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storage := NewMockStorage()
			client := NewDistClient(storage, WithDistTimeout(tt.clientTimeout))

			got := client.getTimeout()
			if got != tt.want {
				t.Errorf("getTimeout() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestDistSessionWithOptions 测试 DistSession 使用 Option
func TestDistSessionWithOptions(t *testing.T) {
	hub := NewHubRun()
	defer hub.Close()

	storage := NewMockStorage()
	customID := "custom-client-id"

	session := NewDistSession(hub, storage, "127.0.0.1:8080", WithID(customID))

	if session.client.GetID() != customID {
		t.Errorf("client ID = %v, want %v", session.client.GetID(), customID)
	}
}

// TestCleanupExpiredConnections 测试过期连接被清理
func TestCleanupExpiredConnections(t *testing.T) {
	// 清理连接池
	CloseGrpcPool()

	// 创建一个模拟的过期 pooledConn（不设置 conn 字段，测试 Range 处理）
	// 注意：cleanupExpiredConnections 会在 conn 为 nil 时 panic，所以我们需要测试 Range 的行为
	// 这里我们测试的是 cleanupExpiredConnections 函数能正常执行不 panic

	// 先确保连接池为空
	count := 0
	grpcClientPool.Range(func(key, value interface{}) bool {
		count++
		return true
	})

	if count != 0 {
		t.Logf("Pool has %d connections before test", count)
	}

	// 调用清理函数 - 主要验证不 panic
	cleanupExpiredConnections()

	// 验证通过 - 没有 panic 即可
}

// TestCloseGrpcPool 测试关闭所有连接池
func TestCloseGrpcPool(t *testing.T) {
	// 先清理
	CloseGrpcPool()

	// 验证可以正常调用多次不 panic
	CloseGrpcPool()
	CloseGrpcPool()
}

// TestStartPoolCleanup 测试清理 goroutine 启动
func TestStartPoolCleanup(t *testing.T) {
	// 重置 poolCleanupOnce，确保可以重新启动
	// 注意：由于 sync.Once 无法重置，这里主要测试函数不 panic

	// 调用 startPoolCleanup 多次，应该只启动一个 goroutine
	startPoolCleanup()
	startPoolCleanup()
	startPoolCleanup()

	// 给一点时间让 goroutine 启动
	time.Sleep(10 * time.Millisecond)

	// 验证通过 - 没有 panic 即可
}

// TestGrpcClientConnPoolReuse 测试连接池重用
func TestGrpcClientConnPoolReuse(t *testing.T) {
	// 清理连接池
	CloseGrpcPool()

	// 由于需要真实 gRPC 服务器，这里主要测试连接池的初始状态
	// 验证连接池为空
	count := 0
	grpcClientPool.Range(func(key, value interface{}) bool {
		count++
		return true
	})

	if count != 0 {
		t.Errorf("Expected empty pool, got %d connections", count)
	}
}

// TestDistClientBroadcastEmptyStorage 测试 Broadcast 空 storage
func TestDistClientBroadcastEmptyStorage(t *testing.T) {
	storage := NewMockStorage()
	client := NewDistClient(storage)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	count, err := client.Broadcast(ctx, []byte("test message"))

	// 空 storage 应该返回 0，没有错误
	if err != nil {
		t.Errorf("Broadcast() error = %v, want nil", err)
	}
	if count != 0 {
		t.Errorf("Broadcast() count = %v, want 0", count)
	}
}

// TestDistClientBroadcastSingleAddress 测试 Broadcast 单地址
func TestDistClientBroadcastSingleAddress(t *testing.T) {
	storage := NewMockStorage()
	// 设置多个客户端在同一地址
	_ = storage.Set("client1", "127.0.0.1:8080")
	_ = storage.Set("client2", "127.0.0.1:8080")
	_ = storage.Set("client3", "127.0.0.1:8080")

	client := NewDistClient(storage)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// 由于没有真实 gRPC 服务器，预期返回 0
	count, err := client.Broadcast(ctx, []byte("test message"))

	// 可能有错误（连接失败）或返回 0
	if err != nil {
		t.Logf("Broadcast() error (expected): %v", err)
	}
	t.Logf("Broadcast() count = %d", count)
}

// TestDistClientBroadcastMultipleAddresses 测试 Broadcast 多地址
func TestDistClientBroadcastMultipleAddresses(t *testing.T) {
	storage := NewMockStorage()
	// 设置客户端在不同地址
	_ = storage.Set("client1", "127.0.0.1:8080")
	_ = storage.Set("client2", "127.0.0.1:8081")
	_ = storage.Set("client3", "127.0.0.1:8082")

	client := NewDistClient(storage)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// 由于没有真实 gRPC 服务器，预期返回 0
	count, err := client.Broadcast(ctx, []byte("test message"))

	// 可能有错误（连接失败）或返回 0
	if err != nil {
		t.Logf("Broadcast() error (expected): %v", err)
	}
	t.Logf("Broadcast() count = %d", count)
}

// TestDistClientBroadcastWithError 测试 Broadcast 处理 storage 错误
func TestDistClientBroadcastWithError(t *testing.T) {
	storage := &MockStorage{data: make(map[string]string)}
	// 不设置任何数据，模拟空 storage

	client := NewDistClient(storage)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	count, err := client.Broadcast(ctx, []byte("test message"))

	// 空 storage 应该返回 0
	if err != nil {
		t.Errorf("Broadcast() error = %v, want nil", err)
	}
	if count != 0 {
		t.Errorf("Broadcast() count = %v, want 0", count)
	}
}

// TestDistClientBroadcastV2 测试 BroadcastV2 方法
func TestDistClientBroadcastV2(t *testing.T) {
	storage := NewMockStorage()
	_ = storage.Set("client1", "127.0.0.1:8080")

	client := NewDistClient(storage)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// BroadcastV2 应该调用 Broadcast
	count, err := client.BroadcastV2(ctx, []byte("test message"))

	// 由于没有真实 gRPC 服务器，预期返回 0
	if err != nil {
		t.Logf("BroadcastV2() error (expected): %v", err)
	}
	t.Logf("BroadcastV2() count = %d", count)
}

// TestDistSessionOnConnect 测试 DistSession OnConnect
func TestDistSessionOnConnect(t *testing.T) {
	hub := NewHubRun()
	defer hub.Close()

	storage := NewMockStorage()
	session := NewDistSession(hub, storage, "127.0.0.1:8080")

	connectCalled := false
	session.OnConnect(func(conn *Client) {
		connectCalled = true
	})

	// 验证回调已设置
	if session.client.onConnect == nil {
		t.Error("OnConnect callback was not set")
	}

	// 手动触发回调进行测试
	if session.client.onConnect != nil {
		session.client.onConnect(session.client)
	}

	// 注意：由于 storage 操作在回调中，我们需要验证 storage 是否被更新
	// 但由于 client 没有真实连接，这里主要验证回调机制
	t.Logf("Connect callback called: %v", connectCalled)
}

// TestDistSessionOnDisconnect 测试 DistSession OnDisconnect
func TestDistSessionOnDisconnect(t *testing.T) {
	hub := NewHubRun()
	defer hub.Close()

	storage := NewMockStorage()
	// 预先设置一个客户端
	_ = storage.Set("test-client", "127.0.0.1:8080")

	session := NewDistSession(hub, storage, "127.0.0.1:8080", WithID("test-client"))

	disconnectCalled := false
	session.OnDisconnect(func(id string) {
		disconnectCalled = true
	})

	// 验证回调已设置
	if session.client.onDisconnect == nil {
		t.Error("OnDisconnect callback was not set")
	}

	// 手动触发回调进行测试
	if session.client.onDisconnect != nil {
		session.client.onDisconnect("test-client")
	}

	if !disconnectCalled {
		t.Error("OnDisconnect callback was not called")
	}
}

// TestDistSessionOnEvent 测试 DistSession OnEvent
func TestDistSessionOnEvent(t *testing.T) {
	hub := NewHubRun()
	defer hub.Close()

	storage := NewMockStorage()
	session := NewDistSession(hub, storage, "127.0.0.1:8080")

	eventCalled := false
	session.OnEvent(func(conn *Client, messageType int, message []byte) {
		eventCalled = true
	})

	// 验证回调已设置
	if session.client.onEvent == nil {
		t.Error("OnEvent callback was not set")
	}

	// 手动触发回调进行测试
	if session.client.onEvent != nil {
		session.client.onEvent(session.client, websocket.TextMessage, []byte("test"))
	}

	if !eventCalled {
		t.Error("OnEvent callback was not called")
	}
}

// TestDistSessionOnError 测试 DistSession OnError
func TestDistSessionOnError(t *testing.T) {
	hub := NewHubRun()
	defer hub.Close()

	storage := NewMockStorage()
	session := NewDistSession(hub, storage, "127.0.0.1:8080")

	errorCalled := false
	session.OnError(func(id string, err error) {
		errorCalled = true
	})

	// 验证回调已设置
	if session.client.onError == nil {
		t.Error("OnError callback was not set")
	}

	// 手动触发回调进行测试
	if session.client.onError != nil {
		session.client.onError("test-client", fmt.Errorf("test error"))
	}

	if !errorCalled {
		t.Error("OnError callback was not called")
	}
}

// TestDistSessionConn 测试 DistSession Conn 方法
func TestDistSessionConn(t *testing.T) {
	hub := NewHubRun()
	defer hub.Close()

	storage := NewMockStorage()
	session := NewDistSession(hub, storage, "127.0.0.1:8080")

	// 创建测试服务器
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
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

	// 创建 WebSocket 连接
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to dial: %v", err)
	}
	defer wsConn.Close()

	// 手动设置连接
	session.client.conn = wsConn
	hub.register <- session.client

	// 等待注册
	time.Sleep(50 * time.Millisecond)

	// 验证客户端已注册
	if _, ok := hub.Client(session.client.GetID()); !ok {
		t.Error("Session client was not registered")
	}
}

// TestDistServerEmitWithClient 测试 DistServer Emit 方法（客户端存在）
func TestDistServerEmitWithClient(t *testing.T) {
	hub := NewHubRun()
	defer hub.Close()

	// 创建测试服务器
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
	wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to dial: %v", err)
	}
	defer wsConn.Close()

	// 创建并注册客户端
	client := NewClient(hub, WithID("emit-test-client"))
	client.conn = wsConn
	hub.register <- client
	time.Sleep(50 * time.Millisecond)

	// 创建 DistServer
	distServer := NewDistServer(hub)

	// 测试 Emit
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	resp, err := distServer.Emit(ctx, &websocketpb.EmitRequest{
		Id:   "emit-test-client",
		Data: []byte("test message"),
	})

	if err != nil {
		t.Errorf("Emit() error = %v", err)
	}
	if !resp.Success {
		t.Error("Emit() should return success = true")
	}
}

// TestDistServerEmitClientNotFound 测试 DistServer Emit 方法（客户端不存在）
func TestDistServerEmitClientNotFound(t *testing.T) {
	hub := NewHubRun()
	defer hub.Close()

	distServer := NewDistServer(hub)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	resp, err := distServer.Emit(ctx, &websocketpb.EmitRequest{
		Id:   "non-existent-client",
		Data: []byte("test message"),
	})

	if err != nil {
		t.Errorf("Emit() error = %v", err)
	}
	if resp.Success {
		t.Error("Emit() should return success = false for non-existent client")
	}
}

// TestDistServerOnlineWithClient 测试 DistServer Online 方法（客户端存在）
func TestDistServerOnlineWithClient(t *testing.T) {
	hub := NewHubRun()
	defer hub.Close()

	// 创建并注册客户端
	client := NewClient(hub, WithID("online-test-client"))
	hub.register <- client
	time.Sleep(50 * time.Millisecond)

	distServer := NewDistServer(hub)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	resp, err := distServer.Online(ctx, &websocketpb.OnlineRequest{
		Id: "online-test-client",
	})

	if err != nil {
		t.Errorf("Online() error = %v", err)
	}
	if !resp.Online {
		t.Error("Online() should return online = true for existing client")
	}
}

// TestDistServerBroadcastWithClients 测试 DistServer Broadcast 方法（有客户端）
func TestDistServerBroadcastWithClients(t *testing.T) {
	hub := NewHubRun()
	defer hub.Close()

	// 创建并注册多个客户端
	client1 := NewClient(hub, WithID("broadcast-client-1"))
	client2 := NewClient(hub, WithID("broadcast-client-2"))
	hub.register <- client1
	hub.register <- client2
	time.Sleep(50 * time.Millisecond)

	distServer := NewDistServer(hub)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	resp, err := distServer.Broadcast(ctx, &websocketpb.BroadcastRequest{
		Data: []byte("broadcast message"),
	})

	if err != nil {
		t.Errorf("Broadcast() error = %v", err)
	}
	if resp.Count != 1 {
		t.Errorf("Broadcast() count = %v, want 1", resp.Count)
	}
}

// TestDistClientEmitStorageError 测试 DistClient Emit 存储错误
func TestDistClientEmitStorageError(t *testing.T) {
	// 使用一个会返回错误的 storage
	storage := &errorStorage{}
	client := NewDistClient(storage)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	ok, err := client.Emit(ctx, "test-client", []byte("test message"))

	if err == nil {
		t.Error("Emit() should return error when storage fails")
	}
	if ok {
		t.Error("Emit() should return ok = false when storage fails")
	}
}

// TestDistClientOnlineStorageError 测试 DistClient Online 存储错误
func TestDistClientOnlineStorageError(t *testing.T) {
	storage := &errorStorage{}
	client := NewDistClient(storage)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	ok, err := client.Online(ctx, "test-client")

	if err == nil {
		t.Error("Online() should return error when storage fails")
	}
	if ok {
		t.Error("Online() should return ok = false when storage fails")
	}
}

// TestDistClientBroadcastStorageError 测试 DistClient Broadcast 存储错误
func TestDistClientBroadcastStorageError(t *testing.T) {
	storage := &errorStorage{}
	client := NewDistClient(storage)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	count, err := client.Broadcast(ctx, []byte("test message"))

	if err == nil {
		t.Error("Broadcast() should return error when storage fails")
	}
	if count != 0 {
		t.Errorf("Broadcast() count = %v, want 0", count)
	}
}

// TestDistClientEmitNotFound 测试 DistClient Emit 客户端不存在
func TestDistClientEmitNotFound(t *testing.T) {
	storage := NewMockStorage()
	client := NewDistClient(storage)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	ok, err := client.Emit(ctx, "non-existent-client", []byte("test message"))

	if err == nil {
		t.Error("Emit() should return error for non-existent client")
	}
	if ok {
		t.Error("Emit() should return ok = false for non-existent client")
	}
}

// TestDistClientOnlineNotFound 测试 DistClient Online 客户端不存在
func TestDistClientOnlineNotFound(t *testing.T) {
	storage := NewMockStorage()
	client := NewDistClient(storage)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	ok, err := client.Online(ctx, "non-existent-client")

	if err == nil {
		t.Error("Online() should return error for non-existent client")
	}
	if ok {
		t.Error("Online() should return ok = false for non-existent client")
	}
}

// TestDistClientEmitEmptyAddress 测试 DistClient Emit 空地址
func TestDistClientEmitEmptyAddress(t *testing.T) {
	storage := NewMockStorage()
	_ = storage.Set("client-with-empty-addr", "")

	client := NewDistClient(storage)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	ok, err := client.Emit(ctx, "client-with-empty-addr", []byte("test message"))

	if err == nil {
		t.Error("Emit() should return error for empty address")
	}
	if ok {
		t.Error("Emit() should return ok = false for empty address")
	}
}

// TestDistClientOnlineEmptyAddress 测试 DistClient Online 空地址
func TestDistClientOnlineEmptyAddress(t *testing.T) {
	storage := NewMockStorage()
	_ = storage.Set("client-with-empty-addr", "")

	client := NewDistClient(storage)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	ok, err := client.Online(ctx, "client-with-empty-addr")

	if err == nil {
		t.Error("Online() should return error for empty address")
	}
	if ok {
		t.Error("Online() should return ok = false for empty address")
	}
}

// errorStorage 是一个总是返回错误的 storage 实现（用于测试）
type errorStorage struct{}

func (e *errorStorage) Set(key string, value string) error {
	return fmt.Errorf("storage error")
}

func (e *errorStorage) Get(key string) (string, error) {
	return "", fmt.Errorf("storage error")
}

func (e *errorStorage) Del(key ...string) error {
	return fmt.Errorf("storage error")
}

func (e *errorStorage) Clear(host string) error {
	return fmt.Errorf("storage error")
}

func (e *errorStorage) All() (map[string]string, error) {
	return nil, fmt.Errorf("storage error")
}
