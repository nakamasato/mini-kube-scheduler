## [4. PreScore Plugins](https://github.com/nakamasato/mini-kube-scheduler/tree/04-score-plugins)

`preScore`: This is an informational extension point that can be used for doing pre-scoring work.

PreScore Plugins need to implement `PreScorePlugin` interface:

```go
// PreScorePlugin is an interface for "PreScore" plugin. PreScore is an
// informational extension point. Plugins will be called with a list of nodes
// that passed the filtering phase. A plugin may use this data to update internal
// state or to generate logs/metrics.
type PreScorePlugin interface {
	Plugin
	// PreScore is called by the scheduling framework after a list of nodes
	// passed the filtering phase. All prescore plugins must return success or
	// the pod will be rejected
	PreScore(ctx context.Context, state *CycleState, pod *v1.Pod, nodes []*v1.Node) *Status
}
```

https://github.com/kubernetes/kubernetes/blob/4d08582d1fa21e1f5887e73380001ac827371553/pkg/scheduler/framework/interface.go#L394-L404

This example just moves the logic to get number from the last character of Pod name to `PreScorePlugin` from `ScorePlugin` and passes the information through `CycleState`.

### 4.1. Add PreScore to NodeNumber plugin.

1. Add `preScoreState`, `PreScore` function to `NodeNumber`.
    ```go
    // preScoreState computed at PreScore and used at Score.
    type preScoreState struct {
        podSuffixNumber int
    }

    // Clone implements the mandatory Clone interface. We don't really copy the data since
    // there is no need for that.
    func (s *preScoreState) Clone() framework.StateData {
        return s
    }

    func (pl *NodeNumber) PreScore(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodes []*v1.Node) *framework.Status {
        podNameLastChar := pod.Name[len(pod.Name)-1:]
        podnum, err := strconv.Atoi(podNameLastChar)
        if err != nil {
            // return success even if its suffix is non-number.
            return nil
        }

        s := &preScoreState{
            podSuffixNumber: podnum,
        }
        state.Write(preScoreStateKey, s)

        return nil
    }
    ```
1. In `Score`, read data from `CycleState` that was stored in `PreScore`.

    ```go
    // Score invoked at the score extension point.
    func (pl *NodeNumber) Score(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeName string) (int64, *framework.Status) {
        // Get data from CycleState
        data, err := state.Read(preScoreStateKey)
        if err != nil {
            return 0, framework.AsStatus(err)
        }

        s, ok := data.(*preScoreState)
        if !ok {
            return 0, framework.AsStatus(errors.New("failed to convert pre score state"))
        }

        nodeNameLastChar := nodeName[len(nodeName)-1:]

        nodenum, err := strconv.Atoi(nodeNameLastChar)
        if err != nil {
            // return success even if its suffix is non-number.
            return 0, nil
        }

        if s.podSuffixNumber == nodenum {
            // if match, node get high score.
            return 10, nil
        }

        return 0, nil
    }
    ```

### 4.2. Add PreScorePlugins to Scheduler

1. Add `preScorePlugins` to `Scheduler` in [minisched/initialize.go](minisched/initialize.go)

    ```diff
    +++ b/minisched/initialize.go
    @@ -17,7 +17,7 @@ type Scheduler struct {
            client clientset.Interface

            filterPlugins []framework.FilterPlugin
    -
    +       preScorePlugins []framework.PreScorePlugin
            scorePlugins []framework.ScorePlugin
     }

    @@ -31,6 +31,13 @@ func New(
                    return nil, fmt.Errorf("create filter plugins: %w", err)
            }

    +       // prescore plugin
    +       preScoreP, err := createPreScorePlugins()
    +       if err != nil {
    +               return nil, fmt.Errorf("create pre score plugins: %w", err)
    +       }
    +
    +
            // score plugin
            scoreP, err := createScorePlugins()
            if err != nil {
    @@ -41,6 +48,7 @@ func New(
                    SchedulingQueue: queue.New(),
                    client:          client,
                    filterPlugins:   filterP,
    +               preScorePlugins: preScoreP,
                    scorePlugins:    scoreP,
            }
    ```

    Add `createPreScorePlugins`:

    ```go
    func createPreScorePlugins() ([]framework.PreScorePlugin, error) {
    	// nodenumber is FilterPlugin.
    	nodenumberplugin, err := createNodeNumberPlugin()
    	if err != nil {
    		return nil, fmt.Errorf("create nodenumber plugin: %w", err)
    	}

    	// We use nodenumber plugin only.
    	preScorePlugins := []framework.PreScorePlugin{
    		nodenumberplugin.(framework.PreScorePlugin),
    	}

    	return preScorePlugins, nil
    }
    ```

