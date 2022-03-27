package queue

import (
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

type SchedulingQueue struct {
	activeQ        []*framework.QueuedPodInfo
	podBackoffQ    []*framework.QueuedPodInfo
	unschedulableQ map[string]*framework.QueuedPodInfo

	lock *sync.Cond
}

func New() *SchedulingQueue {
	return &SchedulingQueue{
		activeQ: []*framework.QueuedPodInfo{},
		lock:    sync.NewCond(&sync.Mutex{}),
	}
}

func (s *SchedulingQueue) Add(pod *v1.Pod) {
	s.lock.L.Lock()
	defer s.lock.L.Unlock()

	podInfo := s.newQueuedPodInfo(pod)

	s.activeQ = append(s.activeQ, podInfo)
	s.lock.Signal()
	return
}

func (s *SchedulingQueue) NextPod() *v1.Pod {
	// wait
	s.lock.L.Lock()
	for len(s.activeQ) == 0 {
		s.lock.Wait()
	}

	p := s.activeQ[0]
	s.activeQ = s.activeQ[1:]
	s.lock.L.Unlock()
	return p.Pod
}

func (s *SchedulingQueue) newQueuedPodInfo(pod *v1.Pod, unschedulableplugins ...string) *framework.QueuedPodInfo {
	now := time.Now()
	return &framework.QueuedPodInfo{
		PodInfo:                 framework.NewPodInfo(pod),
		Timestamp:               now,
		InitialAttemptTimestamp: now,
		UnschedulablePlugins:    sets.NewString(unschedulableplugins...),
	}
}

func (s *SchedulingQueue) AddUnschedulable(pInfo *framework.QueuedPodInfo) error {
	s.lock.L.Lock()
	defer s.lock.L.Unlock()

	// Refresh the timestamp since the pod is re-added.
	pInfo.Timestamp = time.Now()

	// add or update
	s.unschedulableQ[keyFunc(pInfo)] = pInfo

	klog.Info("queue: pod added to unschedulableQ: "+pInfo.Pod.Name+". This pod is unscheduled by ", pInfo.UnschedulablePlugins)
	return nil
}

func keyFunc(pInfo *framework.QueuedPodInfo) string {
	return pInfo.Pod.Name + "_" + pInfo.Pod.Namespace
}
