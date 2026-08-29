package queue

import (
	"container/list"
	"context"
	"sync"
)

// Job 翻译任务
type Job struct {
	ID   string
	Type string   // "translate" | "batch"
	Args any      // *api.TranslateRequest 或 *api.BatchRequest
	Done chan any // 结果通道（返回值或 error）
}

// Queue 串行翻译队列：单 worker 保证推理串行
type Queue struct {
	mu      sync.Mutex
	jobs    *list.List
	notify  chan struct{}
	worker  func(context.Context, *Job)
	stop    chan struct{}
	stopped bool
}

// New 创建队列并启动 worker
func New(worker func(context.Context, *Job)) *Queue {
	q := &Queue{
		jobs:   list.New(),
		notify: make(chan struct{}, 1),
		worker: worker,
		stop:   make(chan struct{}),
	}
	go q.loop()
	return q
}

func (q *Queue) loop() {
	for {
		q.mu.Lock()
		if q.jobs.Len() == 0 {
			q.mu.Unlock()
			select {
			case <-q.notify:
				continue
			case <-q.stop:
				return
			}
		}
		el := q.jobs.Front()
		q.jobs.Remove(el)
		q.mu.Unlock()

		job := el.Value.(*Job)
		q.worker(context.Background(), job)
		close(job.Done)
	}
}

// Submit 提交任务，返回结果通道
func (q *Queue) Submit(job *Job) <-chan any {
	job.Done = make(chan any, 1)
	q.mu.Lock()
	q.jobs.PushBack(job)
	q.mu.Unlock()
	select {
	case q.notify <- struct{}{}:
	default:
	}
	return job.Done
}

// Pending 当前排队任务数
func (q *Queue) Pending() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.jobs.Len()
}

// Close 停止队列
func (q *Queue) Close() {
	q.mu.Lock()
	if q.stopped {
		q.mu.Unlock()
		return
	}
	q.stopped = true
	close(q.stop)
	q.mu.Unlock()
}