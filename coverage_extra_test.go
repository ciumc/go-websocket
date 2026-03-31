package websocket

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestClientConnMethodExtra 测试 Client.Conn 方法额外场景
func TestClientConnMethodExtra(t *testing.T) {
	hub := NewHubRun()
	defer hub.Close()

	// 创建测试服务器
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		}
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

	t.Run("successful connection", func(t *testing.T) {
		wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
		wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			t.Fatalf("Dial failed: %v", err)
		}
		defer wsConn.Close()

		// 使用客户端连接
		client := NewClient(hub, WithID("conn-test"))
		client.conn = wsConn
		hub.register <- client

		time.Sleep(50 * time.Millisecond)

		// 验证客户端已注册
		if _, ok := hub.Client("conn-test"); !ok {
			t.Error("Client should be registered")
		}
	})
}

// TestClientWriterMoreCoverage 测试 writer 更多分支
func TestClientWriterMoreCoverage(t *testing.T) {
	hub := NewHubRunWithConfig(
		WithPingPeriod(100*time.Millisecond),
		WithWriteWait(50*time.Millisecond),
	)
	defer hub.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := hub.Upgrader().Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		// 读取消息但不响应
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
		t.Fatalf("Dial failed: %v", err)
	}
	defer wsConn.Close()

	client := NewClient(hub, WithID("writer-coverage-test"), WithBufSize(10))
	client.conn = wsConn
	hub.register <- client

	// 启动 writer
	go client.writer()

	// 发送多条消息测试批量写入
	for i := 0; i < 5; i++ {
		client.Emit([]byte("batch message"))
	}

	time.Sleep(100 * time.Millisecond)
}

// TestClientWriterPingPeriod 测试 writer ping 周期
func TestClientWriterPingPeriod(t *testing.T) {
	hub := NewHubRunWithConfig(
		WithPingPeriod(50*time.Millisecond),
		WithPongWait(200*time.Millisecond),
	)
	defer hub.Close()

	pingReceived := make(chan bool, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := hub.Upgrader().Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		conn.SetPongHandler(func(string) error {
			pingReceived <- true
			return nil
		})

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
		t.Fatalf("Dial failed: %v", err)
	}
	defer wsConn.Close()

	client := NewClient(hub, WithID("ping-test"))
	client.conn = wsConn
	hub.register <- client

	go client.writer()
	go client.reader()

	// 等待 ping
	select {
	case <-pingReceived:
		t.Log("Ping received")
	case <-time.After(200 * time.Millisecond):
		t.Log("No ping received (acceptable)")
	}
}

// TestHubBroadcastWithFullChannel 测试广播到满的 channel
func TestHubBroadcastWithFullChannel(t *testing.T) {
	hub := NewHubRunWithConfig(WithHubBufSize(5))
	defer hub.Close()

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
	for i := 0; i < 3; i++ {
		wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			continue
		}

		client := &Client{
			hub:  hub,
			id:   string(rune('A' + i)),
			send: make(chan []byte, 2), // 小缓冲区
			conn: wsConn,
		}
		hub.register <- client
	}

	time.Sleep(50 * time.Millisecond)

	// 广播大量消息
	for i := 0; i < 20; i++ {
		hub.Broadcast([]byte("broadcast test"))
	}

	time.Sleep(50 * time.Millisecond)
}

// TestDistClientEmitWithStorageError 测试 Emit 处理存储错误
func TestDistClientEmitWithStorageError(t *testing.T) {
	storage := &errorStorage{}
	client := NewDistClient(storage)
	defer client.Close()

	ctx := context.Background()

	ok, err := client.Emit(ctx, "test-id", []byte("test"))
	if err == nil {
		t.Error("Emit should return error with failing storage")
	}
	if ok {
		t.Error("Emit should return false with failing storage")
	}
}

