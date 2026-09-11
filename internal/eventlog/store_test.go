package eventlog

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestStoreKeepsBoundedRecentEvents(t *testing.T) {
	store, err := New(2)
	if err != nil {
		t.Fatalf("New() 失败: %v", err)
	}
	first := store.Append(Event{AccountID: "one", Message: "first"})
	second := store.Append(Event{AccountID: "two", Message: "second"})
	third := store.Append(Event{AccountID: "three", Message: "third"})

	if first.Sequence != 1 || second.Sequence != 2 || third.Sequence != 3 {
		t.Fatalf("事件序号异常: %d %d %d", first.Sequence, second.Sequence, third.Sequence)
	}
	recent := store.Recent(0)
	if len(recent) != 2 || recent[0].Message != "second" || recent[1].Message != "third" {
		t.Fatalf("近期事件 = %+v", recent)
	}
	limited := store.Recent(1)
	if len(limited) != 1 || limited[0].Message != "third" {
		t.Fatalf("限制结果 = %+v", limited)
	}
}

func TestStoreResizeKeepsNewestEvents(t *testing.T) {
	store, err := New(3)
	if err != nil {
		t.Fatalf("New() 失败: %v", err)
	}
	store.Append(Event{Message: "first"})
	store.Append(Event{Message: "second"})
	store.Append(Event{Message: "third"})
	if err := store.Resize(2); err != nil {
		t.Fatalf("Resize() 失败: %v", err)
	}
	recent := store.Recent(0)
	if len(recent) != 2 || recent[0].Message != "second" || recent[1].Message != "third" {
		t.Fatalf("缩容后的事件 = %+v", recent)
	}
	if err := store.Resize(4); err != nil {
		t.Fatalf("扩容失败: %v", err)
	}
	store.Append(Event{Message: "fourth"})
	if len(store.Recent(0)) != 3 {
		t.Fatalf("扩容后事件数量 = %d", len(store.Recent(0)))
	}
}

func TestStoreWaitReturnsExistingEvents(t *testing.T) {
	store, err := New(4)
	if err != nil {
		t.Fatalf("New() 失败: %v", err)
	}
	store.Append(Event{Message: "first"})
	store.Append(Event{Message: "second"})
	events, err := store.Wait(context.Background(), 1, 0)
	if err != nil {
		t.Fatalf("Wait() 失败: %v", err)
	}
	if len(events) != 1 || events[0].Message != "second" {
		t.Fatalf("等待结果 = %+v", events)
	}
}

func TestStoreWaitReceivesNewEventAndCanCancel(t *testing.T) {
	store, err := New(4)
	if err != nil {
		t.Fatalf("New() 失败: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := make(chan []Event, 1)
	failure := make(chan error, 1)
	go func() {
		events, err := store.Wait(ctx, 0, 1)
		if err != nil {
			failure <- err
			return
		}
		result <- events
	}()
	time.Sleep(10 * time.Millisecond)
	store.Append(Event{Message: "new"})
	select {
	case events := <-result:
		if len(events) != 1 || events[0].Message != "new" {
			t.Fatalf("新事件 = %+v", events)
		}
	case err := <-failure:
		t.Fatalf("Wait() 返回错误: %v", err)
	case <-time.After(time.Second):
		t.Fatal("Wait() 未收到新事件")
	}

	cancelled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := store.Wait(cancelled, 99, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("已取消 Wait() 错误 = %v", err)
	}
}

func TestStoreDropsOldEventsForSlowSubscriber(t *testing.T) {
	store, err := New(4)
	if err != nil {
		t.Fatalf("New() 失败: %v", err)
	}
	subscription, err := store.Subscribe(1)
	if err != nil {
		t.Fatalf("Subscribe() 失败: %v", err)
	}
	defer subscription.Cancel()

	store.Append(Event{Message: "first", At: time.Unix(1, 0)})
	store.Append(Event{Message: "second", At: time.Unix(2, 0)})

	select {
	case event := <-subscription.Events:
		if event.Message != "second" || event.Dropped != 1 {
			t.Fatalf("订阅事件 = %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("未收到订阅事件")
	}
}

func TestSubscriptionCancelClosesChannel(t *testing.T) {
	store, err := New(1)
	if err != nil {
		t.Fatalf("New() 失败: %v", err)
	}
	subscription, err := store.Subscribe(1)
	if err != nil {
		t.Fatalf("Subscribe() 失败: %v", err)
	}
	subscription.Cancel()
	subscription.Cancel()

	select {
	case _, open := <-subscription.Events:
		if open {
			t.Fatal("取消后事件通道仍打开")
		}
	case <-time.After(time.Second):
		t.Fatal("取消后事件通道未关闭")
	}
}

func TestStoreRejectsInvalidCapacity(t *testing.T) {
	if _, err := New(0); err == nil {
		t.Fatal("零容量事件存储未被拒绝")
	}
	store, _ := New(1)
	if _, err := store.Subscribe(0); err == nil {
		t.Fatal("零容量订阅未被拒绝")
	}
}
