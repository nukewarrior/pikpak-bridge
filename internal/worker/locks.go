package worker

import (
 "hash/fnv"
 "sync"
)

// TaskLocks serializes remote operations for one task across all workers.
type TaskLocks struct { slots [256]sync.Mutex }

func NewTaskLocks() *TaskLocks { return &TaskLocks{} }

func (l *TaskLocks) Lock(taskID string) func() {
 h := fnv.New32a()
 _, _ = h.Write([]byte(taskID))
 slot := &l.slots[h.Sum32()%uint32(len(l.slots))]
 slot.Lock()
 return slot.Unlock
}