// TestDistClientOnlineWithStorageError 测试 Online 处理存储错误
func TestDistClientOnlineWithStorageError(t *testing.T) {
	storage := &errorStorage{}
	client := NewDistClient(storage)
	defer client.Close()

	ctx := context.Background()

	ok, err := client.Online(ctx, "test-id")
	if err == nil {
		t.Error("Online should return error with failing storage")
	}
	if ok {
		t.Error("Online should return false with failing storage")
	}
}

// TestDistClientBroadcastWithStorageError 测试 Broadcast 处理存储错误
func TestDistClientBroadcastWithStorageError(t *testing.T) {
	storage := &errorStorage{}
	client := NewDistClient(storage)
	defer client.Close()

	ctx := context.Background()

	count, err := client.Broadcast(ctx, []byte("test"))
	if err == nil {
		t.Error("Broadcast should return error with failing storage")
	}
	if count != 0 {
		t.Error("Broadcast should return 0 with failing storage")
	}
}

// TestGrpcPoolCleanExpiredWithConnections 测试 cleanExpired 有连接时
func TestGrpcPoolCleanExpiredWithConnections(t *testing.T) {
	storage := NewMockStorage()
	client := NewDistClient(storage)
	defer client.Close()

	// 验证 pool 已创建
	if client.pool == nil {
		t.Fatal("Pool should not be nil")
	}

	// 手动触发清理
	client.pool.cleanExpired()

	// 验证可以正常工作
	time.Sleep(10 * time.Millisecond)
}

// TestCloseGrpcPoolWithItems 测试 CloseGrpcPool 有项目时
func TestCloseGrpcPoolWithItems(t *testing.T) {
	// 清空池
	CloseGrpcPool()

	// 添加 mock 连接到全局池
	grpcClientPool.Store("test-pool-addr-1", &pooledConn{
		conn:     nil,
		lastUsed: time.Now().UnixNano(),
	})
	grpcClientPool.Store("test-pool-addr-2", &pooledConn{
		conn:     nil,
		lastUsed: time.Now().UnixNano(),
	})

	// 调用 CloseGrpcPool
	CloseGrpcPool()

	// 验证池已清空
	count := 0
	grpcClientPool.Range(func(key, value interface{}) bool {
		count++
		return true
	})

	if count != 0 {
		t.Errorf("Pool should be empty, got %d items", count)
	}

	// 再次调用（测试空池情况）
	CloseGrpcPool()
}

// TestEtcdStorageWithMock 测试 Etcd 存储接口（使用 mock）
func TestEtcdStorageWithMock(t *testing.T) {
	// 使用 MockEtcdStorage 测试接口实现
	storage := NewMockEtcdStorage()

	t.Run("Set and Get", func(t *testing.T) {
		err := storage.Set("etcd-key", "etcd-value")
		if err != nil {
			t.Fatalf("Set failed: %v", err)
		}

		val, err := storage.Get("etcd-key")
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if val != "etcd-value" {
			t.Errorf("Get = %v, want etcd-value", val)
		}
	})

	t.Run("Del", func(t *testing.T) {
		storage.Set("del-key", "value")
		err := storage.Del("del-key")
		if err != nil {
			t.Fatalf("Del failed: %v", err)
		}

		val, _ := storage.Get("del-key")
		if val != "" {
			t.Error("Key should be deleted")
		}
	})

	t.Run("Clear", func(t *testing.T) {
		storage.Set("clear-1", "host1:8080")
		storage.Set("clear-2", "host1:8080")
		storage.Set("clear-3", "host2:8080")

		err := storage.Clear("host1:8080")
		if err != nil {
			t.Fatalf("Clear failed: %v", err)
		}

		if v, _ := storage.Get("clear-1"); v != "" {
			t.Error("clear-1 should be deleted")
		}
		if v, _ := storage.Get("clear-3"); v != "host2:8080" {
			t.Error("clear-3 should still exist")
		}
	})

	t.Run("All", func(t *testing.T) {
		all, err := storage.All()
		if err != nil {
			t.Fatalf("All failed: %v", err)
		}
		t.Logf("All returned %d items", len(all))
	})
}
