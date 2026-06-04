// Package redisx 封装 go-redis 客户端创建，支持单机与集群透明切换。
package redisx

import (
	"strings"

	"github.com/redis/go-redis/v9"
)

// NewClient 根据地址字符串创建 UniversalClient。
// 单机:  "localhost:6379"
// 集群:  "host1:6379,host2:6379,host3:6379"（逗号分隔）
func NewClient(addr string) redis.UniversalClient {
	addrs := strings.Split(addr, ",")
	for i := range addrs {
		addrs[i] = strings.TrimSpace(addrs[i])
	}
	return redis.NewUniversalClient(&redis.UniversalOptions{
		Addrs: addrs,
	})
}