### 4.3. Use PreScorePlugins in Scheduler [minisched/scheduler.go](minisched/scheduler.go)

1. Add a logic to execute PreScorePlugin in `ScheduleOne`.

    ```diff
    @@ -28,6 +28,8 @@ func (sched *Scheduler) scheduleOne(ctx context.Context) {
            pod := sched.SchedulingQueue.NextPod()
            klog.Info("minischeduler: Start schedule(" + pod.Name + ")")

    +       state := framework.NewCycleState()
    +
            // get nodes
            nodes, err := sched.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
            if err != nil {
    @@ -47,6 +49,14 @@ func (sched *Scheduler) scheduleOne(ctx context.Context) {
            klog.Info("minischeduler: ran filter plugins successfully")
            klog.Info("minischeduler: feasible nodes: ", len(feasibleNodes))

    +       // pre score
    +       status := sched.RunPreScorePlugins(ctx, state, pod, feasibleNodes)
    +       if !status.IsSuccess() {
    +               klog.Error(status.AsError())
    +               return
    +       }
    +       klog.Info("minischeduler: ran pre score plugins successfully")

    -       score, status := sched.RunScorePlugins(ctx, nil, pod, feasibleNodes)
    +       score, status := sched.RunScorePlugins(ctx, state, pod, feasibleNodes)
            if !status.IsSuccess() {
                    klog.Error(status.AsError())
                    return
    ```

1. Add `RunPreScorePlugins` function.

    ```go
    func (sched *Scheduler) RunPreScorePlugins(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodes []*v1.Node) *framework.Status {
        for _, pl := range sched.preScorePlugins {
            status := pl.PreScore(ctx, state, pod, nodes)
            if !status.IsSuccess() {
                return status
            }
        }

        return nil
    }
    ```

## 4.4. Run

Conditions:
- Nodes:
    - node0~node4: unschedulable
    - node5~node9: schedulable
- FilterPlugin: `NodeName`
- Pods:
    - `pod1`
    - `pod8`
- Expect:
    - `pod1` is assigned to one of node5~node9 randomly.
    - `pod8` is assigned to `node8`.

This case is exactly the same as the previous one.

```
make build run
```

<details>

