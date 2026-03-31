package websocket

import (
	"maps"
	"testing"
)

// MockEtcdStorage 是 EtcdStorage 的 mock 实现
type MockEtcdStorage struct {
	data map[string]string
}

func NewMockEtcdStorage() *MockEtcdStorage {
	return &MockEtcdStorage{
		data: make(map[string]string),
	}
}

func (m *MockEtcdStorage) Set(key string, value string) error {
	m.data[key] = value
	return nil
}

func (m *MockEtcdStorage) Get(key string) (string, error) {
	return m.data[key], nil
}

func (m *MockEtcdStorage) Del(key ...string) error {
	for _, k := range key {
		delete(m.data, k)
	}
	return nil
}

func (m *MockEtcdStorage) Clear(host string) error {
	return clearHelper(m, host, m.delKeys)
}

func (m *MockEtcdStorage) delKeys(keys []string) error {
	for _, k := range keys {
		delete(m.data, k)
	}
	return nil
}

func (m *MockEtcdStorage) All() (map[string]string, error) {
	return maps.Clone(m.data), nil
}

// TestMockEtcdStorage 测试 mock etcd 存储实现
func TestMockEtcdStorage(t *testing.T) {
	storage := NewMockEtcdStorage()

	t.Run("Set and Get", func(t *testing.T) {
		err := storage.Set("key1", "value1")
		if err != nil {
			t.Fatalf("Set failed: %v", err)
		}

		val, err := storage.Get("key1")
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if val != "value1" {
			t.Errorf("Get = %v, want value1", val)
		}
	})

	t.Run("Get non-existent", func(t *testing.T) {
		val, err := storage.Get("nonexistent")
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if val != "" {
			t.Errorf("Get = %v, want empty", val)
		}
	})

	t.Run("Del", func(t *testing.T) {
		storage.Set("del_key", "value")
		err := storage.Del("del_key")
		if err != nil {
			t.Fatalf("Del failed: %v", err)
		}

		val, _ := storage.Get("del_key")
		if val != "" {
			t.Error("Key should be deleted")
		}
	})

	t.Run("Clear", func(t *testing.T) {
		storage.Set("clear1", "host1:8080")
		storage.Set("clear2", "host1:8080")
		storage.Set("clear3", "host2:8080")

		err := storage.Clear("host1:8080")
		if err != nil {
			t.Fatalf("Clear failed: %v", err)
		}

		if v, _ := storage.Get("clear1"); v != "" {
			t.Error("clear1 should be deleted")
		}
		if v, _ := storage.Get("clear2"); v != "" {
			t.Error("clear2 should be deleted")
		}
		if v, _ := storage.Get("clear3"); v != "host2:8080" {
			t.Error("clear3 should still exist")
		}
	})

	t.Run("All", func(t *testing.T) {
		storage := NewMockEtcdStorage()
		storage.Set("all1", "val1")
		storage.Set("all2", "val2")

		all, err := storage.All()
		if err != nil {
			t.Fatalf("All failed: %v", err)
		}
		if len(all) != 2 {
			t.Errorf("All returned %d items, want 2", len(all))
		}
	})
}

// TestEtcdStorageImplementsInterface 验证 MockEtcdStorage 实现 Storage 接口
func TestEtcdStorageImplementsInterface(t *testing.T) {
	var _ Storage = NewMockEtcdStorage()
}
