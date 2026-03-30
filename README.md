# go-websocket

分布式 WebSocket 服务框架，基于 Go 语言实现。支持单节点和分布式部署，通过 Redis/Etcd 存储客户端连接信息，利用 gRPC 实现跨节点消息传递。

## 核心特性

- **单节点/分布式部署** - 灵活支持从单机到集群的扩展
- **多种存储后端** - 支持 Redis 和 Etcd 作为连接信息存储
- **gRPC 节点通信** - 高效的跨节点消息传递机制
- **连接池管理** - 自动管理 gRPC 连接，支持连接复用和过期清理
- **事件驱动架构** - 丰富的回调函数支持（连接、消息、错误、断开）
- **并发安全** - 基于 RWMutex 和 sync.Map 的高并发设计
- **函数式选项模式** - 灵活的配置方式

## 架构

```
┌──────────────────┐     ┌───────────────────┐    ┌───────────────┐
│  WebSocket 客户端 │────▶│ WebSocket 服务节点 │◀───▶│  Redis/Etcd  │
└──────────────────┘     └───────────────────┘    └───────────────┘
                               │      ▲
                               ▼      │
                        ┌──────────────────┐
                        │     gRPC         │
                        │   服务接口        │
                        └──────────────────┘
```

### 核心组件

| 组件 | 说明 |
|------|------|
| **Hub** | 中央消息代理，管理活跃客户端连接，处理消息广播 |
| **Client** | WebSocket 客户端连接封装，处理消息收发和生命周期 |
| **DistSession** | 分布式会话管理，自动同步连接信息到存储 |
| **DistClient** | 分布式客户端，用于跨节点发送消息 |
| **DistServer** | gRPC 服务端，处理来自其他节点的请求 |
| **Storage** | 存储接口抽象，支持 Redis/Etcd 实现 |

## 安装

### 环境要求

- Go 1.24+
- Redis 或 Etcd（分布式模式需要）

### 安装命令

```bash
go get github.com/jayecc/go-websocket
```

## 快速开始

### 单节点模式

```go
package main

import (
    "log"
    "net/http"
    "time"
    
    "github.com/gin-gonic/gin"
    websocket "github.com/jayecc/go-websocket"
)

func main() {
    // 创建并启动 Hub
    hub := websocket.NewHubRun()
    defer hub.Close()

    // 创建 HTTP 服务
    app := gin.Default()

    // WebSocket 路由
    app.GET("/ws", func(ctx *gin.Context) {
        // 创建客户端，指定自定义 ID
        client := websocket.NewClient(hub, websocket.WithID("user-123"))

        // 设置连接回调
        client.OnConnect(func(conn *websocket.Client) {
            log.Printf("客户端已连接: %s", conn.GetID())
        })

        // 设置消息处理回调
        client.OnEvent(func(conn *websocket.Client, messageType int, message []byte) {
            log.Printf("收到消息: %s", string(message))
            
            // 回复客户端
            response := time.Now().Format(time.RFC3339)
            conn.Emit([]byte(response))
        })

        // 设置断开连接回调
        client.OnDisconnect(func(id string) {
            log.Printf("客户端已断开: %s", id)
        })

        // 设置错误处理回调
        client.OnError(func(id string, err error) {
            log.Printf("客户端错误 [%s]: %v", id, err)
        })

        // 建立 WebSocket 连接
        if err := client.Conn(ctx.Writer, ctx.Request); err != nil {
            ctx.String(http.StatusInternalServerError, "连接失败")
            return
        }
    })

    // 启动服务
    log.Fatal(app.Run(":8080"))
}
```

### 分布式模式

