# Cardinal

`Cardinal` 是一个 Redis 命令行诊断工具，用于检查连接、查看键、遍历大键空间、
测量命令延迟，以及对比缓存旁路命中和源站回源耗时。

当前版本：`v1.0.0`。版本历史见 [CHANGELOG.md](CHANGELOG.md)。

支持的能力包括：

- 通过 TCP 或 Unix Socket 连接单机 Redis
- 使用监控主节点名称连接 Redis Sentinel
- 连接 Redis Cluster，并控制重定向和读路由
- TLS、自定义 CA、客户端证书，以及 ACL 用户名和密码登录
- YAML 或 JSON 配置文件
- 文本、JSON 和 Prometheus 文本输出
- 可感知游标的 SCAN 报告，支持从上次游标继续
- 批量执行 `GET`、`SET`、`DEL`、`EXPIRE` 和 `INCR`
- 百分位和直方图延迟报告

## 构建

```sh
go build -o cardinal ./cmd/cardinal
```

也可以直接运行：

```sh
go run ./cmd/cardinal ping
```

## 连接参数

默认地址为 `127.0.0.1:6379`。

```sh
cardinal --addr 10.0.0.10:6379 --db 2 --timeout 3s ping
cardinal --unix-socket /run/redis/redis.sock ping
cardinal --tls --tls-ca ca.pem --username app --password "$REDIS_PASSWORD" ping
```

Sentinel：

```sh
cardinal \
  --mode sentinel \
  --master-name primary \
  --sentinel-addresses sentinel-a:26379,sentinel-b:26379 \
  --username app \
  --password "$REDIS_PASSWORD" \
  ping
```

Cluster：

```sh
cardinal \
  --mode cluster \
  --cluster-addresses node-a:6379,node-b:6379,node-c:6379 \
  --max-redirects 5 \
  --route-by-latency \
  ping
```

TLS 参数可以写在命令行，也可以写入配置文件：

```sh
cardinal \
  --tls \
  --tls-ca /etc/redis/ca.pem \
  --tls-cert /etc/redis/client.pem \
  --tls-key /etc/redis/client-key.pem \
  --tls-server-name redis.internal \
  ping
```

仅在受控测试环境中使用 `--tls-insecure-skip-verify`。

## 配置文件

配置文件使用 YAML，JSON 也可以直接使用，因为 YAML 是其超集。未知字段会导致
配置校验失败，避免拼写错误被静默忽略。

```yaml
mode: standalone
address: 127.0.0.1:6379
db: 0
username: app
password: change-me
timeout: 5s

tls:
  enabled: false
  ca_file: ""
  cert_file: ""
  key_file: ""
  server_name: ""
  insecure_skip_verify: false
```

任意命令都可以使用配置文件：

```sh
cardinal --config examples/standalone.yaml ping
cardinal --config examples/sentinel.yaml config validate
cardinal --config examples/cluster.yaml --format json config show
```

命令行参数会覆盖配置文件中的值。时长支持 Go 风格写法，例如 `250ms`、`5s`
和 `2m`，也支持 `1d`、`1w` 这类天和周后缀。单独的数字按秒解释。

## 命令

### ping

```sh
cardinal ping
```

### get

```sh
cardinal get user:42
```

### set

可选的 TTL 参数接受时长：

```sh
cardinal set user:42 active 30s
cardinal set session:abc token 1d
```

### ttl

```sh
cardinal ttl user:42
```

文本输出为 `missing`、`persistent` 或具体剩余时长。

### scan

`scan` 按 Redis 游标遍历，不会使用 `KEYS`。它会报告每次游标变化，并可在达到
最大调用次数或软性键数量限制后停止。

```sh
cardinal scan --count 500 user:
```

从报告中的游标继续：

```sh
cardinal scan --count 500 --max-iterations 1 user:
cardinal scan --count 500 --cursor 12345 user:
```

如果报告被截断，`next_cursor` 不为零，且 `complete` 为 false。键数量限制是
软限制，因为 Redis 按批次返回游标结果。

