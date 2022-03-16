# Mini Kube Scheduler

What this repo does are:
- Study kubernetes scheduler following [自作して学ぶKubernetes Scheduler](https://engineering.mercari.com/blog/entry/20211220-create-your-kube-scheduler/) with https://github.com/sanposhiho/mini-kube-scheduler.
- Simplify the original repo https://github.com/sanposhiho/mini-kube-scheduler

## Versions

- Go: 1.17
- Kubernetes: [1.23.4](https://github.com/kubernetes/kubernetes/releases/tag/v1.23.4)

## Components

- `Scheduler`
    - `SchedulingQueue`
    - `client`
- `Informer` with `Handler` (`Scheduler.addPodToSchedulingQueue()`) + `FilterFunc`
- `Service`:
    - Initialize Scheduler and set the event handler for informer with `New()`
    - Call `Scheduler.Run()` to start `Scheduler`.

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

## [1. Initial Random Scheduler](https://github.com/nakamasato/mini-kube-scheduler/tree/01-initial-random-scheduler)

### 1.1. Initialize project

1. Init module

    ```
    go mod init github.com/nakamasato/mini-kube-scheduler
    ```
1. Create directory for mini scheduler

    ```
    mkdir minisched
    ```

### 1.2. Create Scheduler

1. Create `SchedulingQueue` in `minisched/queue/queue.go`.

    <details>

    ```go
    package queue

    import (
        "sync"

        v1 "k8s.io/api/core/v1"
    )

    type SchedulingQueue struct {
        activeQ []*v1.Pod
        lock    *sync.Cond
    }

    func New() *SchedulingQueue {
        return &SchedulingQueue{
            activeQ: []*v1.Pod{},
            lock:    sync.NewCond(&sync.Mutex{}),
        }
    }

    func (s *SchedulingQueue) Add(pod *v1.Pod) {
        s.lock.L.Lock()
        defer s.lock.L.Unlock()

        s.activeQ = append(s.activeQ, pod)
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
        return p
    }
    ```

    </details>

1. Create `Scheduler` in `minisched/initialize.go`.

    <details>

    ```go
    package minisched

    import (
        "github.com/nakamasato/mini-kube-scheduler/minisched/queue"
        "k8s.io/client-go/informers"
        clientset "k8s.io/client-go/kubernetes"
    )

    type Scheduler struct {
        SchedulingQueue *queue.SchedulingQueue

        client clientset.Interface
    }

    func New(
        client clientset.Interface,
        informerFactory informers.SharedInformerFactory,
    ) *Scheduler {
        sched := &Scheduler{
            SchedulingQueue: queue.New(),
            client:          client,
        }

        addAllEventHandlers(sched, informerFactory)

        return sched
    }
    ```

    </details>

1. Write event handlers `minisched/eventhandler.go`

    <details>

    ```go
    package minisched

    import (
        v1 "k8s.io/api/core/v1"
        "k8s.io/client-go/informers"
        "k8s.io/client-go/tools/cache"
    )

    func addAllEventHandlers(
        sched *Scheduler,
        informerFactory informers.SharedInformerFactory,
    ) {
        // unscheduled pod
        informerFactory.Core().V1().Pods().Informer().AddEventHandler(
            cache.FilteringResourceEventHandler{
                FilterFunc: func(obj interface{}) bool {
                    switch t := obj.(type) {
                    case *v1.Pod:
                        return !assignedPod(t) // まだどこにもアサインされていないPodにtrueを返す
                    default:
                        return false
                    }
                },
                Handler: cache.ResourceEventHandlerFuncs{
                    // FilterFuncでtrueが返ってきた新しいPodにこれを実行する
                    AddFunc: sched.addPodToSchedulingQueue,
                },
            },
        )
    }

    // assignedPod selects pods that are assigned (scheduled and running).
    func assignedPod(pod *v1.Pod) bool {
        return len(pod.Spec.NodeName) != 0
    }

    func (sched *Scheduler) addPodToSchedulingQueue(obj interface{}) {
        pod, ok := obj.(*v1.Pod)
        if !ok {
            return
        }
        sched.SchedulingQueue.Add(pod)
    }
    ```

    </details>

1. Schedule a Pod (`minisched/minisched.go`).

    <details>

    ```go
    package minisched

    import (
        "context"
        "math/rand"
        "time"

        v1 "k8s.io/api/core/v1"
        metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
        "k8s.io/klog/v2"

        "k8s.io/apimachinery/pkg/util/wait"
    )

    func init() {
        rand.Seed(time.Now().UnixNano())
    }

    func (sched *Scheduler) Run(ctx context.Context) {
        wait.UntilWithContext(ctx, sched.scheduleOne, 0)
    }

    func (sched *Scheduler) scheduleOne(ctx context.Context) {
        // 1. Get pod from the scheduling queue
        klog.Info("minischeduler: Try to get pod from queue....")
        pod := sched.SchedulingQueue.NextPod()
        klog.Info("minischeduler: Start schedule(" + pod.Name + ")")

        // 2. Get nodes
        nodes, err := sched.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
        if err != nil {
            klog.Error(err)
            return
        }
        klog.Info("minischeduler: Got Nodes successfully")

        // 3. Select node randomly
        selectedNode := nodes.Items[rand.Intn(len(nodes.Items))]

        // 4. Bind the pod to the randomly selected node
        if err := sched.Bind(ctx, pod, selectedNode.Name); err != nil {
            klog.Error(err)
            return
        }

        klog.Info("minischeduler: Bind Pod successfully")
    }

    func (sched *Scheduler) Bind(ctx context.Context, p *v1.Pod, nodeName string) error {
        binding := &v1.Binding{
            ObjectMeta: metav1.ObjectMeta{Namespace: p.Namespace, Name: p.Name, UID: p.UID},
            Target:     v1.ObjectReference{Kind: "Node", Name: nodeName},
        }

        err := sched.client.CoreV1().Pods(binding.Namespace).Bind(ctx, binding, metav1.CreateOptions{})
        if err != nil {
            return err
        }
        return nil
    }
    ```

    </details>

### 1.3. Prepare dependencies

```
K8S_VERSION=1.23.4
```

1. Add [sched.go](sched.go).
1. Add scheduler.
    1. [scheduler/defaultconfig/defaultconfig.go](scheduler/defaultconfig/defaultconfig.go)
    1. [scheduler/scheduler.go](scheduler/scheduler.go)
1. Add k8sapiserver.
    1. Add [k8sapiserver/k8sapiserver.go](k8sapiserver/k8sapiserver.go).
    1. Generate `k8sapiserver/openapi/zz_generated.openapi.go`.

        ```
        git submodule add https://github.com/kubernetes/kubernetes.git
        cd kubernetes && git checkout $K8S_VERSION # target version
        ./hack/openapi.sh
        ```
1. Run `./update_go_mod.sh` if you get `revision v0.0.0' errors`.

### 1.4. Run

```
make build run
```

1. Build `sched.go`.
1. Start etcd.
1. Run `sched` with `PORT=1212 FRONTEND_URL=http://localhost:3000 ./bin/sched`

<details>

```
make build run
go build -o ./bin/sched ./sched.go
./hack/run.sh
Starting etcd instance
etcd --advertise-client-urls http://127.0.0.1:2379 --data-dir /var/folders/5g/vmdg2t1j2011ggd9p983ns6h0000gn/T/tmp.YyHfDuIu --listen-client-urls http://127.0.0.1:2379 --log-level=debug > "/dev/null" 2>/dev/null
Waiting for etcd to come up.
On try 2, etcd: : {"health":"true","reason":""}
{"header":{"cluster_id":"14841639068965178418","member_id":"10276657743932975437","revision":"2","raft_term":"2"}}etcd started
I0316 11:20:55.217872   38964 instance.go:318] Node port range unspecified. Defaulting to 30000-32767.
I0316 11:20:55.218278   38964 instance.go:274] Using reconciler:
I0316 11:20:55.219451   38964 instance.go:382] Could not construct pre-rendered responses for ServiceAccountIssuerDiscovery endpoints. Endpoints will not be enabled. Error: empty issuer URL
W0316 11:20:55.383743   38964 genericapiserver.go:538] Skipping API authentication.k8s.io/v1beta1 because it has no resources.
W0316 11:20:55.385490   38964 genericapiserver.go:538] Skipping API authorization.k8s.io/v1beta1 because it has no resources.
W0316 11:20:55.399936   38964 genericapiserver.go:538] Skipping API certificates.k8s.io/v1beta1 because it has no resources.
W0316 11:20:55.401555   38964 genericapiserver.go:538] Skipping API coordination.k8s.io/v1beta1 because it has no resources.
W0316 11:20:55.409200   38964 genericapiserver.go:538] Skipping API networking.k8s.io/v1beta1 because it has no resources.
W0316 11:20:55.411930   38964 genericapiserver.go:538] Skipping API node.k8s.io/v1alpha1 because it has no resources.
W0316 11:20:55.416691   38964 genericapiserver.go:538] Skipping API rbac.authorization.k8s.io/v1beta1 because it has no resources.
W0316 11:20:55.416706   38964 genericapiserver.go:538] Skipping API rbac.authorization.k8s.io/v1alpha1 because it has no resources.
W0316 11:20:55.417794   38964 genericapiserver.go:538] Skipping API scheduling.k8s.io/v1beta1 because it has no resources.
W0316 11:20:55.417806   38964 genericapiserver.go:538] Skipping API scheduling.k8s.io/v1alpha1 because it has no resources.
W0316 11:20:55.420820   38964 genericapiserver.go:538] Skipping API storage.k8s.io/v1alpha1 because it has no resources.
W0316 11:20:55.424122   38964 genericapiserver.go:538] Skipping API flowcontrol.apiserver.k8s.io/v1alpha1 because it has no resources.
W0316 11:20:55.427453   38964 genericapiserver.go:538] Skipping API apps/v1beta2 because it has no resources.
W0316 11:20:55.427470   38964 genericapiserver.go:538] Skipping API apps/v1beta1 because it has no resources.
W0316 11:20:55.429035   38964 genericapiserver.go:538] Skipping API admissionregistration.k8s.io/v1beta1 because it has no resources.
I0316 11:20:55.481418   38964 apf_controller.go:317] Starting API Priority and Fairness config controller
I0316 11:20:55.481610   38964 cluster_authentication_trust_controller.go:440] Starting cluster_authentication_trust_controller controller
I0316 11:20:55.481629   38964 shared_informer.go:240] Waiting for caches to sync for cluster_authentication_trust_controller
W0316 11:20:55.482782   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:55.482824   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:55.483012   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
E0316 11:20:55.483076   38964 controller.go:155] Found stale data, removed previous endpoints on kubernetes service, apiserver didn't exit successfully previously
W0316 11:20:55.483402   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:55.483453   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:55.506290   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:55.563612   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
I0316 11:20:55.581807   38964 shared_informer.go:247] Caches are synced for cluster_authentication_trust_controller
I0316 11:20:55.581868   38964 apf_controller.go:322] Running API Priority and Fairness config worker
W0316 11:20:55.583752   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:55.604055   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:55.622413   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:55.623141   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:55.642500   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:55.660619   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:55.729867   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:55.732251   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:55.751335   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:55.751479   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:55.770612   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:55.788562   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:55.789049   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:55.809188   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:55.828234   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:55.828659   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:55.903042   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:55.904575   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:55.906364   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:55.924466   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:55.944081   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:55.945695   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:55.962338   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:55.985147   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:55.985488   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.062546   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.064065   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.065290   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.116941   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.136440   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.137069   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.156164   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.231986   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.232238   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.234184   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.254190   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.254661   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.274690   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.275050   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.298549   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.299030   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.318382   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.318473   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.394744   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.395206   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.397018   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.415926   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.416359   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.418220   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.437005   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.437097   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.460460   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.460675   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.480641   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.481104   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.558224   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.558494   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.559557   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
I0316 11:20:56.560156   38964 storage_scheduling.go:93] created PriorityClass system-node-critical with value 2000001000
W0316 11:20:56.580235   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.580495   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.599334   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
I0316 11:20:56.619886   38964 storage_scheduling.go:93] created PriorityClass system-cluster-critical with value 2000000000
I0316 11:20:56.619913   38964 storage_scheduling.go:109] all system priority classes are created successfully or already exist.
W0316 11:20:56.620082   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.621340   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.639176   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.640977   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.642222   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.643566   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.644785   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.703105   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.705039   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.723952   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.725291   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.743221   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.744369   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.764257   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.765422   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.784507   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.786083   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.842878   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.844523   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.863551   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.865289   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.886373   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.887884   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.907871   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 11:20:56.933195   38964 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 11:21:00.290785   38964 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/pods", Verb:"list", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"pods", Subresource:"", Name:"", Parts:[]string{"pods"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"163ac80e-7fb1-4ac7-aedd-3181a1269f96","resourceVersion":"49","generation":1,"creationTimestamp":"2022-03-16T02:20:56Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T02:20:56Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T02:20:56Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-16T02:20:56Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
W0316 11:21:00.297092   38964 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/pods", Verb:"watch", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"pods", Subresource:"", Name:"", Parts:[]string{"pods"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"163ac80e-7fb1-4ac7-aedd-3181a1269f96","resourceVersion":"49","generation":1,"creationTimestamp":"2022-03-16T02:20:56Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T02:20:56Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T02:20:56Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-16T02:20:56Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
I0316 11:21:00.312180   38964 alloc.go:329] "allocated clusterIPs" service="default/kubernetes" clusterIPs=map[IPv4:10.0.0.1]
I0316 11:21:00.390765   38964 scheduler.go:24] minischeduler: Try to get pod from queue....
W0316 11:21:00.391084   38964 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"163ac80e-7fb1-4ac7-aedd-3181a1269f96","resourceVersion":"49","generation":1,"creationTimestamp":"2022-03-16T02:20:56Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T02:20:56Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T02:20:56Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-16T02:20:56Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
W0316 11:21:00.412779   38964 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"163ac80e-7fb1-4ac7-aedd-3181a1269f96","resourceVersion":"49","generation":1,"creationTimestamp":"2022-03-16T02:20:56Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T02:20:56Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T02:20:56Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-16T02:20:56Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
W0316 11:21:00.430635   38964 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"163ac80e-7fb1-4ac7-aedd-3181a1269f96","resourceVersion":"49","generation":1,"creationTimestamp":"2022-03-16T02:20:56Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T02:20:56Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T02:20:56Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-16T02:20:56Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
W0316 11:21:00.451030   38964 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"163ac80e-7fb1-4ac7-aedd-3181a1269f96","resourceVersion":"49","generation":1,"creationTimestamp":"2022-03-16T02:20:56Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T02:20:56Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T02:20:56Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-16T02:20:56Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
W0316 11:21:00.529368   38964 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"163ac80e-7fb1-4ac7-aedd-3181a1269f96","resourceVersion":"49","generation":1,"creationTimestamp":"2022-03-16T02:20:56Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T02:20:56Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T02:20:56Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-16T02:20:56Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
W0316 11:21:00.531490   38964 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"163ac80e-7fb1-4ac7-aedd-3181a1269f96","resourceVersion":"49","generation":1,"creationTimestamp":"2022-03-16T02:20:56Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T02:20:56Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T02:20:56Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-16T02:20:56Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
W0316 11:21:00.549869   38964 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"163ac80e-7fb1-4ac7-aedd-3181a1269f96","resourceVersion":"49","generation":1,"creationTimestamp":"2022-03-16T02:20:56Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T02:20:56Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T02:20:56Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-16T02:20:56Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
W0316 11:21:00.568516   38964 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"163ac80e-7fb1-4ac7-aedd-3181a1269f96","resourceVersion":"49","generation":1,"creationTimestamp":"2022-03-16T02:20:56Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T02:20:56Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T02:20:56Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-16T02:20:56Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
W0316 11:21:00.586560   38964 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"163ac80e-7fb1-4ac7-aedd-3181a1269f96","resourceVersion":"49","generation":1,"creationTimestamp":"2022-03-16T02:20:56Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T02:20:56Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T02:20:56Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-16T02:20:56Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
W0316 11:21:00.612190   38964 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"163ac80e-7fb1-4ac7-aedd-3181a1269f96","resourceVersion":"49","generation":1,"creationTimestamp":"2022-03-16T02:20:56Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T02:20:56Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T02:20:56Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-16T02:20:56Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
I0316 11:21:00.690557   38964 sched.go:88] scenario: all nodes created
W0316 11:21:00.690867   38964 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/namespaces/default/pods", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"default", Resource:"pods", Subresource:"", Name:"", Parts:[]string{"pods"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"163ac80e-7fb1-4ac7-aedd-3181a1269f96","resourceVersion":"49","generation":1,"creationTimestamp":"2022-03-16T02:20:56Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T02:20:56Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T02:20:56Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-16T02:20:56Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
I0316 11:21:00.693673   38964 sched.go:105] scenario: pod1 created
I0316 11:21:00.693772   38964 scheduler.go:26] minischeduler: Start schedule(pod1)
W0316 11:21:00.694054   38964 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"list", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"163ac80e-7fb1-4ac7-aedd-3181a1269f96","resourceVersion":"49","generation":1,"creationTimestamp":"2022-03-16T02:20:56Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T02:20:56Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T02:20:56Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-16T02:20:56Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
I0316 11:21:00.711841   38964 scheduler.go:34] minischeduler: Got Nodes successfully
W0316 11:21:00.712084   38964 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/namespaces/default/pods/pod1/binding", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"default", Resource:"pods", Subresource:"binding", Name:"pod1", Parts:[]string{"pods", "pod1", "binding"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"163ac80e-7fb1-4ac7-aedd-3181a1269f96","resourceVersion":"49","generation":1,"creationTimestamp":"2022-03-16T02:20:56Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T02:20:56Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T02:20:56Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-16T02:20:56Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
I0316 11:21:00.713854   38964 scheduler.go:44] minischeduler: Bind Pod successfully
I0316 11:21:00.713891   38964 scheduler.go:24] minischeduler: Try to get pod from queue....
W0316 11:21:04.695486   38964 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/namespaces/default/pods/pod1", Verb:"get", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"default", Resource:"pods", Subresource:"", Name:"pod1", Parts:[]string{"pods", "pod1"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"163ac80e-7fb1-4ac7-aedd-3181a1269f96","resourceVersion":"49","generation":1,"creationTimestamp":"2022-03-16T02:20:56Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T02:20:56Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T02:20:56Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-16T02:20:56Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
I0316 11:21:04.703590   38964 sched.go:115] scenario: pod1 is bound to node5
I0316 11:21:04.703620   38964 scheduler.go:73] shutdown scheduler...
I0316 11:21:04.703632   38964 k8sapiserver.go:65] destroying API server
I0316 11:21:04.703654   38964 controller.go:186] Shutting down kubernetes service endpoint reconciler
I0316 11:21:04.721989   38964 apf_controller.go:326] Shutting down API Priority and Fairness config worker
I0316 11:21:04.722017   38964 cluster_authentication_trust_controller.go:463] Shutting down cluster_authentication_trust_controller controller
I0316 11:21:04.722077   38964 storage_flowcontrol.go:150] APF bootstrap ensurer is exiting
I0316 11:21:04.722287   38964 k8sapiserver.go:68] destroyed API server
Cleaning up etcd
Clean up finished
```

</details>

## [2. Filter Plugins](https://github.com/nakamasato/mini-kube-scheduler/tree/02-filter-plugins)

### 2.1. Add Nodename Filter Plugin

1. Update `minisched/initialize.go`.

    1. Add necessary packages.
    1. Add `filterPlugins` https://pkg.go.dev/k8s.io/kubernetes@v1.23.4/pkg/scheduler/framework/plugins/nodename to `Scheduler`.
    1. Change the return value of `New()` to `(*Scheduler, error)`
    1. Add `createFilterPlugins()`.

    <details>

    ```diff
     import (
    +       "fmt"
    +
            "github.com/nakamasato/mini-kube-scheduler/minisched/queue"
            "k8s.io/client-go/informers"
            clientset "k8s.io/client-go/kubernetes"
    +       "k8s.io/kubernetes/pkg/scheduler/framework"
    +       "k8s.io/kubernetes/pkg/scheduler/framework/plugins/nodename"
     )

     type Scheduler struct {
            SchedulingQueue *queue.SchedulingQueue

            client clientset.Interface
    +
    +       filterPlugins []framework.FilterPlugin
     }
    -) *Scheduler {
    +) (*Scheduler, error) {
    +       filterP, err := createFilterPlugins()
    +       if err != nil {
    +               return nil, fmt.Errorf("create filter plugins: %w", err)
    +       }
            sched := &Scheduler{
                    SchedulingQueue: queue.New(),
                    client:          client,
    +               filterPlugins:   filterP,
            }

            addAllEventHandlers(sched, informerFactory)

    -       return sched
    +       return sched, nil
    +}
    +
    +func createFilterPlugins() ([]framework.FilterPlugin, error) {
    +       // nodename is FilterPlugin.
    +       nodenameplugin, err := nodename.New(nil, nil)
    +       if err != nil {
    +               return nil, fmt.Errorf("create nodename plugin: %w", err)
    +       }
    +
    +       // We use nodename plugin only.
    +       filterPlugins := []framework.FilterPlugin{
    +               nodenameplugin.(framework.FilterPlugin),
    +       }
    +
    +       return filterPlugins, nil
     }
    ```

    </details>

1. Update `scheduler/scheduler.go`.

    <details>

    ```diff
    -       sched := minisched.New(
    +       sched, err := minisched.New(
                    clientSet,
                    informerFactory,
            )

    +       if err != nil {
    +               cancel()
    +               return fmt.Errorf("create minisched: %w", err)
    +       }
    +
    ```

    </details>

1. Add `NodeName: "node9",` to `PodSpec` in `sched.go`.

    ```go
    Spec: v1.PodSpec{
        Containers: []v1.Container{
            {
                Name:  "container1",
                Image: "k8s.gcr.io/pause:3.5",
            },
        },
        // this pod will be bound to node9 with nodename plugin
        NodeName: "node9",
    },
    ```

### 2.2. Run

```
make build run
```

<details>

```
go build -o ./bin/sched ./sched.go
./hack/run.sh
Starting etcd instance
etcd --advertise-client-urls http://127.0.0.1:2379 --data-dir /var/folders/5g/vmdg2t1j2011ggd9p983ns6h0000gn/T/tmp.9Oznfzgu --listen-client-urls http://127.0.0.1:2379 --log-level=debug > "/dev/null" 2>/dev/null
Waiting for etcd to come up.
On try 2, etcd: : {"health":"true","reason":""}
{"header":{"cluster_id":"14841639068965178418","member_id":"10276657743932975437","revision":"2","raft_term":"2"}}etcd started
I0316 21:18:16.820390   65794 instance.go:318] Node port range unspecified. Defaulting to 30000-32767.
I0316 21:18:16.820662   65794 instance.go:274] Using reconciler:
I0316 21:18:16.822009   65794 instance.go:382] Could not construct pre-rendered responses for ServiceAccountIssuerDiscovery endpoints. Endpoints will not be enabled. Error: empty issuer URL
W0316 21:18:17.043378   65794 genericapiserver.go:538] Skipping API authentication.k8s.io/v1beta1 because it has no resources.
W0316 21:18:17.045348   65794 genericapiserver.go:538] Skipping API authorization.k8s.io/v1beta1 because it has no resources.
W0316 21:18:17.060728   65794 genericapiserver.go:538] Skipping API certificates.k8s.io/v1beta1 because it has no resources.
W0316 21:18:17.063130   65794 genericapiserver.go:538] Skipping API coordination.k8s.io/v1beta1 because it has no resources.
W0316 21:18:17.070550   65794 genericapiserver.go:538] Skipping API networking.k8s.io/v1beta1 because it has no resources.
W0316 21:18:17.076981   65794 genericapiserver.go:538] Skipping API node.k8s.io/v1alpha1 because it has no resources.
W0316 21:18:17.083601   65794 genericapiserver.go:538] Skipping API rbac.authorization.k8s.io/v1beta1 because it has no resources.
W0316 21:18:17.083624   65794 genericapiserver.go:538] Skipping API rbac.authorization.k8s.io/v1alpha1 because it has no resources.
W0316 21:18:17.085167   65794 genericapiserver.go:538] Skipping API scheduling.k8s.io/v1beta1 because it has no resources.
W0316 21:18:17.085185   65794 genericapiserver.go:538] Skipping API scheduling.k8s.io/v1alpha1 because it has no resources.
W0316 21:18:17.089050   65794 genericapiserver.go:538] Skipping API storage.k8s.io/v1alpha1 because it has no resources.
W0316 21:18:17.093015   65794 genericapiserver.go:538] Skipping API flowcontrol.apiserver.k8s.io/v1alpha1 because it has no resources.
W0316 21:18:17.096891   65794 genericapiserver.go:538] Skipping API apps/v1beta2 because it has no resources.
W0316 21:18:17.096912   65794 genericapiserver.go:538] Skipping API apps/v1beta1 because it has no resources.
W0316 21:18:17.098782   65794 genericapiserver.go:538] Skipping API admissionregistration.k8s.io/v1beta1 because it has no resources.
I0316 21:18:17.159960   65794 apf_controller.go:317] Starting API Priority and Fairness config controller
I0316 21:18:17.160089   65794 cluster_authentication_trust_controller.go:440] Starting cluster_authentication_trust_controller controller
I0316 21:18:17.160108   65794 shared_informer.go:240] Waiting for caches to sync for cluster_authentication_trust_controller
W0316 21:18:17.161705   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.161804   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.162549   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.162720   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.162772   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
E0316 21:18:17.162882   65794 controller.go:155] Found stale data, removed previous endpoints on kubernetes service, apiserver didn't exit successfully previously
W0316 21:18:17.166044   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.224358   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.244697   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
I0316 21:18:17.262369   65794 apf_controller.go:322] Running API Priority and Fairness config worker
I0316 21:18:17.262419   65794 shared_informer.go:247] Caches are synced for cluster_authentication_trust_controller
W0316 21:18:17.264374   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.265942   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.285170   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.285388   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.304799   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.322445   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.394746   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.395848   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.416178   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.416224   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.435361   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.435729   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.455521   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.474404   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.474664   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.493694   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.585423   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.585658   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.586994   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.606841   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.607274   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.625129   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.643579   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.643952   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.663494   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.682431   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.682679   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.774473   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.775354   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.776623   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.796463   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.814544   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.814801   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.833932   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.853415   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.853716   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.872713   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.873060   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.947454   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.947665   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:17.949191   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:18.003267   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:18.003442   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:18.021927   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:18.022132   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:18.040607   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:18.040908   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:18.132800   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:18.132871   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:18.133783   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:18.135302   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:18.154701   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:18.154792   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:18.173692   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:18.174106   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:18.193718   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:18.193891   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
I0316 21:18:18.213271   65794 storage_scheduling.go:93] created PriorityClass system-node-critical with value 2000001000
W0316 21:18:18.213482   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:18.231646   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:18.289595   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
I0316 21:18:18.289706   65794 storage_scheduling.go:93] created PriorityClass system-cluster-critical with value 2000000000
I0316 21:18:18.289722   65794 storage_scheduling.go:109] all system priority classes are created successfully or already exist.
W0316 21:18:18.308797   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:18.328167   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:18.329416   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:18.346979   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:18.348395   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:18.349543   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:18.350525   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:18.351500   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:18.370859   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:18.372092   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:18.463115   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:18.464283   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:18.483539   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:18.503583   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:18.504694   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:18.524488   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:18.525777   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:18.545407   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:18.546615   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:18.603668   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:18.604889   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:18.628795   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:18.630092   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:18.649638   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:18.651722   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:18.669921   65794 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0316 21:18:22.079818   65794 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/pods", Verb:"list", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"pods", Subresource:"", Name:"", Parts:[]string{"pods"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"f3bce657-e96b-4aa8-851a-9fd7a0f4678d","resourceVersion":"50","generation":1,"creationTimestamp":"2022-03-16T12:18:18Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T12:18:18Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T12:18:18Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-16T12:18:18Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
W0316 21:18:22.084766   65794 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/pods", Verb:"watch", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"pods", Subresource:"", Name:"", Parts:[]string{"pods"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"f3bce657-e96b-4aa8-851a-9fd7a0f4678d","resourceVersion":"50","generation":1,"creationTimestamp":"2022-03-16T12:18:18Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T12:18:18Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T12:18:18Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-16T12:18:18Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
I0316 21:18:22.179822   65794 scheduler.go:24] minischeduler: Try to get pod from queue....
W0316 21:18:22.180304   65794 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"f3bce657-e96b-4aa8-851a-9fd7a0f4678d","resourceVersion":"50","generation":1,"creationTimestamp":"2022-03-16T12:18:18Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T12:18:18Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T12:18:18Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-16T12:18:18Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
I0316 21:18:22.181288   65794 alloc.go:329] "allocated clusterIPs" service="default/kubernetes" clusterIPs=map[IPv4:10.0.0.1]
W0316 21:18:22.203103   65794 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"f3bce657-e96b-4aa8-851a-9fd7a0f4678d","resourceVersion":"50","generation":1,"creationTimestamp":"2022-03-16T12:18:18Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T12:18:18Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T12:18:18Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-16T12:18:18Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
W0316 21:18:22.237093   65794 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"f3bce657-e96b-4aa8-851a-9fd7a0f4678d","resourceVersion":"50","generation":1,"creationTimestamp":"2022-03-16T12:18:18Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T12:18:18Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T12:18:18Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-16T12:18:18Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
W0316 21:18:22.256243   65794 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"f3bce657-e96b-4aa8-851a-9fd7a0f4678d","resourceVersion":"50","generation":1,"creationTimestamp":"2022-03-16T12:18:18Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T12:18:18Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T12:18:18Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-16T12:18:18Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
W0316 21:18:22.329919   65794 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"f3bce657-e96b-4aa8-851a-9fd7a0f4678d","resourceVersion":"50","generation":1,"creationTimestamp":"2022-03-16T12:18:18Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T12:18:18Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T12:18:18Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-16T12:18:18Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
W0316 21:18:22.331647   65794 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"f3bce657-e96b-4aa8-851a-9fd7a0f4678d","resourceVersion":"50","generation":1,"creationTimestamp":"2022-03-16T12:18:18Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T12:18:18Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T12:18:18Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-16T12:18:18Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
W0316 21:18:22.350289   65794 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"f3bce657-e96b-4aa8-851a-9fd7a0f4678d","resourceVersion":"50","generation":1,"creationTimestamp":"2022-03-16T12:18:18Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T12:18:18Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T12:18:18Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-16T12:18:18Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
W0316 21:18:22.369866   65794 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"f3bce657-e96b-4aa8-851a-9fd7a0f4678d","resourceVersion":"50","generation":1,"creationTimestamp":"2022-03-16T12:18:18Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T12:18:18Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T12:18:18Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-16T12:18:18Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
W0316 21:18:22.388774   65794 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"f3bce657-e96b-4aa8-851a-9fd7a0f4678d","resourceVersion":"50","generation":1,"creationTimestamp":"2022-03-16T12:18:18Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T12:18:18Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T12:18:18Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-16T12:18:18Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
W0316 21:18:22.408201   65794 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"f3bce657-e96b-4aa8-851a-9fd7a0f4678d","resourceVersion":"50","generation":1,"creationTimestamp":"2022-03-16T12:18:18Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T12:18:18Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T12:18:18Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-16T12:18:18Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
I0316 21:18:22.426730   65794 sched.go:88] scenario: all nodes created
W0316 21:18:22.426972   65794 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/namespaces/default/pods", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"default", Resource:"pods", Subresource:"", Name:"", Parts:[]string{"pods"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"f3bce657-e96b-4aa8-851a-9fd7a0f4678d","resourceVersion":"50","generation":1,"creationTimestamp":"2022-03-16T12:18:18Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T12:18:18Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T12:18:18Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-16T12:18:18Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
I0316 21:18:22.518665   65794 sched.go:107] scenario: pod1 created
W0316 21:18:26.523955   65794 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/namespaces/default/pods/pod1", Verb:"get", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"default", Resource:"pods", Subresource:"", Name:"pod1", Parts:[]string{"pods", "pod1"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"f3bce657-e96b-4aa8-851a-9fd7a0f4678d","resourceVersion":"50","generation":1,"creationTimestamp":"2022-03-16T12:18:18Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T12:18:18Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-16T12:18:18Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-16T12:18:18Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
I0316 21:18:26.534293   65794 sched.go:117] scenario: pod1 is bound to node9
I0316 21:18:26.534325   65794 scheduler.go:78] shutdown scheduler...
I0316 21:18:26.534340   65794 k8sapiserver.go:65] destroying API server
I0316 21:18:26.534374   65794 controller.go:186] Shutting down kubernetes service endpoint reconciler
I0316 21:18:26.562896   65794 storage_flowcontrol.go:150] APF bootstrap ensurer is exiting
I0316 21:18:26.562908   65794 cluster_authentication_trust_controller.go:463] Shutting down cluster_authentication_trust_controller controller
I0316 21:18:26.562931   65794 apf_controller.go:326] Shutting down API Priority and Fairness config worker
I0316 21:18:26.563163   65794 k8sapiserver.go:68] destroyed API server
Cleaning up etcd
Clean up finished
```

</details>

You'll see `pod1 is bound to node9`.

## Tips

### kube-apiserver

Use https://github.com/kubernetes/kubernetes to generate [k8sapiserver/openapizz_generated.openapi.go](k8sapiserver/openapizz_generated.openapi.go).

### Go module error

If you get [unknown revision v0.0.0' errors, seemingly due to 'require k8s.io/foo v0.0.0' #79384](https://github.com/kubernetes/kubernetes/issues/79384#issuecomment-521493597), run the command:

```
./update_go_mod.sh <k8s_version> # e.g. 1.23.4
```

### Plugins

You can use [default plugins](https://kubernetes.io/docs/reference/scheduling/config/#scheduling-plugins) or implement your own plugins

## References
1. kubernetes.io/docs
    1. https://kubernetes.io/docs/concepts/scheduling-eviction/scheduling-framework
    1. https://kubernetes.io/docs/reference/scheduling/config
1. github.com
    1. https://github.com/sanposhiho/mini-kube-scheduler
    1. https://github.com/draios/kubernetes-scheduler
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
