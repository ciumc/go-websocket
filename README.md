# go-websocket

分布式 WebSocket 服务框架，基于 Go 语言实现。支持单节点和分布式部署，通过 Redis/Etcd 存储客户端连接信息，利用 gRPC 实现跨节点消息传递。

## 核心特性

- **统一 Session API** - 自动检测单节点/分布式模式，简化使用
- **可配置化** - Hub 级别默认配置 + Session 级别覆盖
- **单节点/分布式部署** - 灵活支持从单机到集群的扩展
- **多种存储后端** - 支持 Redis 和 Etcd 作为连接信息存储
- **gRPC 节点通信** - 高效的跨节点消息传递机制，内置连接池
- **事件驱动架构** - 丰富的回调函数支持（连接、消息、错误、断开）
- **并发安全** - 基于 RWMutex 和 sync.Map 的高并发设计

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
| **Hub** | 中央消息代理，管理活跃客户端连接，内置配置和 WebSocket 升级器 |
| **Session** | 统一会话入口，自动检测单节点/分布式模式 |
| **DistClient** | 分布式客户端，用于跨节点发送消息，内置连接池 |
| **DistServer** | gRPC 服务端，处理来自其他节点的请求 |
| **Storage** | 存储接口抽象，支持 Redis/Etcd 实现 |

## 安装

### 环境要求

- Go 1.24+
- Redis 或 Etcd（分布式模式需要）

### 安装命令

```bash
go get github.com/ciumc/go-websocket
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
    websocket "github.com/ciumc/go-websocket"
)

func main() {
    // 创建并启动 Hub（使用默认配置）
    hub := websocket.NewHubRun()
    defer hub.Close()

    // 或使用自定义配置
    // hub := websocket.NewHubRunWithConfig(
    //     websocket.WithWriteWait(5*time.Second),
    //     websocket.WithPongWait(30*time.Second),
    // )

    // 创建 HTTP 服务
    app := gin.Default()

    // WebSocket 路由
    app.GET("/ws", func(ctx *gin.Context) {
        // 创建 Session（自动检测模式）
        session := websocket.NewSession(hub,
            websocket.WithSessionID("user-123"),
        )

        // 设置连接回调
        session.OnConnect(func(conn *websocket.Client) {
            log.Printf("客户端已连接: %s", conn.GetID())
        })

        // 设置消息处理回调
        session.OnEvent(func(conn *websocket.Client, messageType int, message []byte) {
            log.Printf("收到消息: %s", string(message))
            conn.Emit([]byte(time.Now().Format(time.RFC3339)))
        })

        // 设置断开连接回调
        session.OnDisconnect(func(id string) {
            log.Printf("客户端已断开: %s", id)
        })

        // 建立 WebSocket 连接
        if err := session.Conn(ctx.Writer, ctx.Request); err != nil {
            ctx.String(http.StatusInternalServerError, "连接失败")
            return
        }
    })

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

    websocket "github.com/ciumc/go-websocket"
    "github.com/ciumc/go-websocket/websocketpb"
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
    defer distClient.Close() // 记得关闭以清理连接池

    // 启动 gRPC 服务
    serverGroup.Go(func() error {
        lis, err := net.Listen("tcp", grpcAddr)
        if err != nil {
            return err
        }

        grpcServer := grpc.NewServer(
            grpc.UnaryInterceptor(grpcrecovery.UnaryServerInterceptor()),
        )

        websocketpb.RegisterWebsocketServer(grpcServer, websocket.NewDistServer(hub))
        return grpcServer.Serve(lis)
    })

    // 启动 HTTP 服务
    serverGroup.Go(func() error {
        app := gin.Default()

        app.GET("/ws", func(ctx *gin.Context) {
            // 创建分布式 Session
            session := websocket.NewSession(hub,
                websocket.WithStorage(storage),
                websocket.WithAddr(grpcHost),
                websocket.WithSessionID("user-456"),
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
| `NewHubRunWithConfig(opts ...HubOption)` | 使用配置创建并启动 Hub |
| `Run()` | 启动 Hub 事件循环 |
| `Close()` | 优雅关闭 Hub |
| `Client(id string) (*Client, bool)` | 根据 ID 获取客户端 |
| `Broadcast(message []byte)` | 向所有客户端广播消息 |
| `Config() Config` | 返回 Hub 配置 |
| `Upgrader() *websocket.Upgrader` | 返回 WebSocket 升级器 |

### Hub 配置选项

| 选项 | 说明 | 默认值 |
|------|------|--------|
| `WithWriteWait(d time.Duration)` | 写入超时 | 10s |
| `WithPongWait(d time.Duration)` | Pong 等待超时 | 60s |
| `WithPingPeriod(d time.Duration)` | Ping 发送周期 | 54s |
| `WithMaxMessageSize(size int64)` | 最大消息大小 | 512 |
| `WithHubBufSize(size int)` | 默认发送缓冲区大小 | 256 |
| `WithHubCheckOrigin(fn)` | CORS 检查函数 | 允许所有 |

### Session

Session 是统一的 WebSocket 会话入口。

| 方法 | 说明 |
|------|------|
| `NewSession(hub *Hub, opts ...SessionOption)` | 创建会话 |
| `Conn(w http.ResponseWriter, r *http.Request) error` | 建立 WebSocket 连接 |
| `OnEvent(handler)` | 设置消息回调 |
| `OnConnect(handler)` | 设置连接回调 |
| `OnError(handler)` | 设置错误回调 |
| `OnDisconnect(handler)` | 设置断开回调 |
| `ID() string` | 返回客户端 ID |
| `Emit(message []byte) bool` | 发送消息 |
| `Broadcast(message []byte)` | 广播消息 |
| `Client() *Client` | 返回内部 Client |

### Session 配置选项

| 选项 | 说明 |
|------|------|
| `WithSessionID(id string)` | 设置客户端 ID |
| `WithSessionBufSize(size int)` | 设置缓冲区大小（覆盖 Hub 默认值） |
| `WithSessionWriteWait(d time.Duration)` | 覆盖写入超时 |
| `WithSessionPongWait(d time.Duration)` | 覆盖 Pong 等待超时 |
| `WithSessionPingPeriod(d time.Duration)` | 覆盖 Ping 周期 |
| `WithSessionMaxMessageSize(size int64)` | 覆盖最大消息大小 |
| `WithStorage(storage Storage)` | 设置存储后端（启用分布式模式） |
| `WithAddr(addr string)` | 设置本节点地址（分布式模式必需） |

### DistClient

DistClient 用于跨节点发送消息。

| 方法 | 说明 |
|------|------|
| `NewDistClient(storage Storage, opts ...DistClientOption)` | 创建分布式客户端 |
| `Emit(ctx, id, message) (bool, error)` | 向指定客户端发送消息 |
| `Online(ctx, id) (bool, error)` | 检查客户端是否在线 |
| `Broadcast(ctx, message) (int64, error)` | 广播消息到所有节点 |
| `Close()` | 关闭客户端，清理连接池 |

### DistServer

DistServer 实现 gRPC 服务端。

| 方法 | 说明 |
|------|------|
| `NewDistServer(hub *Hub) *DistServer` | 创建 gRPC 服务端 |

### Storage 接口

```go
type Storage interface {
    Set(key string, value string) error
    Get(key string) (string, error)
    Del(key ...string) error
    Clear(host string) error
    All() (map[string]string, error)
}
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