package audit

import (
	"context"
	"errors"
	"testing"

	"github.com/go-logr/logr"
)

// recordingSink 记录写入的事件。
type recordingSink struct {
	events []Event
	keys   []string
	err    error
}

func (s *recordingSink) Append(_ context.Context, key string, e Event) error {
	if s.err != nil {
		return s.err
	}
	s.keys = append(s.keys, key)
	s.events = append(s.events, e)
	return nil
}

func TestWriter_CriticalSuccess(t *testing.T) {
	sink := &recordingSink{}
	w := NewWriter(sink, logr.Discard())
	err := w.Critical(context.Background(), "key-1", "uid-1", "ExecutionStarted", "operator", map[string]any{"action": "ScaleDeployment"})
	if err != nil {
		t.Fatalf("Critical 写入失败: %v", err)
	}
	if len(sink.events) != 1 || sink.events[0].Severity != Critical {
		t.Errorf("事件未写入或分级错误: %+v", sink.events)
	}
	if sink.events[0].Component != "controller" {
		t.Errorf("组件字段错误: %s", sink.events[0].Component)
	}
}

func TestWriter_CriticalFailClosed(t *testing.T) {
	sink := &recordingSink{err: errors.New("sink down")}
	w := NewWriter(sink, logr.Discard())
	err := w.Critical(context.Background(), "key-1", "uid-1", "ExecutionStarted", "operator", nil)
	if err == nil {
		t.Fatal("Critical 失败必须返回错误(fail-closed)")
	}
}

func TestWriter_BestEffortSwallowsError(_ *testing.T) {
	sink := &recordingSink{err: errors.New("sink down")}
	w := NewWriter(sink, logr.Discard())
	// 不 panic、不返回错误。
	w.BestEffort(context.Background(), "key-1", "uid-1", "PhaseChanged", "operator", nil)
}

func TestWriter_BestEffortRecords(t *testing.T) {
	sink := &recordingSink{}
	w := NewWriter(sink, logr.Discard())
	w.BestEffort(context.Background(), "key-1", "uid-1", "PhaseChanged", "operator", map[string]any{"phase": "Resolved"})
	if len(sink.events) != 1 || sink.events[0].Severity != BestEffort {
		t.Errorf("事件未写入: %+v", sink.events)
	}
}

func TestWriter_NilSink(t *testing.T) {
	w := NewWriter(nil, logr.Discard())
	if err := w.Critical(context.Background(), "k", "u", "T", "", nil); err == nil {
		t.Error("nil sink 的 Critical 应报错")
	}
	// nil sink 的 BestEffort 不 panic。
	w.BestEffort(context.Background(), "k", "u", "T", "", nil)
}

func TestSinkFuncAdapter(t *testing.T) {
	called := false
	sink := SinkFunc(func(_ context.Context, _ string, _ Event) error {
		called = true
		return nil
	})
	w := NewWriter(sink, logr.Discard())
	if err := w.Critical(context.Background(), "k", "u", "T", "", nil); err != nil {
		t.Fatalf("SinkFunc 适配失败: %v", err)
	}
	if !called {
		t.Error("SinkFunc 未被调用")
	}
}