```
go build -o ./bin/sched ./sched.go
./hack/run.sh
Starting etcd instance
etcd --advertise-client-urls http://127.0.0.1:2379 --data-dir /var/folders/5g/vmdg2t1j2011ggd9p983ns6h0000gn/T/tmp.YFHeFj4e --listen-client-urls http://127.0.0.1:2379 --log-level=debug > "/dev/null" 2>/dev/null
Waiting for etcd to come up.
On try 2, etcd: : {"health":"true","reason":""}
{"header":{"cluster_id":"14841639068965178418","member_id":"10276657743932975437","revision":"2","raft_term":"2"}}etcd started
I0318 07:22:27.733004   21760 instance.go:318] Node port range unspecified. Defaulting to 30000-32767.
I0318 07:22:27.733462   21760 instance.go:274] Using reconciler:
I0318 07:22:27.734927   21760 instance.go:382] Could not construct pre-rendered responses for ServiceAccountIssuerDiscovery endpoints. Endpoints will not be enabled. Error: empty issuer URL
W0318 07:22:27.926889   21760 genericapiserver.go:538] Skipping API authentication.k8s.io/v1beta1 because it has no resources.
W0318 07:22:27.928394   21760 genericapiserver.go:538] Skipping API authorization.k8s.io/v1beta1 because it has no resources.
W0318 07:22:27.942869   21760 genericapiserver.go:538] Skipping API certificates.k8s.io/v1beta1 because it has no resources.
W0318 07:22:27.944716   21760 genericapiserver.go:538] Skipping API coordination.k8s.io/v1beta1 because it has no resources.
W0318 07:22:27.952952   21760 genericapiserver.go:538] Skipping API networking.k8s.io/v1beta1 because it has no resources.
W0318 07:22:27.958110   21760 genericapiserver.go:538] Skipping API node.k8s.io/v1alpha1 because it has no resources.
W0318 07:22:27.963987   21760 genericapiserver.go:538] Skipping API rbac.authorization.k8s.io/v1beta1 because it has no resources.
W0318 07:22:27.964010   21760 genericapiserver.go:538] Skipping API rbac.authorization.k8s.io/v1alpha1 because it has no resources.
W0318 07:22:27.965636   21760 genericapiserver.go:538] Skipping API scheduling.k8s.io/v1beta1 because it has no resources.
W0318 07:22:27.965654   21760 genericapiserver.go:538] Skipping API scheduling.k8s.io/v1alpha1 because it has no resources.
W0318 07:22:27.970291   21760 genericapiserver.go:538] Skipping API storage.k8s.io/v1alpha1 because it has no resources.
W0318 07:22:27.975056   21760 genericapiserver.go:538] Skipping API flowcontrol.apiserver.k8s.io/v1alpha1 because it has no resources.
W0318 07:22:27.979269   21760 genericapiserver.go:538] Skipping API apps/v1beta2 because it has no resources.
W0318 07:22:27.979293   21760 genericapiserver.go:538] Skipping API apps/v1beta1 because it has no resources.
W0318 07:22:27.981610   21760 genericapiserver.go:538] Skipping API admissionregistration.k8s.io/v1beta1 because it has no resources.
I0318 07:22:28.046498   21760 apf_controller.go:317] Starting API Priority and Fairness config controller
I0318 07:22:28.047016   21760 cluster_authentication_trust_controller.go:440] Starting cluster_authentication_trust_controller controller
I0318 07:22:28.047039   21760 shared_informer.go:240] Waiting for caches to sync for cluster_authentication_trust_controller
W0318 07:22:28.048472   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.048510   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.048861   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
E0318 07:22:28.049021   21760 controller.go:155] Found stale data, removed previous endpoints on kubernetes service, apiserver didn't exit successfully previously
W0318 07:22:28.049412   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.049742   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.055115   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.072723   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.093066   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.116651   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.137791   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
I0318 07:22:28.146620   21760 apf_controller.go:322] Running API Priority and Fairness config worker
I0318 07:22:28.147175   21760 shared_informer.go:247] Caches are synced for cluster_authentication_trust_controller
W0318 07:22:28.194001   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.194242   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.196479   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.216548   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.216689   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.235136   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.235237   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.254038   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.254253   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.273071   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.273339   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.292722   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.359301   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.359386   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.360477   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.361408   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.380923   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.381057   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.401181   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.401429   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.402889   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.422769   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.422953   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.441885   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.441991   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.534851   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.535103   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.536992   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.572492   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.572680   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.591792   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.591920   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.611655   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.611909   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.613367   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.694135   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.694355   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.696871   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.715100   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.715315   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.733110   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.733218   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.752511   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.752891   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.773137   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.773463   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.791846   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.792237   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.869946   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.870137   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.871029   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.873312   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.892185   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.892468   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.894552   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.912906   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.913018   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.932986   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.933120   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.951909   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:28.952749   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:29.012400   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:29.012870   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:29.014784   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:29.015412   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:29.033817   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:29.035093   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:29.036247   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:29.037511   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:29.038661   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:29.039687   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:29.040718   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:29.041843   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:29.042916   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:29.043878   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:29.044784   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:29.045807   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:29.046962   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:29.048152   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:29.049422   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
I0318 07:22:29.049758   21760 storage_scheduling.go:93] created PriorityClass system-node-critical with value 2000001000
W0318 07:22:29.068370   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
I0318 07:22:29.069739   21760 storage_scheduling.go:93] created PriorityClass system-cluster-critical with value 2000000000
I0318 07:22:29.069757   21760 storage_scheduling.go:109] all system priority classes are created successfully or already exist.
W0318 07:22:29.106716   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:29.107689   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:29.109003   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:29.112246   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:29.150369   21760 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0318 07:22:32.952995   21760 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/pods", Verb:"list", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"pods", Subresource:"", Name:"", Parts:[]string{"pods"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"2f10a572-d0af-4fc9-8f8c-bc57e327d666","resourceVersion":"49","generation":1,"creationTimestamp":"2022-03-17T22:22:28Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T22:22:28Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T22:22:28Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-17T22:22:28Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
W0318 07:22:32.959259   21760 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/pods", Verb:"watch", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"pods", Subresource:"", Name:"", Parts:[]string{"pods"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"2f10a572-d0af-4fc9-8f8c-bc57e327d666","resourceVersion":"49","generation":1,"creationTimestamp":"2022-03-17T22:22:28Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T22:22:28Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T22:22:28Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-17T22:22:28Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
I0318 07:22:32.992713   21760 alloc.go:329] "allocated clusterIPs" service="default/kubernetes" clusterIPs=map[IPv4:10.0.0.1]
I0318 07:22:33.052959   21760 scheduler.go:27] minischeduler: Try to get pod from queue....
W0318 07:22:33.053184   21760 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"2f10a572-d0af-4fc9-8f8c-bc57e327d666","resourceVersion":"49","generation":1,"creationTimestamp":"2022-03-17T22:22:28Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T22:22:28Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T22:22:28Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-17T22:22:28Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
W0318 07:22:33.111895   21760 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"2f10a572-d0af-4fc9-8f8c-bc57e327d666","resourceVersion":"49","generation":1,"creationTimestamp":"2022-03-17T22:22:28Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T22:22:28Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T22:22:28Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-17T22:22:28Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
W0318 07:22:33.114457   21760 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"2f10a572-d0af-4fc9-8f8c-bc57e327d666","resourceVersion":"49","generation":1,"creationTimestamp":"2022-03-17T22:22:28Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T22:22:28Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T22:22:28Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-17T22:22:28Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
W0318 07:22:33.134263   21760 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"2f10a572-d0af-4fc9-8f8c-bc57e327d666","resourceVersion":"49","generation":1,"creationTimestamp":"2022-03-17T22:22:28Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T22:22:28Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T22:22:28Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-17T22:22:28Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
W0318 07:22:33.152456   21760 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"2f10a572-d0af-4fc9-8f8c-bc57e327d666","resourceVersion":"49","generation":1,"creationTimestamp":"2022-03-17T22:22:28Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T22:22:28Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T22:22:28Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-17T22:22:28Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
W0318 07:22:33.171375   21760 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"2f10a572-d0af-4fc9-8f8c-bc57e327d666","resourceVersion":"49","generation":1,"creationTimestamp":"2022-03-17T22:22:28Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T22:22:28Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T22:22:28Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-17T22:22:28Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
W0318 07:22:33.189194   21760 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"2f10a572-d0af-4fc9-8f8c-bc57e327d666","resourceVersion":"49","generation":1,"creationTimestamp":"2022-03-17T22:22:28Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T22:22:28Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T22:22:28Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-17T22:22:28Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
W0318 07:22:33.207113   21760 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"2f10a572-d0af-4fc9-8f8c-bc57e327d666","resourceVersion":"49","generation":1,"creationTimestamp":"2022-03-17T22:22:28Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T22:22:28Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T22:22:28Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-17T22:22:28Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
W0318 07:22:33.301386   21760 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"2f10a572-d0af-4fc9-8f8c-bc57e327d666","resourceVersion":"49","generation":1,"creationTimestamp":"2022-03-17T22:22:28Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T22:22:28Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T22:22:28Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-17T22:22:28Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
W0318 07:22:33.303240   21760 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"2f10a572-d0af-4fc9-8f8c-bc57e327d666","resourceVersion":"49","generation":1,"creationTimestamp":"2022-03-17T22:22:28Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T22:22:28Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T22:22:28Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-17T22:22:28Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
I0318 07:22:33.320947   21760 sched.go:105] scenario: all nodes created
W0318 07:22:33.321180   21760 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/namespaces/default/pods", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"default", Resource:"pods", Subresource:"", Name:"", Parts:[]string{"pods"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"2f10a572-d0af-4fc9-8f8c-bc57e327d666","resourceVersion":"49","generation":1,"creationTimestamp":"2022-03-17T22:22:28Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T22:22:28Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T22:22:28Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-17T22:22:28Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
I0318 07:22:33.339215   21760 sched.go:122] scenario: pod1 created
I0318 07:22:33.339235   21760 scheduler.go:29] minischeduler: Start schedule(pod1)
W0318 07:22:33.339439   21760 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/namespaces/default/pods", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"default", Resource:"pods", Subresource:"", Name:"", Parts:[]string{"pods"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"2f10a572-d0af-4fc9-8f8c-bc57e327d666","resourceVersion":"49","generation":1,"creationTimestamp":"2022-03-17T22:22:28Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T22:22:28Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T22:22:28Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-17T22:22:28Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
W0318 07:22:33.339687   21760 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"list", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"2f10a572-d0af-4fc9-8f8c-bc57e327d666","resourceVersion":"49","generation":1,"creationTimestamp":"2022-03-17T22:22:28Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T22:22:28Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T22:22:28Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-17T22:22:28Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
I0318 07:22:33.357055   21760 sched.go:139] scenario: pod1 created
I0318 07:22:33.357587   21760 scheduler.go:39] minischeduler: Got Nodes successfully
I0318 07:22:33.357602   21760 scheduler.go:40] minischeduler: got nodes: 10
I0318 07:22:33.357631   21760 scheduler.go:49] minischeduler: ran filter plugins successfully
I0318 07:22:33.357637   21760 scheduler.go:50] minischeduler: feasible nodes: 5
I0318 07:22:33.357644   21760 scheduler.go:58] minischeduler: ran pre score plugins successfully
I0318 07:22:33.357652   21760 scheduler.go:154] ScorePlugin: NodeNumber, pod: pod1, node: node5, score: 0
I0318 07:22:33.357657   21760 scheduler.go:154] ScorePlugin: NodeNumber, pod: pod1, node: node6, score: 0
I0318 07:22:33.357661   21760 scheduler.go:154] ScorePlugin: NodeNumber, pod: pod1, node: node7, score: 0
I0318 07:22:33.357665   21760 scheduler.go:154] ScorePlugin: NodeNumber, pod: pod1, node: node8, score: 0
I0318 07:22:33.357669   21760 scheduler.go:154] ScorePlugin: NodeNumber, pod: pod1, node: node9, score: 0
I0318 07:22:33.357675   21760 scheduler.go:67] minischeduler: ran score plugins successfully
I0318 07:22:33.357680   21760 scheduler.go:68] minischeduler: score results[{node5 0} {node6 0} {node7 0} {node8 0} {node9 0}]
W0318 07:22:33.357940   21760 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/namespaces/default/pods/pod1/binding", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"default", Resource:"pods", Subresource:"binding", Name:"pod1", Parts:[]string{"pods", "pod1", "binding"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"2f10a572-d0af-4fc9-8f8c-bc57e327d666","resourceVersion":"49","generation":1,"creationTimestamp":"2022-03-17T22:22:28Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T22:22:28Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T22:22:28Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-17T22:22:28Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
I0318 07:22:33.376982   21760 scheduler.go:82] minischeduler: Bind Pod successfully
I0318 07:22:33.377009   21760 scheduler.go:27] minischeduler: Try to get pod from queue....
I0318 07:22:33.377016   21760 scheduler.go:29] minischeduler: Start schedule(pod8)
W0318 07:22:33.377197   21760 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"list", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"2f10a572-d0af-4fc9-8f8c-bc57e327d666","resourceVersion":"49","generation":1,"creationTimestamp":"2022-03-17T22:22:28Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T22:22:28Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T22:22:28Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-17T22:22:28Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
I0318 07:22:33.395794   21760 scheduler.go:39] minischeduler: Got Nodes successfully
I0318 07:22:33.395811   21760 scheduler.go:40] minischeduler: got nodes: 10
I0318 07:22:33.395838   21760 scheduler.go:49] minischeduler: ran filter plugins successfully
I0318 07:22:33.395843   21760 scheduler.go:50] minischeduler: feasible nodes: 5
I0318 07:22:33.395856   21760 scheduler.go:58] minischeduler: ran pre score plugins successfully
I0318 07:22:33.395862   21760 scheduler.go:154] ScorePlugin: NodeNumber, pod: pod8, node: node5, score: 0
I0318 07:22:33.395867   21760 scheduler.go:154] ScorePlugin: NodeNumber, pod: pod8, node: node6, score: 0
I0318 07:22:33.395871   21760 scheduler.go:154] ScorePlugin: NodeNumber, pod: pod8, node: node7, score: 0
I0318 07:22:33.395875   21760 scheduler.go:154] ScorePlugin: NodeNumber, pod: pod8, node: node8, score: 10
I0318 07:22:33.395879   21760 scheduler.go:154] ScorePlugin: NodeNumber, pod: pod8, node: node9, score: 0
I0318 07:22:33.395885   21760 scheduler.go:67] minischeduler: ran score plugins successfully
I0318 07:22:33.395890   21760 scheduler.go:68] minischeduler: score results[{node5 0} {node6 0} {node7 0} {node8 10} {node9 0}]
W0318 07:22:33.396200   21760 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/namespaces/default/pods/pod8/binding", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"default", Resource:"pods", Subresource:"binding", Name:"pod8", Parts:[]string{"pods", "pod8", "binding"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"2f10a572-d0af-4fc9-8f8c-bc57e327d666","resourceVersion":"49","generation":1,"creationTimestamp":"2022-03-17T22:22:28Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T22:22:28Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T22:22:28Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-17T22:22:28Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
I0318 07:22:33.398198   21760 scheduler.go:82] minischeduler: Bind Pod successfully
I0318 07:22:33.398237   21760 scheduler.go:27] minischeduler: Try to get pod from queue....
W0318 07:22:37.361650   21760 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/namespaces/default/pods/pod1", Verb:"get", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"default", Resource:"pods", Subresource:"", Name:"pod1", Parts:[]string{"pods", "pod1"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"2f10a572-d0af-4fc9-8f8c-bc57e327d666","resourceVersion":"49","generation":1,"creationTimestamp":"2022-03-17T22:22:28Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T22:22:28Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T22:22:28Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-17T22:22:28Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
I0318 07:22:37.370421   21760 sched.go:149] scenario: pod1 is bound to node5
W0318 07:22:37.370638   21760 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/namespaces/default/pods/pod8", Verb:"get", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"default", Resource:"pods", Subresource:"", Name:"pod8", Parts:[]string{"pods", "pod8"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"2f10a572-d0af-4fc9-8f8c-bc57e327d666","resourceVersion":"49","generation":1,"creationTimestamp":"2022-03-17T22:22:28Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T22:22:28Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T22:22:28Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-17T22:22:28Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
I0318 07:22:37.371698   21760 sched.go:156] scenario: pod8 is bound to node8
I0318 07:22:37.371721   21760 scheduler.go:78] shutdown scheduler...
I0318 07:22:37.371735   21760 k8sapiserver.go:65] destroying API server
I0318 07:22:37.371757   21760 controller.go:186] Shutting down kubernetes service endpoint reconciler
I0318 07:22:37.454214   21760 cluster_authentication_trust_controller.go:463] Shutting down cluster_authentication_trust_controller controller
I0318 07:22:37.454229   21760 apf_controller.go:326] Shutting down API Priority and Fairness config worker
I0318 07:22:37.454250   21760 storage_flowcontrol.go:150] APF bootstrap ensurer is exiting
I0318 07:22:37.454410   21760 k8sapiserver.go:68] destroyed API server
Cleaning up etcd
Clean up finished
```

</details>
