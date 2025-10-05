# 安全说明

## 报告问题

如果发现可能影响用户或 Redis 连接安全的问题，请通过 GitHub Security
Advisories 私下报告，不要在公开 Issue 中披露可复现细节。

## 使用边界

`Cardinal` 是诊断工具，不应在未经授权的 Redis 实例上执行扫描或压测。

配置文件可能包含密码和证书路径。提交问题报告或日志时，应移除 ACL 密码、
TLS 私钥路径和服务端地址。

`--tls-insecure-skip-verify` 仅用于受控测试环境。
