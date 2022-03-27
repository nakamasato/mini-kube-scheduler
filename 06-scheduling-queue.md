## [6. Scheduling Queue](https://github.com/nakamasato/mini-kube-scheduler/tree/06-scheduling-queue)

In this example, we'll enable our scheduling queue to handle failure of scheduling Pods.

A Pod will remain unscheduled once it's failed to schedule. Ideally we should try to reschedule the failed Pod some time later.

Current queue:

```go
type SchedulingQueue struct {
	activeQ []*v1.Pod
	lock    *sync.Cond
}
```

[The queues in kube-scheduler](https://github.com/kubernetes/kubernetes/blob/da7f184344d841807c2da88a92ee96e1de32d97b/pkg/scheduler/internal/queue/scheduling_queue.go#L125-L175):

- `activeQ`: a queue for Pods that are waiting for scheduling.
- `unschedulableQ`: a queue for Pods that are failed to schedule. Also storing the plugins that failed during scheduling.
- `podBackoffQ`: a queue for Pods in backoff.

[QueuedPodInfo](https://pkg.go.dev/k8s.io/kubernetes/pkg/scheduler/framework#QueuedPodInfo) is a type for those queues.

### 6.1 Add `unschedulableQ` and `podBackoffQ`

```diff
diff --git a/minisched/queue/queue.go b/minisched/queue/queue.go
index d5ddd33..a93bb94 100644
--- a/minisched/queue/queue.go
+++ b/minisched/queue/queue.go
@@ -4,11 +4,15 @@ import (
        "sync"

        v1 "k8s.io/api/core/v1"
+       "k8s.io/kubernetes/pkg/scheduler/framework"
 )

 type SchedulingQueue struct {
-       activeQ []*v1.Pod
-       lock    *sync.Cond
+       activeQ        []*framework.QueuedPodInfo
+       podBackoffQ    []*framework.QueuedPodInfo
+       unschedulableQ map[string]*framework.QueuedPodInfo
+
+       lock *sync.Cond
 }
```

Also need to modify NextPod and Add functions to convert Pod into QueuedPodInfo

```diff
 func New() *SchedulingQueue {
        return &SchedulingQueue{
-               activeQ: []*v1.Pod{},
+               activeQ: []*framework.QueuedPodInfo{},
                lock:    sync.NewCond(&sync.Mutex{}),
        }
 }
@@ -22,7 +28,9 @@ func (s *SchedulingQueue) Add(pod *v1.Pod) {
        s.lock.L.Lock()
        defer s.lock.L.Unlock()

-       s.activeQ = append(s.activeQ, pod)
+       podInfo := s.newQueuedPodInfo(pod)
+
+       s.activeQ = append(s.activeQ, podInfo)
        s.lock.Signal()
        return
 }
@@ -37,5 +45,15 @@ func (s *SchedulingQueue) NextPod() *v1.Pod {
        p := s.activeQ[0]
        s.activeQ = s.activeQ[1:]
        s.lock.L.Unlock()
-       return p
+       return p.Pod
+}
+
+func (s *SchedulingQueue) newQueuedPodInfo(pod *v1.Pod, unschedulableplugins ...string) *framework.QueuedPodInfo {
+       now := time.Now()
+       return &framework.QueuedPodInfo{
+               PodInfo:                 framework.NewPodInfo(pod),
+               Timestamp:               now,
+               InitialAttemptTimestamp: now,
+               UnschedulablePlugins:    sets.NewString(unschedulableplugins...),
+       }
 }
```

### 6.2. Add `ErrorFunc` to all failed case.

Add `sched.ErrorFunc(pod, err)` when failing to schedule a pod in [scheduler.go](minisched/scheduler.go).

```diff
        klog.Info("minischeduler: ran pre score plugins successfully")
@@ -63,6 +64,7 @@ func (sched *Scheduler) scheduleOne(ctx context.Context) {
        score, status := sched.RunScorePlugins(ctx, state, pod, feasibleNodes)
        if !status.IsSuccess() {
                klog.Error(status.AsError())
+               sched.ErrorFunc(pod, err)
                return
        }
```

Define `ErrorFunc`:

```go
func (sched *Scheduler) ErrorFunc(pod *v1.Pod, err error) {
	podInfo := &framework.QueuedPodInfo{
		PodInfo: framework.NewPodInfo(pod),
	}
	if fitError, ok := err.(*framework.FitError); ok {
		// Inject UnschedulablePlugins to PodInfo, which will be used later for moving Pods between queues efficiently.
		podInfo.UnschedulablePlugins = fitError.Diagnosis.UnschedulablePlugins
		klog.V(2).InfoS("Unable to schedule pod; no fit; waiting", "pod", klog.KObj(pod), "err", err)
	} else {
		klog.ErrorS(err, "Error scheduling pod; retrying", "pod", klog.KObj(pod))
	}

	if err := sched.SchedulingQueue.AddUnschedulable(podInfo); err != nil {
		klog.ErrorS(err, "Error occurred")
	}
}
```

### 6.3 Add `AddUnschedulable` to [minisched/queue/queue.go](queue.go)

```go
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
```
At this point, failed Pods are put back to `unschedulableQ`. In the next section, we'll implement move the items in `unschedulableQ` to `activeQ` or `podBackoffQ` to try scheduling again.
