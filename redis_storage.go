package websocket

import (
	"context"
	"errors"

	"github.com/go-redis/redis/v8"
)

// NewRedisClient 创建一个新的Redis客户端实例
// 参数:
//
//	opt - Redis连接选项配置
//
// 返回值:
//
//	*redis.Client - Redis客户端实例指针
//	error - 连接错误信息，如果连接成功则为nil
func NewRedisClient(opt *redis.Options) (*redis.Client, error) {

	client := redis.NewClient(opt)

	// 验证Redis连接是否正常
	if _, err := client.Ping(context.Background()).Result(); err != nil {
		return nil, err
	}

	return client, nil
}

// RedisStorage implements the Storage interface using Redis.
// It uses a Redis hash to store key-value pairs under a specified prefix.
type RedisStorage struct {
	// client is the Redis client used to perform operations
	client *redis.Client

	// prefix is the Redis key prefix for the hash used to store data
	prefix string
}

// NewRedisStorage 使用给定的 Redis 客户端和键前缀创建一个新的 RedisStorage 实例。
// 参数:
//   - redisClient: Redis 客户端实例
//   - prefix: Redis 哈希键的前缀
//
// 返回值:
//   - *RedisStorage: 新创建的 RedisStorage 实例
func NewRedisStorage(redisClient *redis.Client, prefix string) *RedisStorage {
	return &RedisStorage{
		client: redisClient,
		prefix: prefix,
	}
}

// Set 在 Redis 中存储键值对，使用 2 秒超时。
// 使用 HSet 将数据存储在 Redis 哈希中。
// 参数:
//   - key: 要存储的键
//   - value: 要存储的值
//
// 返回值:
//   - error: 操作过程中发生的错误（如果有）
func (s *RedisStorage) Set(key string, value string) error {
	ctx, cancel := contextTimeout()
	defer cancel()
	return s.client.HSet(ctx, s.prefix, key, value).Err()
}

// Get 从 Redis 检索键对应的值，使用 2 秒超时。
// 使用 HGet 从 Redis 哈希中检索数据。
// 如果键不存在，返回空字符串且不返回错误。
// 参数:
//   - key: 要检索的键
//
// 返回值:
//   - string: 对应的值；如果键不存在则返回空字符串
//   - error: 操作过程中发生的错误（如果有）
func (s *RedisStorage) Get(key string) (string, error) {
	ctx, cancel := contextTimeout()
	value, err := s.client.HGet(ctx, s.prefix, key).Result()
	cancel()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	return value, err
}

// Del 从 Redis 中删除一个或多个键，使用 2 秒超时。
// 使用 HDel 从 Redis 哈希中删除字段。
// 如果键不存在，不返回错误。
// 参数:
//   - key: 要删除的一个或多个键
//
// 返回值:
//   - error: 操作过程中发生的错误（如果有）
func (s *RedisStorage) Del(key ...string) error {
	ctx, cancel := contextTimeout()
	defer cancel()
	return s.client.HDel(ctx, s.prefix, key...).Err()
}

// Clear 从 Redis 中删除特定主机的所有条目，使用 2 秒超时。
// 首先检索所有条目，识别匹配主机的条目并删除它们。
// 用于服务器节点关闭时清理。
// 参数:
//   - host: 要清理的主机标识符
//
// 返回值:
//   - error: 操作过程中发生的错误（如果有）
func (s *RedisStorage) Clear(host string) error {
	return clearHelper(s, host, s.delKeys)
}

// delKeys 是 RedisStorage 特有的删除键的实现。
// 使用 HDel 命令一次性删除多个字段。
// 参数:
//   - keys: 要删除的键列表
//
// 返回值:
//   - error: 操作过程中发生的错误（如果有）
func (s *RedisStorage) delKeys(keys []string) error {
	ctx, cancel := contextTimeout()
	defer cancel()
	return s.client.HDel(ctx, s.prefix, keys...).Err()
}

// All 从 Redis 检索所有键值对，使用 2 秒超时。
// 使用 HGetAll 从 Redis 哈希中检索所有字段和值。
// 如果没有条目存在，返回 nil 且不返回错误。
// 返回值:
//   - map[string]string: 包含所有键值对的映射表
//   - error: 操作过程中发生的错误（如果有）
func (s *RedisStorage) All() (map[string]string, error) {
	ctx, cancel := contextTimeout()
	values, err := s.client.HGetAll(ctx, s.prefix).Result()
	cancel()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	return values, err
}
