# Development Tips

## 1. kube-apiserver

Use https://github.com/kubernetes/kubernetes to generate [k8sapiserver/openapizz_generated.openapi.go](k8sapiserver/openapizz_generated.openapi.go).

## 2. Scheduler Plugins

You can use [default plugins](https://kubernetes.io/docs/reference/scheduling/config/#scheduling-plugins) or implement your own plugins

## 3. Go: module error

If you get [unknown revision v0.0.0' errors, seemingly due to 'require k8s.io/foo v0.0.0' #79384](https://github.com/kubernetes/kubernetes/issues/79384#issuecomment-521493597), run the command:

```
./update_go_mod.sh <k8s_version> # e.g. 1.23.4
```

## 4. Go: Use pointer of for range loop variable

About the redeclaration of a variable in a for loop:

```go
n := n
```

[Use Pointer of for Range Loop Variable in Go](https://medium.com/swlh/use-pointer-of-for-range-loop-variable-in-go-3d3481f7ffc9)

## 5. Go: [Cond.Wait](https://pkg.go.dev/sync#Cond.Wait) and [Cond.Signal](https://pkg.go.dev/sync#Cond.Signal)

`s.lock.Wait()`: Wait atomically unlocks c.L and suspends execution of the calling goroutine. After later resuming execution, Wait locks c.L before returning. Unlike in other systems, Wait cannot return unless awoken by Broadcast or Signal.

When getting `NextPod()`, it's waiting until `s.activeQ` has at least one Pod:

```go
func (s *SchedulingQueue) NextPod() *v1.Pod {
	// wait
	s.lock.L.Lock()
	for len(s.activeQ) == 0 {
		klog.Info("NextPod: waiting")
		s.lock.Wait()
		klog.Info("NextPod: awoken")
	}
    ...
}
```

When adding a Pod to `activeQ`, we call `Signal` to awaken `Wait`:

```go
func (s *SchedulingQueue) Add(pod *v1.Pod) {
	s.lock.L.Lock()
	defer s.lock.L.Unlock()

	podInfo := s.newQueuedPodInfo(pod)

	s.activeQ = append(s.activeQ, podInfo)
	s.lock.Signal() // Awaken wait
}
```

```go
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
```

```go
func (s *SchedulingQueue) flushBackoffQCompleted() {
	s.lock.L.Lock()
	defer s.lock.L.Unlock()
	for {
        ...
		} else {
			s.activeQ = append(s.activeQ, queuedPodInfo)
			s.lock.Signal() // awaken Wait() in NextPod()
			klog.Infof("flushBackoffQCompleted: added pod(%s) to activeQ", queuedPodInfo.Pod.Name)
		}
	}
}
```

For more details: https://pkg.go.dev/sync
