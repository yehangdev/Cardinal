# Cardinal

`Cardinal` 是一个 Redis 命令行诊断工具，用于检查连接、查看键、遍历键空间、
测量命令延迟，以及对比缓存旁路行为。

当前版本：`v1.0.0-rc1`。

## 功能

- 单机、Unix、Sentinel 和 Cluster
- TLS 和 ACL 认证
- SCAN、延迟、流水线和缓存旁路
- 文本、JSON 和 Prometheus 输出

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