### latency

```sh
cardinal latency --count 500 get user:42
cardinal latency --count 1000 --pipeline --batch-size 100 ping
```

报告包含最小值、平均值、标准差、p50、p90、p95、p99、最大值、吞吐量和
直方图分桶。

### pipeline

流水线操作会在一次往返中发送：

```sh
cardinal pipeline get user:1 user:2 user:3
cardinal pipeline --value active --ttl 1m set user:1 user:2 user:3
cardinal pipeline del stale:1 stale:2 stale:3
cardinal pipeline --repeat 10 get hot:1 hot:2
```

支持的命令为 `get`、`set`、`del`、`expire` 和 `incr`。读取不存在的键会记为
未命中，而不是传输失败。

### cache-aside

该命令执行一次缓存查询；未命中时从模拟源站获取数据并回填缓存，然后测量下一次
命中耗时。

```sh
cardinal cache-aside --ttl 2m --origin-delay 25ms --value active user:42
cardinal cache-aside --ttl 2m --origin-delay 50ms user:42
```

键已存在时，命令报告命中并跳过源站。

### config

```sh
cardinal --config examples/standalone.yaml config validate
cardinal --config examples/sentinel.yaml config show
```

`config show` 会隐藏密码。

## 输出格式

使用 `--format json` 输出结构化结果：

```sh
cardinal --format json scan --count 100 user:
cardinal --format json latency --count 100 get user:42
cardinal --format json pipeline get user:1 user:2
```

使用 `--format prometheus` 输出 Prometheus 文本：

```sh
cardinal --format prometheus ping
cardinal --format prometheus latency --count 100 get user:42
```

指标族示例：

- `cardinal_up`
- `cardinal_scan_keys`
- `cardinal_scan_next_cursor`
- `cardinal_latency_seconds`
- `cardinal_latency_histogram_seconds`
- `cardinal_pipeline_operations_total`
- `cardinal_cache_aside_origin_saved_seconds`

命令执行一次后退出。集成方应按自身生命周期主动采集或抓取这些指标。

## 退出码

- `0`：命令执行完成
- `1`：连接、Redis、校验或输出失败
- `2`：命令行参数或配置错误

JSON 错误会写入标准错误，并带有 `ok: false`。

## 测试

```sh
go test ./...
go vet ./...
```

集成测试会启动进程内 Redis 实现，覆盖 SCAN、流水线、延迟批处理和认证连接。

也可以使用 Makefile：

```sh
make test
make check
```

## 架构

`Cardinal` 是一次性运行的运维 CLI，用于诊断 Redis 连接、键分布、延迟、流水线
和缓存旁路行为。它不是代理、缓存服务，也不是长期运行的监控代理。

```text
cmd/cardinal
  命令解析和进程退出码
      |
      v
internal/cardinal
  config       配置文件和参数归一化
  client       单机、Unix、Sentinel 和 Cluster 客户端
  scan         游标遍历和结果统计
  pipeline     流水线操作执行
  latency      耗时采样、百分位和直方图
  cacheaside   缓存查询、源站模拟、回填和命中对比
  output       文本、JSON 和 Prometheus 渲染
  errors       类型化配置错误、用法错误和 Redis 错误
```

初始化流程：

1. 解析命令行参数。
2. 在提供配置文件时加载 YAML 或 JSON。
3. 应用默认值和命令行覆盖项。
4. 为选定的拓扑构建客户端。
5. 执行一个命令。
6. 渲染结果并退出。

命令处理器返回类型化报告对象。输出层将其转换为文本、JSON 或 Prometheus
格式，因此命令行为可测试，输出命名也能保持一致。

该工具不会跨运行保存指标，不提供看板，也不尝试覆盖所有 Redis 数据类型。

## 许可证

MIT

## 安全

安全问题请通过 GitHub Security Advisories 报告，不要在公开 Issue 中披露
密码、私钥或可复现的未授权访问细节。
