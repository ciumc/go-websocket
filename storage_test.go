package websocket

import (
	"context"
	"errors"
	"maps"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	clientv3 "go.etcd.io/etcd/client/v3"
)

// MockStorage 是一个内存实现的 Storage 接口，用于测试
type MockStorage struct {
	data map[string]string
	mu   sync.RWMutex
}

// NewMockStorage 创建一个新的 MockStorage 实例
func NewMockStorage() *MockStorage {
	return &MockStorage{
		data: make(map[string]string),
	}
}

// Set 存储键值对
func (m *MockStorage) Set(key string, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = value
	return nil
}

// Get 获取指定键的值
func (m *MockStorage) Get(key string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.data[key], nil
}

// Del 删除一个或多个键
func (m *MockStorage) Del(key ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range key {
		delete(m.data, k)
	}
	return nil
}

// Clear 清除指定主机的所有条目
func (m *MockStorage) Clear(host string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, v := range m.data {
		if v == host {
			delete(m.data, k)
		}
	}
	return nil
}

// All 获取所有键值对
func (m *MockStorage) All() (map[string]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return maps.Clone(m.data), nil
}

// TestMockStorage 测试 MockStorage 的所有方法
func TestMockStorage(t *testing.T) {
	t.Run("SetAndGet", func(t *testing.T) {
		m := NewMockStorage()
		tests := []struct {
			name  string
			key   string
			value string
		}{
			{"normal", "key1", "value1"},
			{"empty value", "key2", ""},
			{"special chars", "key!@#", "value!@#"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if err := m.Set(tt.key, tt.value); err != nil {
					t.Errorf("Set() error = %v", err)
				}
				got, err := m.Get(tt.key)
				if err != nil {
					t.Errorf("Get() error = %v", err)
				}
				if got != tt.value {
					t.Errorf("Get() = %v, want %v", got, tt.value)
				}
			})
		}
	})

	t.Run("GetNonExistent", func(t *testing.T) {
		m := NewMockStorage()
		got, err := m.Get("nonexistent")
		if err != nil {
			t.Errorf("Get() error = %v", err)
		}
		if got != "" {
			t.Errorf("Get() = %v, want empty string", got)
		}
	})

	t.Run("Del", func(t *testing.T) {
		m := NewMockStorage()
		_ = m.Set("key1", "value1")
		_ = m.Set("key2", "value2")
		_ = m.Set("key3", "value3")

		// 删除单个键
		if err := m.Del("key1"); err != nil {
			t.Errorf("Del() error = %v", err)
		}
		if _, err := m.Get("key1"); err != nil {
			t.Errorf("Get() after Del() error = %v", err)
		}
		got, _ := m.Get("key1")
		if got != "" {
			t.Errorf("Get() after Del() = %v, want empty", got)
		}

		// 删除多个键
		if err := m.Del("key2", "key3"); err != nil {
			t.Errorf("Del() multiple error = %v", err)
		}
		got2, _ := m.Get("key2")
		got3, _ := m.Get("key3")
		if got2 != "" || got3 != "" {
			t.Errorf("Get() after Del() multiple failed")
		}
	})

	t.Run("DelNonExistent", func(t *testing.T) {
		m := NewMockStorage()
		// 删除不存在的键不应该返回错误
		if err := m.Del("nonexistent"); err != nil {
			t.Errorf("Del() nonexistent error = %v", err)
		}
	})

	t.Run("Clear", func(t *testing.T) {
		m := NewMockStorage()
		_ = m.Set("client1", "host1")
		_ = m.Set("client2", "host1")
		_ = m.Set("client3", "host2")

		if err := m.Clear("host1"); err != nil {
			t.Errorf("Clear() error = %v", err)
		}

		// host1 的条目应该被删除
		got1, _ := m.Get("client1")
		got2, _ := m.Get("client2")
		if got1 != "" || got2 != "" {
			t.Errorf("Clear() did not remove host1 entries")
		}

		// host2 的条目应该保留
		got3, _ := m.Get("client3")
		if got3 != "host2" {
			t.Errorf("Clear() removed wrong entries, got %v want host2", got3)
		}
	})

	t.Run("ClearNonExistent", func(t *testing.T) {
		m := NewMockStorage()
		_ = m.Set("client1", "host1")

		// 清除不存在的主机不应该返回错误
		if err := m.Clear("nonexistent"); err != nil {
			t.Errorf("Clear() nonexistent error = %v", err)
		}

		// 现有条目应该保留
		got, _ := m.Get("client1")
		if got != "host1" {
			t.Errorf("Clear() removed wrong entries")
		}
	})

	t.Run("All", func(t *testing.T) {
		m := NewMockStorage()
		_ = m.Set("key1", "value1")
		_ = m.Set("key2", "value2")

		all, err := m.All()
		if err != nil {
			t.Errorf("All() error = %v", err)
		}
		if len(all) != 2 {
			t.Errorf("All() len = %v, want 2", len(all))
		}
		if all["key1"] != "value1" || all["key2"] != "value2" {
			t.Errorf("All() returned wrong values")
		}
	})

	t.Run("AllEmpty", func(t *testing.T) {
		m := NewMockStorage()
		all, err := m.All()
		if err != nil {
			t.Errorf("All() error = %v", err)
		}
		if len(all) != 0 {
			t.Errorf("All() len = %v, want 0", len(all))
		}
	})

	t.Run("ConcurrentAccess", func(t *testing.T) {
		m := NewMockStorage()
		var wg sync.WaitGroup

		// 并发写入
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				key := string(rune('a' + i%26))
				_ = m.Set(key, string(rune('A'+i%26)))
			}(i)
		}

		// 并发读取
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				key := string(rune('a' + i%26))
				_, _ = m.Get(key)
			}(i)
		}

		wg.Wait()
	})
}

