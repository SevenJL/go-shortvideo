// Package mq 提供消息队列的抽象接口和一个基于 channel 的内存实现。
// 生产环境替换为 Kafka/RocketMQ 客户端时，只需实现同一接口，上层代码无需改动。
package mq

import (
	"context"
	"sync"
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
