package websocket

import (
	"context"
	"time"
)

// Storage 定义了存储和检索 WebSocket 客户端信息的接口。
// 在分布式 WebSocket 设置中使用，用于共享客户端位置信息。
type Storage interface {
	// Set 在存储中存储键值对。
	// 在分布式 WebSocket 中，这通常存储客户端 ID 到服务器地址的映射。
	Set(key string, value string) error

	// Get 从存储中检索键对应的值。
	// 如果键不存在，返回空字符串。
	Get(key string) (string, error)

	// Del 从存储中删除一个或多个键。
	// 如果键不存在，不应返回错误。
	Del(key ...string) error

	// Clear 从存储中删除特定主机的所有条目。
	// 用于服务器关闭时清理条目。
	Clear(host string) error

	// All 从存储中检索所有键值对。
	// 用于维护和监控目的。
	All() (map[string]string, error)
}

// contextTimeout 创建一个带有 2 秒超时的上下文。
// 用于 Storage 实现中的各种操作，确保操作不会无限期阻塞。
// 返回值:
//   - context.Context: 带有超时的上下文
//   - context.CancelFunc: 取消函数，用于提前释放资源
func contextTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 2*time.Second)
}

// delKeysFunc 是删除键的函数类型，用于 clearHelper。
// 参数:
//   - keys: 要删除的键列表
//
// 返回值:
//   - error: 删除过程中发生的错误（如果有）
type delKeysFunc func(keys []string) error

// clearHelper 是一个辅助函数，用于实现 Storage.Clear 方法。
// 它从存储中检索所有条目，找出匹配指定主机的键，然后调用 delFunc 删除它们。
// 这个函数提取了 RedisStorage 和 EtcdStorage 中 Clear 方法的公共逻辑。
// 参数:
//   - s: Storage 接口实例，用于调用 All() 方法
//   - host: 要清理的主机标识符
//   - delFunc: 删除键的具体实现函数
//
// 返回值:
//   - error: 操作过程中发生的错误（如果有）
func clearHelper(s Storage, host string, delFunc delKeysFunc) error {
	all, err := s.All()
	if err != nil {
		return err
	}
	remove := make([]string, 0, len(all))
	for id, addr := range all {
		if addr != host {
			continue
		}
		remove = append(remove, id)
	}
	if len(remove) == 0 {
		return nil
	}
	return delFunc(remove)
}