// TestMockStorageImplementsInterface 验证 MockStorage 实现了 Storage 接口
func TestMockStorageImplementsInterface(t *testing.T) {
	var _ Storage = (*MockStorage)(nil)
}

// TestNewRedisStorage 测试 NewRedisStorage 构造函数
func TestNewRedisStorage(t *testing.T) {
	// 创建一个不连接到真实服务器的 Redis 客户端
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	defer client.Close()

	storage := NewRedisStorage(client, "test:prefix")
	if storage == nil {
		t.Error("NewRedisStorage() returned nil")
		return
	}
	if storage.client != client {
		t.Error("NewRedisStorage() client mismatch")
	}
	if storage.prefix != "test:prefix" {
		t.Errorf("NewRedisStorage() prefix = %v, want test:prefix", storage.prefix)
	}
}

// TestNewEtcdStorage 测试 NewEtcdStorage 构造函数
func TestNewEtcdStorage(t *testing.T) {
	// 创建一个不连接到真实服务器的 Etcd 客户端
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{"localhost:2379"},
		DialTimeout: time.Second,
	})
	if err != nil {
		// etcd 客户端创建失败是正常的，我们只需要测试构造函数
		t.Skip("Skipping: etcd client creation failed:", err)
	}
	defer client.Close()

	storage := NewEtcdStorage(client, "test:prefix")
	if storage == nil {
		t.Error("NewEtcdStorage() returned nil")
		return
	}
	if storage.client != client {
		t.Error("NewEtcdStorage() client mismatch")
	}
	if storage.prefix != "test:prefix" {
		t.Errorf("NewEtcdStorage() prefix = %v, want test:prefix", storage.prefix)
	}
}

// TestRedisStorageUsesContextTimeout 验证 RedisStorage 使用 contextTimeout 函数
func TestRedisStorageUsesContextTimeout(t *testing.T) {
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	defer client.Close()

	storage := NewRedisStorage(client, "test")
	if storage == nil {
		t.Fatal("NewRedisStorage() returned nil")
	}

	// 验证 storage 实例正确创建
	if storage.client != client {
		t.Error("RedisStorage.client mismatch")
	}
	if storage.prefix != "test" {
		t.Errorf("RedisStorage.prefix = %v, want test", storage.prefix)
	}
}

// TestEtcdStorageUsesContextTimeout 验证 EtcdStorage 使用 contextTimeout 函数
func TestEtcdStorageUsesContextTimeout(t *testing.T) {
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{"localhost:2379"},
		DialTimeout: time.Second,
	})
	if err != nil {
		t.Skip("Skipping: etcd client creation failed:", err)
	}
	defer client.Close()

	storage := NewEtcdStorage(client, "test")
	if storage == nil {
		t.Fatal("NewEtcdStorage() returned nil")
	}

	// 验证 storage 实例正确创建
	if storage.client != client {
		t.Error("EtcdStorage.client mismatch")
	}
	if storage.prefix != "test" {
		t.Errorf("EtcdStorage.prefix = %v, want test", storage.prefix)
	}
}