```go
package main

import (
    "context"
    "log"
    "net"
    "net/http"
    
    "github.com/gin-gonic/gin"
    "github.com/go-redis/redis/v8"
    grpcrecovery "github.com/grpc-ecosystem/go-grpc-middleware/recovery"
    "golang.org/x/sync/errgroup"
    "google.golang.org/grpc"
    
    websocket "github.com/jayecc/go-websocket"
    "github.com/jayecc/go-websocket/websocketpb"
)

func main() {
    serverGroup := errgroup.Group{}
    
    // 配置地址
    grpcAddr := ":8081"
    httpAddr := ":8080"
    grpcHost := websocket.IP().String() + grpcAddr
    
    // 创建 Redis 客户端
    redisClient := redis.NewClient(&redis.Options{
        Addr:     "localhost:6379",
        Password: "",
        DB:       0,
    })
    
    // 创建存储层
    storage := websocket.NewRedisStorage(redisClient, "websocket")
    
    // 创建 Hub
    hub := websocket.NewHubRun()
    defer hub.Close()
    
    // 创建分布式客户端（用于跨节点通信）
    distClient := websocket.NewDistClient(storage)

    // 启动 gRPC 服务
    serverGroup.Go(func() error {
        lis, err := net.Listen("tcp", grpcAddr)
        if err != nil {
            return err
        }
        
        grpcServer := grpc.NewServer(
            grpc.UnaryInterceptor(grpcrecovery.UnaryServerInterceptor()),
        )
        
        // 注册 gRPC 服务
        websocketpb.RegisterWebsocketServer(grpcServer, websocket.NewDistServer(hub))
        return grpcServer.Serve(lis)
    })

    // 启动 HTTP 服务
    serverGroup.Go(func() error {
        app := gin.Default()
        
        app.GET("/ws", func(ctx *gin.Context) {
            // 创建分布式会话
            session := websocket.NewDistSession(
                hub, 
                storage, 
                grpcHost,
                websocket.WithID("user-456"),
            )
            
            session.OnConnect(func(conn *websocket.Client) {
                log.Printf("分布式客户端已连接: %s", conn.GetID())
            })
            
            session.OnEvent(func(conn *websocket.Client, messageType int, message []byte) {
                log.Printf("收到消息: %s", string(message))
                
                // 广播到所有节点
                count, err := distClient.Broadcast(context.Background(), message)
                log.Printf("广播结果: count=%d, err=%v", count, err)
            })
            
            session.OnDisconnect(func(id string) {
                log.Printf("分布式客户端已断开: %s", id)
            })
            
            if err := session.Conn(ctx.Writer, ctx.Request); err != nil {
                ctx.String(http.StatusInternalServerError, "连接失败")
                return
            }
        })
        
        return app.Run(httpAddr)
    })
    
    log.Fatal(serverGroup.Wait())
}
```

## 核心 API

### Hub

Hub 是中央消息代理，管理所有活跃的客户端连接。

| 方法 | 说明 |
|------|------|
| `NewHub()` | 创建 Hub 实例（需手动调用 Run） |
| `NewHubRun()` | 创建并启动 Hub 实例 |
| `Run()` | 启动 Hub 事件循环 |
| `Close()` | 优雅关闭 Hub |
| `Client(id string) (*Client, bool)` | 根据 ID 获取客户端 |
| `Broadcast(message []byte)` | 向所有客户端广播消息 |

### Client

Client 表示单个 WebSocket 客户端连接。

| 方法 | 说明 |
|------|------|
| `NewClient(hub *Hub, opts ...Option)` | 创建客户端 |
| `Conn(w http.ResponseWriter, r *http.Request) error` | 建立 WebSocket 连接 |
| `Emit(message []byte) bool` | 向客户端发送消息 |
| `Broadcast(message []byte)` | 广播消息到所有客户端 |
| `GetID() string` | 获取客户端 ID |

#### Client 回调函数

| 方法 | 说明 |
|------|------|
| `OnEvent(handler func(conn *Client, messageType int, message []byte))` | 消息接收回调 |
| `OnConnect(handler func(conn *Client))` | 连接建立回调 |
| `OnDisconnect(handler func(id string))` | 连接断开回调 |
| `OnError(handler func(id string, err error))` | 错误处理回调 |

### Storage 接口

Storage 定义了连接信息存储的抽象接口。

