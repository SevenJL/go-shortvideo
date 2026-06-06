// Package mq 提供消息队列的抽象接口和一个基于 channel 的内存实现。
// 生产环境替换为 Kafka/RocketMQ 客户端时，只需实现同一接口，上层代码无需改动。
package mq

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Producer 向指定 topic 发送消息。
type Producer interface {
	Publish(ctx context.Context, topic string, payload []byte) error
}

// Handler 处理单条消息；返回非 nil 错误时 MQ 应重试（至少一次语义）。
type Handler func(ctx context.Context, payload []byte) error

// Consumer 订阅 topic 并驱动 Handler。
type Consumer interface {
	Subscribe(topic string, h Handler)
	Run(ctx context.Context) error
}

// ---------------------------------------------------------------
// ChanBus：基于 channel 的内存实现，用于开发/测试
// ---------------------------------------------------------------

type message struct {
	topic   string
	payload []byte
}

// ChanBus 同时实现 Producer 和 Consumer。
type ChanBus struct {
	ch       chan message
	mu       sync.RWMutex
	handlers map[string]Handler
}

// NewChanBus 创建一个缓冲大小为 buf 的内存总线。
func NewChanBus(buf int) *ChanBus {
	return &ChanBus{
		ch:       make(chan message, buf),
		handlers: make(map[string]Handler),
	}
}

// ---------------------------------------------------------------
// RedisStreamBus：基于 Redis Streams 的持久化实现，用于生产过渡
// ---------------------------------------------------------------

type RedisStreamBus struct {
	rdb      redis.UniversalClient
	group    string
	consumer string
	maxRetry int64
	mu       sync.RWMutex
	handlers map[string]Handler
}

func NewRedisStreamBus(rdb redis.UniversalClient, group string, maxRetry int64) *RedisStreamBus {
	if group == "" {
		group = "shortvideo"
	}
	if maxRetry <= 0 {
		maxRetry = 3
	}
	host, _ := os.Hostname()
	if host == "" {
		host = "consumer"
	}
	return &RedisStreamBus{
		rdb: rdb, group: group, consumer: fmt.Sprintf("%s-%d", host, time.Now().UnixNano()),
		maxRetry: maxRetry, handlers: make(map[string]Handler),
	}
}

func (b *RedisStreamBus) Publish(ctx context.Context, topic string, payload []byte) error {
	return b.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: topic,
		Values: map[string]interface{}{"payload": payload, "attempts": int64(0)},
	}).Err()
}

func (b *RedisStreamBus) Subscribe(topic string, h Handler) {
	b.mu.Lock()
	b.handlers[topic] = h
	b.mu.Unlock()
}

func (b *RedisStreamBus) Run(ctx context.Context) error {
	b.mu.RLock()
	topics := make([]string, 0, len(b.handlers))
	for topic := range b.handlers {
		topics = append(topics, topic)
	}
	b.mu.RUnlock()

	var wg sync.WaitGroup
	for _, topic := range topics {
		_ = b.rdb.XGroupCreateMkStream(ctx, topic, b.group, "0").Err()
		wg.Add(1)
		go func(topic string) {
			defer wg.Done()
			b.runTopic(ctx, topic)
		}(topic)
	}
	<-ctx.Done()
	wg.Wait()
	return ctx.Err()
}

func (b *RedisStreamBus) runTopic(ctx context.Context, topic string) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		res, err := b.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group: b.group, Consumer: b.consumer,
			Streams: []string{topic, ">"}, Count: 10, Block: 2 * time.Second,
		}).Result()
		if err == redis.Nil {
			continue
		}
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		for _, stream := range res {
			for _, msg := range stream.Messages {
				b.handleMessage(ctx, topic, msg)
			}
		}
	}
}

func (b *RedisStreamBus) handleMessage(ctx context.Context, topic string, msg redis.XMessage) {
	b.mu.RLock()
	h := b.handlers[topic]
	b.mu.RUnlock()
	if h == nil {
		_ = b.rdb.XAck(ctx, topic, b.group, msg.ID).Err()
		return
	}
	payload := payloadBytes(msg.Values["payload"])
	attempts := int64Value(msg.Values["attempts"])
	if err := h(ctx, payload); err == nil {
		_ = b.rdb.XAck(ctx, topic, b.group, msg.ID).Err()
		return
	}
	if attempts+1 >= b.maxRetry {
		_ = b.rdb.XAdd(ctx, &redis.XAddArgs{
			Stream: topic + ":dlq",
			Values: map[string]interface{}{"payload": payload, "attempts": attempts + 1, "source_id": msg.ID},
		}).Err()
		_ = b.rdb.XAck(ctx, topic, b.group, msg.ID).Err()
		return
	}
	_ = b.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: topic,
		Values: map[string]interface{}{"payload": payload, "attempts": attempts + 1},
	}).Err()
	_ = b.rdb.XAck(ctx, topic, b.group, msg.ID).Err()
}

func payloadBytes(v interface{}) []byte {
	switch p := v.(type) {
	case []byte:
		return p
	case string:
		return []byte(p)
	default:
		return nil
	}
}

func int64Value(v interface{}) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case string:
		var out int64
		_, _ = fmt.Sscan(n, &out)
		return out
	default:
		return 0
	}
}

func (b *ChanBus) Publish(_ context.Context, topic string, payload []byte) error {
	b.ch <- message{topic: topic, payload: payload}
	return nil
}

func (b *ChanBus) Subscribe(topic string, h Handler) {
	b.mu.Lock()
	b.handlers[topic] = h
	b.mu.Unlock()
}

// Run 阻塞消费，直到 ctx 取消。
func (b *ChanBus) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg := <-b.ch:
			b.mu.RLock()
			h := b.handlers[msg.topic]
			b.mu.RUnlock()
			if h != nil {
				_ = h(ctx, msg.payload) // 内存实现不重试
			}
		}
	}
}
