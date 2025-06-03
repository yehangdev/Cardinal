# Cardinal

`Cardinal` 是一个 Redis 命令行诊断工具，用于检查连接、查看键、遍历键空间、
测量命令延迟，以及对比缓存旁路行为。

当前版本：`早期开发版`。

## 功能

- 单机 Redis 连接
- PING 健康检查
- 统一的用法错误和配置错误

## 构建

```sh
go build -o cardinal ./cmd/cardinal
go test ./...
```

## 示例

```sh
cardinal --addr 127.0.0.1:6379 ping
cardinal --config examples/standalone.yaml config validate
```

## 许可证

MIT