```go
type Storage interface {
    Set(key string, value string) error
    Get(key string) (string, error)
    Del(key ...string) error
    Clear(host string) error
    All() (map[string]string, error)
}
```

#### RedisStorage

```go
// 创建 Redis 客户端
redisClient, err := websocket.NewRedisClient(&redis.Options{
    Addr:     "localhost:6379",
    Password: "",
    DB:       0,
})

// 创建 Redis 存储
storage := websocket.NewRedisStorage(redisClient, "websocket")
```

#### EtcdStorage

```go
// 创建 Etcd 客户端
etcdClient, err := websocket.NewEtcdClient([]string{"localhost:2379"})

// 创建 Etcd 存储
storage := websocket.NewEtcdStorage(etcdClient, "websocket/")
```

### DistSession

DistSession 是分布式模式下的会话管理器，自动处理连接信息的存储和清理。

| 方法 | 说明 |
|------|------|
| `NewDistSession(hub *Hub, storage Storage, addr string, opts ...Option)` | 创建分布式会话 |
| `Conn(w http.ResponseWriter, r *http.Request) error` | 建立连接 |
| `Client() *Client` | 获取内部 Client 实例 |

回调函数与 Client 相同：`OnEvent`、`OnConnect`、`OnDisconnect`、`OnError`

### DistClient

DistClient 用于在分布式系统中跨节点发送消息。

| 方法 | 说明 |
|------|------|
| `NewDistClient(storage Storage, opts ...DistClientOption)` | 创建分布式客户端 |
| `Emit(ctx context.Context, id string, message []byte) (bool, error)` | 向指定客户端发送消息 |
| `Online(ctx context.Context, id string) (bool, error)` | 检查客户端是否在线 |
| `Broadcast(ctx context.Context, message []byte) (int64, error)` | 广播消息到所有节点 |

### DistServer

DistServer 实现 gRPC 服务端，处理来自其他节点的请求。

| 方法 | 说明 |
|------|------|
| `NewDistServer(hub *Hub) *DistServer` | 创建 gRPC 服务端 |

## 配置选项

### Client Option

| 选项 | 说明 | 示例 |
|------|------|------|
| `WithID(id string)` | 设置客户端 ID | `WithID("user-123")` |
| `WithBufSize(size int)` | 设置发送缓冲区大小 | `WithBufSize(512)` |
| `WithCheckOrigin(fn func(r *http.Request) bool)` | 自定义跨域检查 | `WithCheckOrigin(func(r *http.Request) bool { return true })` |

### DistClient Option

| 选项 | 说明 | 示例 |
|------|------|------|
| `WithDistTimeout(timeout time.Duration)` | 设置 gRPC 调用超时 | `WithDistTimeout(5 * time.Second)` |

### WebSocket 常量

| 常量 | 默认值 | 说明 |
|------|--------|------|
| `writeWait` | 10s | 写入超时时间 |
| `pongWait` | 60s | 读取 pong 消息超时 |
| `pingPeriod` | 54s | ping 发送周期（pongWait * 9/10） |
| `maxMessageSize` | 512 | 最大消息大小（字节） |
| `bufSize` | 256 | 发送缓冲区大小 |

## 工具函数

### IP()

获取本机本地 IP 地址（非回环 IPv4）。

```go
ip := websocket.IP()
fmt.Println(ip.String()) // 例如: 192.168.1.100
```

## 依赖

| 包 | 用途 |
|----|------|
| `github.com/gorilla/websocket` | WebSocket 连接处理 |
| `github.com/gin-gonic/gin` | HTTP 路由（示例中使用） |
| `github.com/go-redis/redis/v8` | Redis 客户端 |
| `go.etcd.io/etcd/client/v3` | Etcd 客户端 |
| `google.golang.org/grpc` | gRPC 通信 |
| `github.com/google/uuid` | 客户端 ID 生成 |
| `golang.org/x/sync/errgroup` | 并发服务管理 |

## License

MIT
