// tasks.go 异步批量任务管理：批量签到/刷新/旅行这类对多账号串行执行的操作
// 耗时可达数十秒，同步 HTTP 请求会超时。改为「提交任务 → 轮询进度」模式。
package ops

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// TaskItem 单个账号在批量任务中的执行结果。
type TaskItem struct {
	UID       string    `json:"uid"`
	Nickname  string    `json:"nickname"`
	Action    string    `json:"action"`
	OK        bool      `json:"ok"`
	Message   string    `json:"message"`
	Reward    int64     `json:"reward,omitempty"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
}

// Task 一次批量任务。
type Task struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`  // checkin / refresh / travel
	Title string `json:"title"` // 人类可读标题

	mu         sync.Mutex
	items      []TaskItem
	running    bool
	errText    string
	startedAt  time.Time
	finishedAt time.Time
}

// TaskView 任务的只读快照（JSON 输出用）。
type TaskView struct {
	ID         string     `json:"id"`
	Kind       string     `json:"kind"`
	Title      string     `json:"title"`
	Running    bool       `json:"running"`
	Error      string     `json:"error,omitempty"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt time.Time  `json:"finished_at,omitempty"`
	Total      int        `json:"total"`
	Done       int        `json:"done"`
	OK         int        `json:"ok"`
	Failed     int        `json:"failed"`
	Items      []TaskItem `json:"items"`
}

// view 生成快照（持锁拷贝）。
func (t *Task) view() TaskView {
	t.mu.Lock()
	defer t.mu.Unlock()
	v := TaskView{
		ID:        t.ID,
		Kind:      t.Kind,
		Title:     t.Title,
		Running:   t.running,
		Error:     t.errText,
		StartedAt: t.startedAt,
		Total:     len(t.items),
		Items:     append([]TaskItem(nil), t.items...),
	}
	if !t.finishedAt.IsZero() {
		v.FinishedAt = t.finishedAt
	}
	for _, it := range t.items {
		v.Done++
		if it.OK {
			v.OK++
		} else {
			v.Failed++
		}
	}
	return v
}

// addItem 追加一条账号结果。
func (t *Task) addItem(it TaskItem) {
	t.mu.Lock()
	t.items = append(t.items, it)
	t.mu.Unlock()
}

// finish 标记任务结束。
func (t *Task) finish(err error) {
	t.mu.Lock()
	t.running = false
	t.finishedAt = time.Now()
	if err != nil {
		t.errText = err.Error()
	}
	t.mu.Unlock()
}

// TaskManager 进程内任务表（保留最近若干条，供前端展示历史）。
type TaskManager struct {
	mu    sync.Mutex
	tasks map[string]*Task
	order []string
	seq   atomic.Int64
	// maxKeep 保留的任务条数上限。
	maxKeep int
}

// NewTaskManager 构建任务表。
func NewTaskManager() *TaskManager {
	return &TaskManager{tasks: map[string]*Task{}, maxKeep: 50}
}

// New 创建一个任务并立即启动 runner（在独立 goroutine 中执行）。
// runner 通过 addItem 汇报每个账号的结果。
func (m *TaskManager) New(kind, title string, runner func(t *Task)) *TaskView {
	id := fmt.Sprintf("%s-%d", kind, m.seq.Add(1))
	t := &Task{
		ID:        id,
		Kind:      kind,
		Title:     title,
		running:   true,
		startedAt: time.Now(),
	}
	m.mu.Lock()
	m.tasks[id] = t
	m.order = append(m.order, id)
	// 淘汰最老的已完成任务，避免长期运行后内存无界增长。
	for len(m.order) > m.maxKeep {
		old := m.order[0]
		m.order = m.order[1:]
		delete(m.tasks, old)
	}
	m.mu.Unlock()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				t.finish(fmt.Errorf("任务内部错误: %v", r))
			}
		}()
		runner(t)
		t.finish(nil)
	}()

	v := t.view()
	return &v
}

// Get 按 id 取任务快照。
func (m *TaskManager) Get(id string) (TaskView, bool) {
	m.mu.Lock()
	t, ok := m.tasks[id]
	m.mu.Unlock()
	if !ok {
		return TaskView{}, false
	}
	return t.view(), true
}

// Recent 返回最近的若干条任务（最新在前，不含明细）。
func (m *TaskManager) Recent(limit int) []TaskView {
	m.mu.Lock()
	ids := append([]string(nil), m.order...)
	m.mu.Unlock()
	if limit <= 0 || limit > len(ids) {
		limit = len(ids)
	}
	// 倒序取最新。
	out := make([]TaskView, 0, limit)
	for i := len(ids) - 1; i >= 0 && len(out) < limit; i-- {
		if v, ok := m.Get(ids[i]); ok {
			v.Items = nil // 列表不带明细
			out = append(out, v)
		}
	}
	return out
}

// Running 报告当前是否有任务在执行（用于 UI 防重入提示）。
func (m *TaskManager) Running() []TaskView {
	m.mu.Lock()
	ids := append([]string(nil), m.order...)
	m.mu.Unlock()
	var out []TaskView
	for _, id := range ids {
		if v, ok := m.Get(id); ok && v.Running {
			v.Items = nil
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out
}
