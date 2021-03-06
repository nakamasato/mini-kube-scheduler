# Mini Kube Scheduler

What this repo does are:
- Study kubernetes scheduler following [自作して学ぶKubernetes Scheduler](https://engineering.mercari.com/blog/entry/20211220-create-your-kube-scheduler/) with [sanposhiho/mini-kube-scheduler](https://github.com/sanposhiho/mini-kube-scheduler).
    - Simplify the original repo https://github.com/sanposhiho/mini-kube-scheduler
    - Implement [ToDos in sanposhiho/mini-kube-scheduler](https://github.com/sanposhiho/mini-kube-scheduler/blob/b5ca1625d39d4c98e67fab3b052366ce01047234/minisched/queue/queue.go#L135-L146) .
- Gain basic understanding of [Scheduling Framework](https://kubernetes.io/docs/concepts/scheduling-eviction/scheduling-framework/) by hands-on.
    ![](https://raw.githubusercontent.com/kubernetes/website/main/static/images/docs/scheduling-framework-extensions.png)
## Versions

- Go: 1.17
- Kubernetes: [1.23.4](https://github.com/kubernetes/kubernetes/releases/tag/v1.23.4)
- etcd: [3.5.2](https://github.com/etcd-io/etcd/releases/tag/v3.5.2)

## Components

- `Scheduler`: Custom Scheduler (minisched)
    - `SchedulingQueue`: Store Pods to schedule to a node.
        - `activeQ`: A queue to store Pods to start scheduling.
        - `unschedulableQ`: A queue to store Pods that are failed to schedule (with the plugins that made it unschedulable.)
        - `podBackoffQ`: A queue to store Pods that are in back-off state.
    - `client`: Used to update Pod to bind it to a node.
- `Informer` with `Handler` (`Scheduler.addPodToSchedulingQueue()`) + `FilterFunc`
- `Service`:
    - Initialize Scheduler and set the event handler for informer with `New()`
    - Call `Scheduler.Run()` to start `Scheduler`.
- `Plugin`: Implement logic for extension points in each scheduling context and called in the scheduler.

Diagram:

![](diagram.drawio.svg)

Files:
1. `hack`: Shell scripts
    1. `etcd.sh`: Functions to start/stop/cleanup etcd.
    1. `openapi.sh`: Generate `zz_generated.openapi.go` for `kube-apiserver`.
    1. `run.sh`: Start etcd and run the scheduler.
1. `k8sapiserver`: Dependency to run a scheduler.
1. `minisched`: Implementation of mini-kube-scheduler.
1. `sched.go`: Run dependency (`apiserver`) and the scheduler within a scenario (create nodes and a pod).
1. `scheduler`: Scheduler service to manage `minisched`.

## Steps
1. [Initial Random Scheduler](https://github.com/nakamasato/mini-kube-scheduler/tree/01-initial-random-scheduler/01-initial-random-scheduler.md): Randomly schedule a Pod to available nodes.
1. [Filter Plugins](https://github.com/nakamasato/mini-kube-scheduler/tree/02-filter-plugins/02-filter-plugins.md): Use `NodeUnschedulable` plugin.
1. [Score Plugins](https://github.com/nakamasato/mini-kube-scheduler/tree/03-score-plugins/03-score-plugins.md): Schedule based on the score of each node. (`NodeName`)
1. [PreScore Plugins](https://github.com/nakamasato/mini-kube-scheduler/tree/04-prescore-plugins/04-prescore-plugins.md): Move common logic in the ScorePlugin to PreScorePlugin to reuse it.
1. [Permit Plugins](https://github.com/nakamasato/mini-kube-scheduler/tree/05-permit-plugins/05-permit-plugins.md): Delay n seconds based on the name of the node.
1. [Scheduling Queue](https://github.com/nakamasato/mini-kube-scheduler/tree/06-scheduling-queue/06-scheduling-queue.md): Make the simple queue closer to the one used in `kube-scheduler`. (`podBackoffQ` and `UnschedulableQ` + Logic to put back a queue)
1. [Event Handler](https://github.com/nakamasato/mini-kube-scheduler/tree/07-event-handler/07-event-handler.md): Trigger `MoveAllToActiveOrBackoffQueue` when a new node is added.
1. [Flush Queue](https://github.com/nakamasato/mini-kube-scheduler/tree/08-flush-queue/08-flush-queue.md): Flush `podBackoffQ` and `UnschedulableQ` periodically.

Tips: [Development Tips](DEVELOPMENT.md)

## References
1. kubernetes.io/docs
    1. https://kubernetes.io/docs/concepts/scheduling-eviction/scheduling-framework
    1. https://kubernetes.io/docs/reference/scheduling/config
1. github.com
    1. https://github.com/sanposhiho/mini-kube-scheduler
    1. https://github.com/draios/kubernetes-scheduler
    1. https://github.com/kubernetes/kubernetes/blob/v1.23.4/pkg/scheduler/internal/queue/scheduling_queue.go
    1. https://github.com/kubernetes/enhancements/tree/master/keps/sig-scheduling/624-scheduling-framework
    1. https://github.com/kubernetes-sigs/kube-scheduler-simulator
    1. https://github.com/kubernetes-sigs/scheduler-plugins
1. Others
    1. [自作して学ぶKubernetes Scheduler (mercari engineering)](https://engineering.mercari.com/blog/entry/20211220-create-your-kube-scheduler/)
    1. [自作して学ぶKubernetes scheduler入門 (CloudNative Days 2021)](https://event.cloudnativedays.jp/cndt2021/talks/1184)
    1. [Writing custom Kubernetes schedulers](https://banzaicloud.com/blog/k8s-custom-scheduler/)
    1. [Kubernetes Scheduler 自作入門](https://qiita.com/ozota/items/28f6686029865e8df4fe)
    1. [Building a Kubernetes Scheduler using Custom Metrics - Mateo Burillo, Sysdig (YouTube)](https://www.youtube.com/watch?v=4TaHQgG9wEg)
    1. ['unknown revision v0.0.0' errors, seemingly due to 'require k8s.io/foo v0.0.0' #79384](https://github.com/kubernetes/kubernetes/issues/79384#issuecomment-521493597)
