package ops

import (
	"sync"
	"testing"
	"time"
)

// TestTaskManagerRunsAndAggregates 任务应异步执行并正确聚合成功/失败计数。
func TestTaskManagerRunsAndAggregates(t *testing.T) {
	m := NewTaskManager()
	done := make(chan struct{})

	view := m.New("test", "测试任务", func(task *Task) {
		defer close(done)
		task.addItem(TaskItem{UID: "u1", OK: true, Message: "ok"})
		task.addItem(TaskItem{UID: "u2", OK: false, Message: "fail"})
		task.addItem(TaskItem{UID: "u3", OK: true, Message: "ok"})
	})

	if view.ID == "" {
		t.Fatal("任务应返回 id")
	}
	if !view.Running {
		t.Error("刚创建的任务应为 running")
	}

	<-done
	// 等待 finish() 落地（runner 返回后才标记完成）。
	deadline := time.Now().Add(2 * time.Second)
	var got TaskView
	for time.Now().Before(deadline) {
		v, ok := m.Get(view.ID)
		if ok && !v.Running {
			got = v
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if got.Running {
		t.Fatal("任务应已完成")
	}
	if got.Total != 3 || got.OK != 2 || got.Failed != 1 || got.Done != 3 {
		t.Errorf("计数错误: total=%d ok=%d failed=%d done=%d", got.Total, got.OK, got.Failed, got.Done)
	}
	if len(got.Items) != 3 {
		t.Errorf("明细条数 = %d, want 3", len(got.Items))
	}
}

// TestTaskManagerPanicRecovered 任务内 panic 不应拖垮进程，且任务需被标记结束。
func TestTaskManagerPanicRecovered(t *testing.T) {
	m := NewTaskManager()
	view := m.New("panic", "会 panic 的任务", func(task *Task) {
		task.addItem(TaskItem{UID: "u1", OK: true})
		panic("boom")
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		v, ok := m.Get(view.ID)
		if ok && !v.Running {
			if v.Error == "" {
				t.Error("panic 后应记录错误信息")
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("panic 后任务未结束（recover 失效）")
}

// TestTaskManagerUnknownID 未知任务 id 应返回 false 而不是空结构。
func TestTaskManagerUnknownID(t *testing.T) {
	m := NewTaskManager()
	if _, ok := m.Get("nope"); ok {
		t.Error("未知 id 应返回 false")
	}
}

// TestTaskManagerRecentOrder 最近任务应最新在前，且列表不带明细。
func TestTaskManagerRecentOrder(t *testing.T) {
	m := NewTaskManager()
	for i := 0; i < 3; i++ {
		id := i
		v := m.New("t", "任务", func(task *Task) {
			task.addItem(TaskItem{UID: "u", OK: true})
		})
		_ = id
		_ = v
	}
	time.Sleep(200 * time.Millisecond)

	recent := m.Recent(10)
	if len(recent) != 3 {
		t.Fatalf("应为 3 条, 得到 %d", len(recent))
	}
	for _, r := range recent {
		if r.Items != nil {
			t.Error("列表视图不应带 items 明细")
		}
	}
	// 最新创建的 seq 最大，应排在最前。
	if recent[0].ID <= recent[len(recent)-1].ID {
		t.Errorf("排序错误: first=%s last=%s", recent[0].ID, recent[len(recent)-1].ID)
	}
}

// TestTaskManagerEviction 超过保留上限时淘汰最老任务，防止内存无界增长。
func TestTaskManagerEviction(t *testing.T) {
	m := NewTaskManager()
	m.maxKeep = 3
	var ids []string
	for i := 0; i < 6; i++ {
		v := m.New("t", "任务", func(task *Task) {})
		ids = append(ids, v.ID)
	}
	time.Sleep(300 * time.Millisecond)

	if _, ok := m.Get(ids[0]); ok {
		t.Error("最老的任务应已被淘汰")
	}
	if _, ok := m.Get(ids[5]); !ok {
		t.Error("最新的任务应被保留")
	}
	if got := len(m.Recent(100)); got > 3 {
		t.Errorf("保留条数 = %d, 应 <= 3", got)
	}
}

// TestTaskManagerConcurrentAdd 并发追加结果不应丢数据或触发竞态
// （用 -race 运行时才有完整意义）。
func TestTaskManagerConcurrentAdd(t *testing.T) {
	m := NewTaskManager()
	const n = 50
	view := m.New("concurrent", "并发任务", func(task *Task) {
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				task.addItem(TaskItem{UID: "u", OK: i%2 == 0})
			}(i)
		}
		wg.Wait()
	})

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		v, ok := m.Get(view.ID)
		if ok && !v.Running {
			if v.Done != n {
				t.Errorf("并发追加丢数据: done=%d, want %d", v.Done, n)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("并发任务未结束")
}

// TestTaskManagerRunning 进行中任务查询。
func TestTaskManagerRunning(t *testing.T) {
	m := NewTaskManager()
	release := make(chan struct{})
	m.New("slow", "慢任务", func(task *Task) {
		<-release
	})

	// 等待任务真正开跑。
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && len(m.Running()) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if got := m.Running(); len(got) != 1 {
		t.Errorf("应有 1 个进行中任务, 得到 %d", len(got))
	}
	close(release)
}
