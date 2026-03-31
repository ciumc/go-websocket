package websocket

import (
	"fmt"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

// NewEtcdClient 使用指定的端点创建一个新的 etcd 客户端。
// 使用 5 秒的拨号超时时间初始化客户端。
//
// 参数:
//   - endpoints: 表示 etcd 服务器地址的字符串切片
//
// 返回值:
//   - *clientv3.Client: 指向创建的 etcd 客户端的指针
//   - error: 如果客户端创建失败则返回错误，否则为 nil
func NewEtcdClient(endpoints []string) (*clientv3.Client, error) {
	// 使用提供的配置创建新的 etcd 客户端
	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   endpoints,
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	return cli, nil
}

// EtcdStorage 是一个基于 etcd 的存储实现，用于操作带前缀的键值数据。
type EtcdStorage struct {
	client *clientv3.Client // etcd 客户端实例
	prefix string           // 所有键操作时使用的公共前缀
}

// NewEtcdStorage 创建一个新的 EtcdStorage 实例。
// 参数:
//   - client: 已初始化的 etcd v3 客户端
//   - prefix: 键名前缀，所有操作都会在这个前缀下进行
//
// 返回值:
//   - *EtcdStorage: 新创建的 EtcdStorage 实例
func NewEtcdStorage(client *clientv3.Client, prefix string) *EtcdStorage {
	return &EtcdStorage{
		client: client,
		prefix: prefix,
	}
}

// Set 将指定的键值对存入 etcd 中。
// 参数:
//   - key: 不带前缀的键名
//   - value: 要保存的字符串值
//
// 返回值:
//   - error: 操作过程中发生的错误（如果有）
func (s *EtcdStorage) Set(key string, value string) error {
	ctx, cancel := contextTimeout()
	defer cancel()
	_, err := s.client.Put(ctx, s.prefix+key, value)
	if err != nil {
		return fmt.Errorf("etcd put key %s: %w", s.prefix+key, err)
	}
	return nil
}

// Get 获取指定键对应的值。
// 参数:
//   - key: 不带前缀的键名
//
// 返回值:
//   - string: 对应的值；如果键不存在则返回空字符串
//   - error: 操作过程中发生的错误（如果有）
func (s *EtcdStorage) Get(key string) (string, error) {
	ctx, cancel := contextTimeout()
	defer cancel() // 确保 cancel 总是被调用，避免资源泄漏
	resp, err := s.client.Get(ctx, s.prefix+key)
	if err != nil {
		return "", fmt.Errorf("etcd get key %s: %w", s.prefix+key, err)
	}
	if len(resp.Kvs) == 0 {
		return "", nil // key 不存在返回空字符串
	}
	return string(resp.Kvs[0].Value), nil
}

// Del 删除一个或多个键。
// 参数:
//   - keys: 需要删除的一个或多个不带前缀的键名
//
// 返回值:
//   - error: 操作过程中发生的错误（如果有）
//
// 注意: 此方法逐个删除键，如果中途出错，已删除的键无法恢复。
// 对于需要原子性删除的场景，请使用 etcd 的事务 API。
func (s *EtcdStorage) Del(keys ...string) error {
	ctx, cancel := contextTimeout()
	defer cancel()
	for _, key := range keys {
		_, err := s.client.Delete(ctx, s.prefix+key)
		if err != nil {
			return fmt.Errorf("etcd delete key %s: %w", s.prefix+key, err)
		}
	}
	return nil
}

// Clear 根据主机标识清理其下的所有相关键。
// 参数:
//   - host: 主机标识符，将删除匹配该主机的所有键
//
// 返回值:
//   - error: 操作过程中发生的错误（如果有）
func (s *EtcdStorage) Clear(host string) error {
	return clearHelper(s, host, s.delKeys)
}

// delKeys 是 EtcdStorage 特有的删除键的实现。
// 逐个删除键，因为 etcd 不支持批量删除。
// 参数:
//   - keys: 要删除的键列表
//
// 返回值:
//   - error: 删除过程中发生的错误（如果有）
func (s *EtcdStorage) delKeys(keys []string) error {
	ctx, cancel := contextTimeout()
	defer cancel()
	for _, key := range keys {
		if _, err := s.client.Delete(ctx, s.prefix+key); err != nil {
			return fmt.Errorf("etcd delete key %s: %w", s.prefix+key, err)
		}
	}
	return nil
}

// All 获取当前前缀下的所有键值对。
// 注意：返回的键已移除前缀，与 Get/Set 方法的键保持一致。
// 返回值:
//   - map[string]string: 包含所有键值对的映射表（键不含前缀）
//   - error: 操作过程中发生的错误（如果有）
func (s *EtcdStorage) All() (map[string]string, error) {
	result := make(map[string]string)
	ctx, cancel := contextTimeout()
	defer cancel() // 确保 cancel 总是被调用，避免资源泄漏
	resp, err := s.client.Get(ctx, s.prefix, clientv3.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("etcd get all with prefix %s: %w", s.prefix, err)
	}
	// 遍历响应中的键值对并填充到结果中
	// 移除前缀以保持与 Get/Set 方法的行为一致
	for _, kv := range resp.Kvs {
		key := string(kv.Key)
		// 移除前缀，确保返回的键与 Get/Set 使用的键一致
		if len(key) > len(s.prefix) {
			key = key[len(s.prefix):]
		}
		result[key] = string(kv.Value)
	}
	return result, nil
}