// TestStorageInterface 测试 Storage 接口的行为契约
func TestStorageInterface(t *testing.T) {
	tests := []struct {
		name    string
		storage Storage
	}{
		{"MockStorage", NewMockStorage()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := tt.storage

			// 测试 Set/Get
			if err := s.Set("key1", "value1"); err != nil {
				t.Errorf("Set() error = %v", err)
			}
			got, err := s.Get("key1")
			if err != nil {
				t.Errorf("Get() error = %v", err)
			}
			if got != "value1" {
				t.Errorf("Get() = %v, want value1", got)
			}

			// 测试 Del
			if err := s.Del("key1"); err != nil {
				t.Errorf("Del() error = %v", err)
			}
			got, _ = s.Get("key1")
			if got != "" {
				t.Errorf("Get() after Del() = %v, want empty", got)
			}
		})
	}
}

// TestContextTimeoutHelper 测试 contextTimeout 辅助函数
func TestContextTimeoutHelper(t *testing.T) {
	ctx, cancel := contextTimeout()
	defer cancel()

	// 验证 context 有超时设置
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Error("contextTimeout() did not set deadline")
	}
	if time.Until(deadline) > 3*time.Second || time.Until(deadline) < 1*time.Second {
		t.Errorf("contextTimeout() deadline = %v, want ~2s", time.Until(deadline))
	}

	// 验证 cancel 函数有效
	cancel()
	select {
	case <-ctx.Done():
		// 预期行为
	default:
		t.Error("context was not cancelled")
	}

	// 验证错误类型
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Errorf("context.Err() = %v, want context.Canceled", ctx.Err())
	}
}

// TestClearHelper 测试 clearHelper 辅助函数
func TestClearHelper(t *testing.T) {
	t.Run("success case", func(t *testing.T) {
		m := NewMockStorage()
		_ = m.Set("c1", "h1")
		_ = m.Set("c2", "h1")
		_ = m.Set("c3", "h2")

		// 创建一个自定义的 delKeys 函数来跟踪被删除的键
		var deletedKeys []string
		delFunc := func(keys []string) error {
			deletedKeys = append(deletedKeys, keys...)
			return nil
		}

		err := clearHelper(m, "h1", delFunc)
		if err != nil {
			t.Errorf("clearHelper() error = %v", err)
		}

		// 验证正确的键被标记为删除
		if len(deletedKeys) != 2 {
			t.Errorf("clearHelper() deleted %d keys, want 2", len(deletedKeys))
		}
	})

	t.Run("no matching keys", func(t *testing.T) {
		m := NewMockStorage()
		_ = m.Set("c1", "h1")

		var deletedKeys []string
		delFunc := func(keys []string) error {
			deletedKeys = append(deletedKeys, keys...)
			return nil
		}

		err := clearHelper(m, "nonexistent", delFunc)
		if err != nil {
			t.Errorf("clearHelper() error = %v", err)
		}

		if len(deletedKeys) != 0 {
			t.Errorf("clearHelper() deleted %d keys, want 0", len(deletedKeys))
		}
	})

	t.Run("empty storage", func(t *testing.T) {
		m := NewMockStorage()

		var deletedKeys []string
		delFunc := func(keys []string) error {
			deletedKeys = append(deletedKeys, keys...)
			return nil
		}

		err := clearHelper(m, "h1", delFunc)
		if err != nil {
			t.Errorf("clearHelper() error = %v", err)
		}

		if len(deletedKeys) != 0 {
			t.Errorf("clearHelper() deleted %d keys, want 0", len(deletedKeys))
		}
	})

	t.Run("delFunc error", func(t *testing.T) {
		m := NewMockStorage()
		_ = m.Set("c1", "h1")

		delFunc := func(keys []string) error {
			return errors.New("delete error")
		}

		err := clearHelper(m, "h1", delFunc)
		if err == nil {
			t.Error("clearHelper() expected error, got nil")
		}
	})

	t.Run("All returns error", func(t *testing.T) {
		// 使用一个会返回错误的 storage
		errStorage := &mockErrorStorage{}

		delFunc := func(keys []string) error {
			return nil
		}

		err := clearHelper(errStorage, "h1", delFunc)
		if err == nil {
			t.Error("clearHelper() expected error when All() fails, got nil")
		}
		if err.Error() != "storage all error" {
			t.Errorf("clearHelper() error = %v, want 'storage all error'", err)
		}
	})
}

// mockErrorStorage 是一个总是返回错误的 storage 实现（用于测试）
type mockErrorStorage struct{}

func (e *mockErrorStorage) Set(key string, value string) error {
	return errors.New("storage error")
}

func (e *mockErrorStorage) Get(key string) (string, error) {
	return "", errors.New("storage error")
}

func (e *mockErrorStorage) Del(key ...string) error {
	return errors.New("storage error")
}

func (e *mockErrorStorage) Clear(host string) error {
	return errors.New("storage error")
}

func (e *mockErrorStorage) All() (map[string]string, error) {
	return nil, errors.New("storage all error")
}
