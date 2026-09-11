package eventlog

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Event 是对外展示的结构化事件，不包含用户名、凭据或认证载荷。
type Event struct {
	Sequence      uint64     `json:"sequence"`
	At            time.Time  `json:"at"`
	AccountID     string     `json:"account_id,omitempty"`
	State         string     `json:"state,omitempty"`
	Operation     string     `json:"operation,omitempty"`
	Category      string     `json:"category,omitempty"`
	Message       string     `json:"message,omitempty"`
	RetryCount    int        `json:"retry_count,omitempty"`
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"`
	Dropped       uint64     `json:"dropped,omitempty"`
}

// Store 保存有界近期事件，并向订阅者非阻塞地发布新事件。
type Store struct {
	mu             sync.RWMutex
	capacity       int
	events         []Event
	nextSequence   uint64
	subscribers    map[uint64]*subscriber
	nextSubscriber uint64
}

type subscriber struct {
	channel chan Event
	dropped uint64
}

// Subscription 是一个可取消的事件订阅。
type Subscription struct {
	Events <-chan Event
	cancel func()
	once   sync.Once
}

// Cancel 停止订阅并关闭事件通道。
func (subscription *Subscription) Cancel() {
	if subscription == nil {
		return
	}
	subscription.once.Do(subscription.cancel)
}

// New 创建一个有界事件存储。
func New(capacity int) (*Store, error) {
	if capacity <= 0 {
		return nil, errors.New("事件缓冲区大小必须大于 0")
	}
	return &Store{
		capacity:    capacity,
		subscribers: make(map[uint64]*subscriber),
	}, nil
}

// Append 保存并发布事件，调用方不会因订阅者读取缓慢而阻塞。
func (store *Store) Append(event Event) Event {
	store.mu.Lock()
	defer store.mu.Unlock()

	store.nextSequence++
	event.Sequence = store.nextSequence
	if event.At.IsZero() {
		event.At = time.Now()
	}
	if len(store.events) == store.capacity {
		copy(store.events, store.events[1:])
		store.events = store.events[:store.capacity-1]
	}
	store.events = append(store.events, cloneEvent(event))

	for _, subscriber := range store.subscribers {
		store.publishLocked(subscriber, event)
	}
	return cloneEvent(event)
}

// Resize 调整近期事件容量并保留最新事件。
func (store *Store) Resize(capacity int) error {
	if capacity <= 0 {
		return errors.New("事件缓冲区大小必须大于 0")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.events) > capacity {
		start := len(store.events) - capacity
		resized := make([]Event, capacity)
		copy(resized, store.events[start:])
		store.events = resized
	}
	store.capacity = capacity
	return nil
}

// Recent 返回最近的 limit 条事件；limit 小于等于 0 时返回全部保留事件。
func (store *Store) Recent(limit int) []Event {
	store.mu.RLock()
	defer store.mu.RUnlock()

	start := 0
	if limit > 0 && limit < len(store.events) {
		start = len(store.events) - limit
	}
	result := make([]Event, len(store.events)-start)
	for index := range result {
		result[index] = cloneEvent(store.events[start+index])
	}
	return result
}

// Wait 等待序号大于 after 的事件；已有事件优先返回，ctx 取消时返回上下文错误。
func (store *Store) Wait(ctx context.Context, after uint64, limit int) ([]Event, error) {
	if ctx == nil {
		return nil, errors.New("等待事件上下文不能为空")
	}
	if limit < 0 {
		return nil, errors.New("等待事件数量不能小于 0")
	}
	subscription, err := store.Subscribe(32)
	if err != nil {
		return nil, err
	}
	defer subscription.Cancel()
	if events := eventsAfter(store.Recent(0), after, limit); len(events) > 0 {
		return events, nil
	}

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case _, open := <-subscription.Events:
			if !open {
				return nil, errors.New("事件订阅已关闭")
			}
			if events := eventsAfter(store.Recent(0), after, limit); len(events) > 0 {
				return events, nil
			}
		}
	}
}

func eventsAfter(events []Event, after uint64, limit int) []Event {
	filtered := make([]Event, 0, len(events))
	for _, event := range events {
		if event.Sequence > after {
			filtered = append(filtered, event)
		}
	}
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[len(filtered)-limit:]
	}
	return filtered
}

// Subscribe 创建一个非阻塞订阅。历史事件通过 Recent 单独获取。
func (store *Store) Subscribe(buffer int) (*Subscription, error) {
	if buffer <= 0 {
		return nil, errors.New("订阅缓冲区大小必须大于 0")
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	store.nextSubscriber++
	id := store.nextSubscriber
	subscriber := &subscriber{channel: make(chan Event, buffer)}
	store.subscribers[id] = subscriber
	return &Subscription{
		Events: subscriber.channel,
		cancel: func() {
			store.mu.Lock()
			defer store.mu.Unlock()
			if current, exists := store.subscribers[id]; exists {
				delete(store.subscribers, id)
				close(current.channel)
			}
		},
	}, nil
}

func (store *Store) publishLocked(subscriber *subscriber, event Event) {
	select {
	case subscriber.channel <- cloneEvent(event):
		return
	default:
	}

	select {
	case <-subscriber.channel:
		subscriber.dropped++
	default:
	}
	event.Dropped = subscriber.dropped
	select {
	case subscriber.channel <- cloneEvent(event):
	default:
	}
}

func cloneEvent(event Event) Event {
	if event.NextAttemptAt != nil {
		next := *event.NextAttemptAt
		event.NextAttemptAt = &next
	}
	return event
}
