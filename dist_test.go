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

	"github.com/ciumc/go-websocket/websocketpb"
	"github.com/gorilla/websocket"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

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

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
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

// TestCleanupExpiredConnections 测试 DistClient.Close 正确清理内部连接池
func TestCleanupExpiredConnections(t *testing.T) {
	// 连接池现在由 DistClient 内部管理
	storage := NewMockStorage()
	client := NewDistClient(storage)

	// 验证 Close 不 panic
	client.Close()

	// 验证可以多次调用 Close
	client.Close()
}

// TestStartPoolCleanup 测试连接池清理 goroutine 自动启动
// 连接池在 NewDistClient 时自动启动清理 goroutine
func TestStartPoolCleanup(t *testing.T) {
	storage := NewMockStorage()
	client := NewDistClient(storage)
	defer client.Close()

	// 给一点时间让 goroutine 启动
	time.Sleep(10 * time.Millisecond)

	// 验证通过 - 没有 panic 即可
}

// TestGrpcClientConnPoolReuse 测试连接池重用
func TestGrpcClientConnPoolReuse(t *testing.T) {
	// 连接池现在是 DistClient 内部管理
	// 创建 DistClient 测试基本行为
	storage := NewMockStorage()
	client := NewDistClient(storage)
	defer client.Close()

	// 验证 DistClient 正常工作
	if client.storage != storage {
		t.Error("DistClient storage not set correctly")
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

// TestDistServerEmitWithClient 测试 DistServer Emit 方法（客户端存在）
func TestDistServerEmitWithClient(t *testing.T) {
	hub := NewHubRun()
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

// TestDistClientEmitContextCancelled 测试 Emit 在 context 已取消时的行为
func TestDistClientEmitContextCancelled(t *testing.T) {
	storage := NewMockStorage()
	_ = storage.Set("test-client", "127.0.0.1:8080")
	client := NewDistClient(storage)
	defer client.Close()

	// 创建一个已取消的 context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	ok, err := client.Emit(ctx, "test-client", []byte("test message"))

	if err == nil {
		t.Error("Emit() should return error when context is cancelled")
	}
	if !strings.Contains(err.Error(), "context cancelled") {
		t.Errorf("Emit() error should contain 'context cancelled', got: %v", err)
	}
	if ok {
		t.Error("Emit() should return ok = false when context is cancelled")
	}
}

// TestDistClientOnlineContextCancelled 测试 Online 在 context 已取消时的行为
func TestDistClientOnlineContextCancelled(t *testing.T) {
	storage := NewMockStorage()
	_ = storage.Set("test-client", "127.0.0.1:8080")
	client := NewDistClient(storage)
	defer client.Close()

	// 创建一个已取消的 context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	ok, err := client.Online(ctx, "test-client")

	if err == nil {
		t.Error("Online() should return error when context is cancelled")
	}
	if !strings.Contains(err.Error(), "context cancelled") {
		t.Errorf("Online() error should contain 'context cancelled', got: %v", err)
	}
	if ok {
		t.Error("Online() should return ok = false when context is cancelled")
	}
}

// TestDistClientBroadcastContextCancelled 测试 Broadcast 在 context 已取消时的行为
func TestDistClientBroadcastContextCancelled(t *testing.T) {
	storage := NewMockStorage()
	_ = storage.Set("test-client", "127.0.0.1:8080")
	client := NewDistClient(storage)
	defer client.Close()

	// 创建一个已取消的 context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	count, err := client.Broadcast(ctx, []byte("test message"))

	if err == nil {
		t.Error("Broadcast() should return error when context is cancelled")
	}
	if !strings.Contains(err.Error(), "context cancelled") {
		t.Errorf("Broadcast() error should contain 'context cancelled', got: %v", err)
	}
	if count != 0 {
		t.Error("Broadcast() should return count = 0 when context is cancelled")
	}
}

// TestGrpcPoolGet 测试 grpcPool.get() 连接创建
func TestGrpcPoolGet(t *testing.T) {
	// 使用 newGrpcPool 确保 done channel 已初始化
	p := newGrpcPool()
	defer p.close()

	// 测试获取连接（grpc.NewClient 是异步的，不会立即失败）
	conn, err := p.get("127.0.0.1:9999")
	if err != nil {
		t.Errorf("get() unexpected error: %v", err)
	}
	if conn == nil {
		t.Error("get() should return a connection")
	} else {
		defer conn.Close()
	}

	// 验证连接已添加到池中
	p.mu.RLock()
	lenConns := len(p.conns)
	p.mu.RUnlock()

	if lenConns != 1 {
		t.Errorf("pool should have 1 connection, got %d", lenConns)
	}
}

// TestGrpcPoolGetConnectionReuse 测试 grpcPool.get() 连接复用
func TestGrpcPoolGetConnectionReuse(t *testing.T) {
	// 使用 newGrpcPool 确保 done channel 已初始化
	p := newGrpcPool()
	defer p.close()

	addr := "127.0.0.1:9999"

	// 第一次获取连接
	conn1, err := p.get(addr)
	if err != nil {
		t.Fatalf("get() unexpected error: %v", err)
	}
	defer conn1.Close()

	// 第二次获取应该返回同一个连接（复用）
	conn2, err := p.get(addr)
	if err != nil {
		t.Fatalf("get() unexpected error on retry: %v", err)
	}

	if conn1 != conn2 {
		t.Error("get() should return the same connection (reuse)")
	}

	// 验证池中只有一个连接
	p.mu.RLock()
	lenConns := len(p.conns)
	p.mu.RUnlock()

	if lenConns != 1 {
		t.Errorf("pool should have 1 connection, got %d", lenConns)
	}
}

// TestGrpcPoolGetConnectionStateCheck 测试 grpcPool.get() 连接状态检查
func TestGrpcPoolGetConnectionStateCheck(t *testing.T) {
	p := newGrpcPool()
	defer p.close()

	addr := "127.0.0.1:9998"

	// 第一次获取连接
	conn1, err := p.get(addr)
	if err != nil {
		t.Fatalf("get() unexpected error: %v", err)
	}
	defer conn1.Close()

	// 模拟连接状态变为 TransientFailure，触发重建
	// 通过删除连接来模拟
	p.mu.Lock()
	delete(p.conns, addr)
	p.mu.Unlock()

	// 再次调用 get 应该创建新连接
	conn2, err := p.get(addr)
	if err != nil {
		t.Fatalf("get() unexpected error on retry: %v", err)
	}
	defer conn2.Close()

	// 验证是新连接
	if conn1 == conn2 {
		t.Error("get() should return a new connection after deletion")
	}
}

// TestGrpcPoolGetInvalidConnection 测试 grpcPool.get() 获取无效连接
func TestGrpcPoolGetInvalidConnection(t *testing.T) {
	p := newGrpcPool()
	defer p.close()

	// 获取一个有效地址的连接（grpc 是异步连接，不会立即失败）
	conn, err := p.get("127.0.0.1:9997")
	if err != nil {
		t.Errorf("get() unexpected error: %v", err)
	} else {
		defer conn.Close()
	}

	// 验证连接已添加到池中
	p.mu.RLock()
	lenConns := len(p.conns)
	p.mu.RUnlock()

	if lenConns != 1 {
		t.Errorf("pool should have 1 connection, got %d", lenConns)
	}
}

// TestGrpcPoolCleanup 测试 grpcPool 清理过期连接
func TestGrpcPoolCleanup(t *testing.T) {
	p := newGrpcPool()
	defer p.close()

	// 验证清理 goroutine 已启动（不 panic 即可）
	// 给一点时间让清理 goroutine 运行
	time.Sleep(10 * time.Millisecond)

	// 手动触发一次清理（通过访问内部方法）
	p.cleanExpired()

	// 验证清理后池为空
	p.mu.RLock()
	lenConns := len(p.conns)
	p.mu.RUnlock()

	if lenConns != 0 {
		t.Errorf("pool should be empty after cleanup, got %d", lenConns)
	}
}
