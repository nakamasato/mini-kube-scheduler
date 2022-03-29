package queue

import (
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

type SchedulingQueue struct {
	activeQ        []*framework.QueuedPodInfo
	podBackoffQ    []*framework.QueuedPodInfo
	unschedulableQ map[string]*framework.QueuedPodInfo

	lock *sync.Cond

	clusterEventMap map[framework.ClusterEvent]sets.String
	stop            chan struct{}
}

func New(clusterEventMap map[framework.ClusterEvent]sets.String) *SchedulingQueue {
	return &SchedulingQueue{
		activeQ:         []*framework.QueuedPodInfo{},
		podBackoffQ:     []*framework.QueuedPodInfo{},
		unschedulableQ:  map[string]*framework.QueuedPodInfo{},
		clusterEventMap: clusterEventMap,
		lock:            sync.NewCond(&sync.Mutex{}),
		stop:            make(chan struct{}),
	}
}

// Run starts the goroutine to pump from podBackoffQ to activeQ
func (s *SchedulingQueue) Run() {
	go wait.Until(s.flushBackoffQCompleted, 1*time.Second, s.stop)
	go wait.Until(s.flushUnschedulablePodsLeftover, 1*time.Second, s.stop) // originally 30 sec
}

func (s *SchedulingQueue) Close() {
	s.lock.L.Lock()
	defer s.lock.L.Unlock()
	close(s.stop)
}

func (s *SchedulingQueue) Add(pod *v1.Pod) {
	s.lock.L.Lock()
	defer s.lock.L.Unlock()

	podInfo := s.newQueuedPodInfo(pod)

	s.activeQ = append(s.activeQ, podInfo)
	s.lock.Signal() // Awaken wait
}

func (s *SchedulingQueue) NextPod() *v1.Pod {
	// wait
	s.lock.L.Lock()
	for len(s.activeQ) == 0 {
		klog.Info("NextPod: waiting")
		s.lock.Wait()
		klog.Info("NextPod: awoken")
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

// This is achieved by looking up the global clusterEventMap registry.
func (s *SchedulingQueue) podMatchesEvent(podInfo *framework.QueuedPodInfo, clusterEvent framework.ClusterEvent) bool {
	if clusterEvent.IsWildCard() {
		return true
	}

	for evt, nameSet := range s.clusterEventMap {
		// Firstly verify if the two ClusterEvents match:
		// - either the registered event from plugin side is a WildCardEvent,
		// - or the two events have identical Resource fields and *compatible* ActionType.
		//   Note the ActionTypes don't need to be *identical*. We check if the ANDed value
		//   is zero or not. In this way, it's easy to tell Update&Delete is not compatible,
		//   but Update&All is.
		evtMatch := evt.IsWildCard() ||
			(evt.Resource == clusterEvent.Resource && evt.ActionType&clusterEvent.ActionType != 0)

		// Secondly verify the plugin name matches.
		// Note that if it doesn't match, we shouldn't continue to search.
		if evtMatch && intersect(nameSet, podInfo.UnschedulablePlugins) {
			return true
		}
	}

	return false
}

func (s *SchedulingQueue) MoveAllToActiveOrBackoffQueue(event framework.ClusterEvent) {
	s.lock.L.Lock()
	defer s.lock.L.Unlock()
	unschedulablePods := make([]*framework.QueuedPodInfo, 0, len(s.unschedulableQ))
	for _, pInfo := range s.unschedulableQ {
		unschedulablePods = append(unschedulablePods, pInfo)
	}
	s.movePodsToActiveOrBackoffQueue(unschedulablePods, event)

	s.lock.Signal()
}

func (s *SchedulingQueue) movePodsToActiveOrBackoffQueue(podInfoList []*framework.QueuedPodInfo, event framework.ClusterEvent) {
	for _, pInfo := range podInfoList {
		// If the event doesn't help making the Pod schedulable, continue.
		// Note: we don't run the check if pInfo.UnschedulablePlugins is nil, which denotes
		// either there is some abnormal error, or scheduling the pod failed by plugins other than PreFilter, Filter and Permit.
		// In that case, it's desired to move it anyways.
		if len(pInfo.UnschedulablePlugins) != 0 && !s.podMatchesEvent(pInfo, event) {
			continue
		}

		if isPodBackingoff(pInfo) {
			klog.Infof("queue: add Pod(%s) to podBackoffQ", pInfo.Pod.Name)
			s.podBackoffQ = append(s.podBackoffQ, pInfo)
		} else {
			klog.Infof("queue: add Pod(%s) to activeQ", pInfo.Pod.Name)
			s.activeQ = append(s.activeQ, pInfo)
		}
		klog.Infof("queue: remove Pod(%s) from unschedulableQ", pInfo.Pod.Name)
		delete(s.unschedulableQ, keyFunc(pInfo))
	}
}

func intersect(x, y sets.String) bool {
	if len(x) > len(y) {
		x, y = y, x
	}
	for v := range x {
		if y.Has(v) {
			return true
		}
	}
	return false
}

// isPodBackingoff returns true if a pod is still waiting for its backoff timer.
// If this returns true, the pod should not be re-tried.
func isPodBackingoff(podInfo *framework.QueuedPodInfo) bool {
	boTime := getBackoffTime(podInfo)
	klog.Infof("queue: Pod: %s, backoff time: %s, now: %s", podInfo.Pod.Name, boTime, time.Now())
	return boTime.After(time.Now())
}

// getBackoffTime returns the time that podInfo completes backoff
func getBackoffTime(podInfo *framework.QueuedPodInfo) time.Time {
	duration := calculateBackoffDuration(podInfo)
	backoffTime := podInfo.Timestamp.Add(duration)
	return backoffTime
}

const (
	podInitialBackoffDuration         = 1 * time.Second
	podMaxBackoffDuration             = 10 * time.Second
	podMaxInUnschedulablePodsDuration = 5 * time.Minute
)

var UnschedulableTimeout = framework.ClusterEvent{Resource: framework.WildCard, ActionType: framework.All, Label: "UnschedulableTimeout"}

// calculateBackoffDuration is a helper function for calculating the backoffDuration
// based on the number of attempts the pod has made.
func calculateBackoffDuration(podInfo *framework.QueuedPodInfo) time.Duration {
	duration := podInitialBackoffDuration
	for i := 1; i < podInfo.Attempts; i++ {
		// Use subtraction instead of addition or multiplication to avoid overflow.
		if duration > podMaxBackoffDuration-duration {
			return podMaxBackoffDuration
		}
		duration += duration
	}
	return duration
}

// flushBackoffQCompleted Moves all pods from backoffQ which have completed backoff in to activeQ
func (s *SchedulingQueue) flushBackoffQCompleted() {
	s.lock.L.Lock()
	defer s.lock.L.Unlock()
	for {
		if len(s.podBackoffQ) == 0 {
			break
		}

		podBackoffQLen := len(s.podBackoffQ)
		queuedPodInfo := s.podBackoffQ[0] // Get item
		s.podBackoffQ = s.podBackoffQ[1:] // Remove the first item
		klog.Infof("flushBackoffQCompleted: podBackOffQ: %d -> %d", podBackoffQLen, len(s.podBackoffQ))
		if queuedPodInfo == nil {
			klog.Info("flushBackoffQCompleted: queuedPodInfo is nil")
			break
		}

		boTime := getBackoffTime(queuedPodInfo)
		if boTime.After(time.Now()) {
			s.podBackoffQ = append(s.podBackoffQ, queuedPodInfo) // Put to the last
			klog.Infof("flushBackoffQCompleted: put pod(%s) back to podBackoffQ (backoffTime: %s, now: %s)", queuedPodInfo.Pod.Name, boTime, time.Now())
			break
		} else {
			s.activeQ = append(s.activeQ, queuedPodInfo)
			s.lock.Signal() // awaken Wait() in NextPod()
			klog.Infof("flushBackoffQCompleted: added pod(%s) to activeQ", queuedPodInfo.Pod.Name)
		}
	}
}

// flushUnschedulablePodsLeftover moves pods which stay in unschedulableQ
// longer than podMaxInUnschedulablePodsDuration to backoffQ or activeQ.
func (s *SchedulingQueue) flushUnschedulablePodsLeftover() {
	s.lock.L.Lock()
	defer s.lock.L.Unlock()

	var podsToMove []*framework.QueuedPodInfo
	currentTime := time.Now()
	for _, pInfo := range s.unschedulableQ {
		lastScheduleTime := pInfo.Timestamp
		if currentTime.Sub(lastScheduleTime) > podMaxInUnschedulablePodsDuration {
			podsToMove = append(podsToMove, pInfo)
		}
	}

	if len(podsToMove) > 0 {
		s.movePodsToActiveOrBackoffQueue(podsToMove, UnschedulableTimeout)
		klog.Infof("flushUnschedulablePodsLeftover: movePodsToActiveOrBackoffQueue  podsToMove: %d", len(podsToMove))
	}
}
