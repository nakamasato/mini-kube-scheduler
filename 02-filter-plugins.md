## [2. Filter Plugins](https://github.com/nakamasato/mini-kube-scheduler/tree/02-filter-plugins)

In this secion, we use [NodeUnschedulable](https://pkg.go.dev/k8s.io/kubernetes@v1.23.4/pkg/scheduler/framework/plugins/nodeunschedulable) plugin, which is one of the [default plugins](https://kubernetes.io/docs/reference/scheduling/config/#scheduling-plugins)

`NodeUnschedulable` plugin filters out nodes that have `.spec.unschedulable` set to true.

for more detail: [scheduler/framework/plugins/nodeunschedulable/node_unschedulable.go](https://github.com/kubernetes/kubernetes/blob/e6c093d87ea4cbb530a7b2ae91e54c0842d8308a/pkg/scheduler/framework/plugins/nodeunschedulable/node_unschedulable.go#L61-L75)

### 2.1. Add `NodeUnschedulable` Filter Plugin

1. Update `minisched/initialize.go`.

    1. Add necessary packages.
        <details>

        ```diff
         import (
        +       "fmt"
        +
                "github.com/nakamasato/mini-kube-scheduler/minisched/queue"
                "k8s.io/client-go/informers"
                clientset "k8s.io/client-go/kubernetes"
        +       "k8s.io/kubernetes/pkg/scheduler/framework"
        +       "k8s.io/kubernetes/pkg/scheduler/framework/plugins/nodeunschedulable"
         )

        ```

        </details>

    1. Add `filterPlugins` https://pkg.go.dev/k8s.io/kubernetes@v1.23.4/pkg/scheduler/framework/plugins/nodeunschedulable to `Scheduler`.
        ```diff
         type Scheduler struct {
                SchedulingQueue *queue.SchedulingQueue

                client clientset.Interface
        +
        +       filterPlugins []framework.FilterPlugin
         }
        ```
    1. Change the return value of `New()` to `(*Scheduler, error)`

        <details>

        ```diff
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
        ```

        </details>

    1. Add `createFilterPlugins()`.

        ```diff

        ```


1. Update `scheduler/scheduler.go`.

    1. Update `New`'s return.

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

    1. Add `RunFilterPlugins` function.

        For all the `filterPlugins`, `Filter` is called: `status := pl.Filter(ctx, state, pod, nodeInfo)`.

        ```go
        func (sched *Scheduler) RunFilterPlugins(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodes []v1.Node)         ([]*v1.Node, error) {
        	feasibleNodes := make([]*v1.Node, 0, len(nodes))

        	diagnosis := framework.Diagnosis{
        		NodeToStatusMap:      make(framework.NodeToStatusMap),
        		UnschedulablePlugins: sets.NewString(),
        	}

        	// TODO: consider about nominated pod
        	for _, n := range nodes {
        		n := n
        		nodeInfo := framework.NewNodeInfo()
        		nodeInfo.SetNode(&n)

        		status := framework.NewStatus(framework.Success)
        		for _, pl := range sched.filterPlugins {
        			status = pl.Filter(ctx, state, pod, nodeInfo)
        			if !status.IsSuccess() {
        				status.SetFailedPlugin(pl.Name())
        				diagnosis.UnschedulablePlugins.Insert(status.FailedPlugin())
        				break
        			}
        		}
        		if status.IsSuccess() {
        			feasibleNodes = append(feasibleNodes, nodeInfo.Node())
        		}
        	}

        	if len(feasibleNodes) == 0 {
        		return nil, &framework.FitError{
        			Pod:       pod,
        			Diagnosis: diagnosis,
        		}
        	}

        	return feasibleNodes, nil
        }
        ```

    1. Use Filter Plugin in `ScheduleOne`.

        ```go
        // filter
        feasibleNodes, err := sched.RunFilterPlugins(ctx, nil, pod, nodes.Items)
        if err != nil {
            klog.Error(err)
            return
        }

        klog.Info(feasibleNodes)
        // select node randomly
        rand.Seed(time.Now().UnixNano())
        selectedNode := feasibleNodes[rand.Intn(len(feasibleNodes))]
        ```

1. Create node0~node9 with `Spec.Unschedulable: true` and node10 without `Unschedulable` in `sched.go`.

    ```go
	// create node0 ~ node9, all nodes are unschedulable
	for i := 0; i < 10; i++ {
		suffix := strconv.Itoa(i)
		_, err := client.CoreV1().Nodes().Create(ctx, &v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node" + suffix,
			},
			Spec: v1.NodeSpec{
				Unschedulable: true,
			},
		}, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("create node: %w", err)
		}
	}

	// node10 is not unschedulable
	_, err := client.CoreV1().Nodes().Create(ctx, &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node10",
		},
		Spec: v1.NodeSpec{},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("create node: %w", err)
	}
    ```

### 2.2. Run

Conditions:
- Nodes:
    - node0~node9: `Spec.Unschedulable: true`
    - node10: `Unschedulable` is not set
- FilterPlugin: `NodeUnschedulable`
- PodSpec:
    ```go
    v1.PodSpec{
        Containers: []v1.Container{
            {
                Name:  "container1",
                Image: "k8s.gcr.io/pause:3.5",
            },
        },
    }
    ```
- Expect: Pod1 is assigned to `node10`

```
make build run
```

<details>

```
go build -o ./bin/sched ./sched.go
./hack/run.sh
Starting etcd instance
etcd --advertise-client-urls http://127.0.0.1:2379 --data-dir /var/folders/5g/vmdg2t1j2011ggd9p983ns6h0000gn/T/tmp.2GjfR3cX --listen-client-urls http://127.0.0.1:2379 --log-level=debug > "/dev/null" 2>/dev/null
Waiting for etcd to come up.
On try 2, etcd: : {"health":"true","reason":""}
{"header":{"cluster_id":"14841639068965178418","member_id":"10276657743932975437","revision":"2","raft_term":"2"}}etcd started
I0317 10:17:43.830797   36544 instance.go:318] Node port range unspecified. Defaulting to 30000-32767.
I0317 10:17:43.840105   36544 instance.go:274] Using reconciler:
I0317 10:17:43.842257   36544 instance.go:382] Could not construct pre-rendered responses for ServiceAccountIssuerDiscovery endpoints. Endpoints will not be enabled. Error: empty issuer URL
W0317 10:17:44.016404   36544 genericapiserver.go:538] Skipping API authentication.k8s.io/v1beta1 because it has no resources.
W0317 10:17:44.017645   36544 genericapiserver.go:538] Skipping API authorization.k8s.io/v1beta1 because it has no resources.
W0317 10:17:44.026973   36544 genericapiserver.go:538] Skipping API certificates.k8s.io/v1beta1 because it has no resources.
W0317 10:17:44.028739   36544 genericapiserver.go:538] Skipping API coordination.k8s.io/v1beta1 because it has no resources.
W0317 10:17:44.033059   36544 genericapiserver.go:538] Skipping API networking.k8s.io/v1beta1 because it has no resources.
W0317 10:17:44.035674   36544 genericapiserver.go:538] Skipping API node.k8s.io/v1alpha1 because it has no resources.
W0317 10:17:44.043119   36544 genericapiserver.go:538] Skipping API rbac.authorization.k8s.io/v1beta1 because it has no resources.
W0317 10:17:44.043137   36544 genericapiserver.go:538] Skipping API rbac.authorization.k8s.io/v1alpha1 because it has no resources.
W0317 10:17:44.044774   36544 genericapiserver.go:538] Skipping API scheduling.k8s.io/v1beta1 because it has no resources.
W0317 10:17:44.044786   36544 genericapiserver.go:538] Skipping API scheduling.k8s.io/v1alpha1 because it has no resources.
W0317 10:17:44.048866   36544 genericapiserver.go:538] Skipping API storage.k8s.io/v1alpha1 because it has no resources.
W0317 10:17:44.053276   36544 genericapiserver.go:538] Skipping API flowcontrol.apiserver.k8s.io/v1alpha1 because it has no resources.
W0317 10:17:44.057743   36544 genericapiserver.go:538] Skipping API apps/v1beta2 because it has no resources.
W0317 10:17:44.057760   36544 genericapiserver.go:538] Skipping API apps/v1beta1 because it has no resources.
W0317 10:17:44.059937   36544 genericapiserver.go:538] Skipping API admissionregistration.k8s.io/v1beta1 because it has no resources.
I0317 10:17:44.115917   36544 apf_controller.go:317] Starting API Priority and Fairness config controller
I0317 10:17:44.115998   36544 cluster_authentication_trust_controller.go:440] Starting cluster_authentication_trust_controller controller
I0317 10:17:44.116013   36544 shared_informer.go:240] Waiting for caches to sync for cluster_authentication_trust_controller
W0317 10:17:44.117414   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.117566   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.117637   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
E0317 10:17:44.117759   36544 controller.go:155] Found stale data, removed previous endpoints on kubernetes service, apiserver didn't exit successfully previously
W0317 10:17:44.118081   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.118547   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.120549   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.139410   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.197305   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.198227   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
I0317 10:17:44.218988   36544 apf_controller.go:322] Running API Priority and Fairness config worker
I0317 10:17:44.219082   36544 shared_informer.go:247] Caches are synced for cluster_authentication_trust_controller
W0317 10:17:44.219388   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.239087   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.257151   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.257728   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.277684   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.296310   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.296608   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.363461   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.363511   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.364449   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.365892   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.385987   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.386575   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.404998   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.405270   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.406761   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.425290   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.425496   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.446039   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.446137   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.521944   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.522191   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.523417   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.541292   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.541611   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.561222   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.579205   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.579514   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.599403   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.617108   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.617359   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.708993   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.710043   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.711347   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.729663   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.749093   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.749462   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.767433   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.767678   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.787401   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.787951   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.807225   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.807597   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.882206   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.882672   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.884722   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.903163   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.903430   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.923232   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.923256   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.941063   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.941321   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.942566   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.962175   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.962320   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.980638   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:44.980818   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:45.074445   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:45.074683   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:45.075745   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:45.095022   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:45.096083   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:45.115142   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:45.115432   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:45.134619   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:45.136024   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
I0317 10:17:45.136433   36544 storage_scheduling.go:93] created PriorityClass system-node-critical with value 2000001000
W0317 10:17:45.155188   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:45.158312   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
I0317 10:17:45.159122   36544 storage_scheduling.go:93] created PriorityClass system-cluster-critical with value 2000000000
I0317 10:17:45.159143   36544 storage_scheduling.go:109] all system priority classes are created successfully or already exist.
W0317 10:17:45.231141   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:45.232239   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:45.233166   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:45.234416   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:45.235805   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:45.237125   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:45.256329   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:45.257621   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:45.277542   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:45.278477   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:45.298091   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:45.299078   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:45.318286   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:45.319486   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:45.376033   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:45.376884   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:45.397741   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 FlowSchema is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:45.416375   36544 warnings.go:70] flowcontrol.apiserver.k8s.io/v1beta2 PriorityLevelConfiguration is deprecated in v1.26+, unavailable in v1.29+
W0317 10:17:48.833507   36544 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/pods", Verb:"list", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"pods", Subresource:"", Name:"", Parts:[]string{"pods"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"c7b14f53-00a5-4c0d-953c-5a3ebd799a26","resourceVersion":"48","generation":1,"creationTimestamp":"2022-03-17T01:17:44Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T01:17:44Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T01:17:44Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-17T01:17:44Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
W0317 10:17:48.840429   36544 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/pods", Verb:"watch", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"pods", Subresource:"", Name:"", Parts:[]string{"pods"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"c7b14f53-00a5-4c0d-953c-5a3ebd799a26","resourceVersion":"48","generation":1,"creationTimestamp":"2022-03-17T01:17:44Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T01:17:44Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T01:17:44Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-17T01:17:44Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
I0317 10:17:48.854273   36544 alloc.go:329] "allocated clusterIPs" service="default/kubernetes" clusterIPs=map[IPv4:10.0.0.1]
I0317 10:17:48.933295   36544 scheduler.go:26] minischeduler: Try to get pod from queue....
W0317 10:17:48.933836   36544 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"c7b14f53-00a5-4c0d-953c-5a3ebd799a26","resourceVersion":"48","generation":1,"creationTimestamp":"2022-03-17T01:17:44Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T01:17:44Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T01:17:44Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-17T01:17:44Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
W0317 10:17:48.954398   36544 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"c7b14f53-00a5-4c0d-953c-5a3ebd799a26","resourceVersion":"48","generation":1,"creationTimestamp":"2022-03-17T01:17:44Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T01:17:44Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T01:17:44Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-17T01:17:44Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
W0317 10:17:48.973598   36544 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"c7b14f53-00a5-4c0d-953c-5a3ebd799a26","resourceVersion":"48","generation":1,"creationTimestamp":"2022-03-17T01:17:44Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T01:17:44Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T01:17:44Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-17T01:17:44Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
W0317 10:17:48.992265   36544 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"c7b14f53-00a5-4c0d-953c-5a3ebd799a26","resourceVersion":"48","generation":1,"creationTimestamp":"2022-03-17T01:17:44Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T01:17:44Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T01:17:44Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-17T01:17:44Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
W0317 10:17:49.010912   36544 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"c7b14f53-00a5-4c0d-953c-5a3ebd799a26","resourceVersion":"48","generation":1,"creationTimestamp":"2022-03-17T01:17:44Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T01:17:44Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T01:17:44Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-17T01:17:44Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
W0317 10:17:49.030549   36544 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"c7b14f53-00a5-4c0d-953c-5a3ebd799a26","resourceVersion":"48","generation":1,"creationTimestamp":"2022-03-17T01:17:44Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T01:17:44Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T01:17:44Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-17T01:17:44Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
W0317 10:17:49.107142   36544 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"c7b14f53-00a5-4c0d-953c-5a3ebd799a26","resourceVersion":"48","generation":1,"creationTimestamp":"2022-03-17T01:17:44Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T01:17:44Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T01:17:44Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-17T01:17:44Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
W0317 10:17:49.110154   36544 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"c7b14f53-00a5-4c0d-953c-5a3ebd799a26","resourceVersion":"48","generation":1,"creationTimestamp":"2022-03-17T01:17:44Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T01:17:44Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T01:17:44Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-17T01:17:44Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
W0317 10:17:49.128532   36544 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"c7b14f53-00a5-4c0d-953c-5a3ebd799a26","resourceVersion":"48","generation":1,"creationTimestamp":"2022-03-17T01:17:44Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T01:17:44Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T01:17:44Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-17T01:17:44Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
W0317 10:17:49.146556   36544 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"c7b14f53-00a5-4c0d-953c-5a3ebd799a26","resourceVersion":"48","generation":1,"creationTimestamp":"2022-03-17T01:17:44Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T01:17:44Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T01:17:44Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-17T01:17:44Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
W0317 10:17:49.166814   36544 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"c7b14f53-00a5-4c0d-953c-5a3ebd799a26","resourceVersion":"48","generation":1,"creationTimestamp":"2022-03-17T01:17:44Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T01:17:44Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T01:17:44Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-17T01:17:44Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
I0317 10:17:49.185750   36544 sched.go:102] scenario: all nodes created
W0317 10:17:49.186085   36544 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/namespaces/default/pods", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"default", Resource:"pods", Subresource:"", Name:"", Parts:[]string{"pods"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"c7b14f53-00a5-4c0d-953c-5a3ebd799a26","resourceVersion":"48","generation":1,"creationTimestamp":"2022-03-17T01:17:44Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T01:17:44Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T01:17:44Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-17T01:17:44Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
I0317 10:17:49.204444   36544 sched.go:119] scenario: pod1 created
I0317 10:17:49.204480   36544 scheduler.go:28] minischeduler: Start schedule(pod1)
W0317 10:17:49.204734   36544 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/nodes", Verb:"list", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"", Resource:"nodes", Subresource:"", Name:"", Parts:[]string{"nodes"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"c7b14f53-00a5-4c0d-953c-5a3ebd799a26","resourceVersion":"48","generation":1,"creationTimestamp":"2022-03-17T01:17:44Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T01:17:44Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T01:17:44Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-17T01:17:44Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
I0317 10:17:49.263204   36544 scheduler.go:36] minischeduler: Got Nodes successfully
I0317 10:17:49.263232   36544 scheduler.go:37] minischeduler: got nodes: 11
I0317 10:17:49.263270   36544 scheduler.go:45] minischeduler: Filtered Nodes successfully
I0317 10:17:49.263278   36544 scheduler.go:46] minischeduler: feasibleNodes: 1
W0317 10:17:49.263620   36544 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/namespaces/default/pods/pod1/binding", Verb:"create", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"default", Resource:"pods", Subresource:"binding", Name:"pod1", Parts:[]string{"pods", "pod1", "binding"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"c7b14f53-00a5-4c0d-953c-5a3ebd799a26","resourceVersion":"48","generation":1,"creationTimestamp":"2022-03-17T01:17:44Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T01:17:44Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T01:17:44Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-17T01:17:44Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
I0317 10:17:49.265855   36544 scheduler.go:57] minischeduler: Bind Pod successfully
I0317 10:17:49.265886   36544 scheduler.go:26] minischeduler: Try to get pod from queue....
W0317 10:17:53.204884   36544 apf_controller.go:831] no match found for request &request.RequestInfo{IsResourceRequest:true, Path:"/api/v1/namespaces/default/pods/pod1", Verb:"get", APIPrefix:"api", APIGroup:"", APIVersion:"v1", Namespace:"default", Resource:"pods", Subresource:"", Name:"pod1", Parts:[]string{"pods", "pod1"}} and user &user.DefaultInfo{Name:"", UID:"", Groups:[]string(nil), Extra:map[string][]string(nil)}; selecting catchAll={"metadata":{"name":"catch-all","uid":"c7b14f53-00a5-4c0d-953c-5a3ebd799a26","resourceVersion":"48","generation":1,"creationTimestamp":"2022-03-17T01:17:44Z","annotations":{"apf.kubernetes.io/autoupdate-spec":"true"},"managedFields":[{"manager":"api-priority-and-fairness-config-consumer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T01:17:44Z","fieldsType":"FieldsV1","fieldsV1":{"f:status":{"f:conditions":{".":{},"k:{\"type\":\"Dangling\"}":{".":{},"f:lastTransitionTime":{},"f:message":{},"f:reason":{},"f:status":{},"f:type":{}}}}},"subresource":"status"},{"manager":"api-priority-and-fairness-config-producer-v1","operation":"Update","apiVersion":"flowcontrol.apiserver.k8s.io/v1beta2","time":"2022-03-17T01:17:44Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:apf.kubernetes.io/autoupdate-spec":{}}},"f:spec":{"f:distinguisherMethod":{".":{},"f:type":{}},"f:matchingPrecedence":{},"f:priorityLevelConfiguration":{"f:name":{}},"f:rules":{}}}}]},"spec":{"priorityLevelConfiguration":{"name":"catch-all"},"matchingPrecedence":10000,"distinguisherMethod":{"type":"ByUser"},"rules":[{"subjects":[{"kind":"Group","group":{"name":"system:unauthenticated"}},{"kind":"Group","group":{"name":"system:authenticated"}}],"resourceRules":[{"verbs":["*"],"apiGroups":["*"],"resources":["*"],"clusterScope":true,"namespaces":["*"]}],"nonResourceRules":[{"verbs":["*"],"nonResourceURLs":["*"]}]}]},"status":{"conditions":[{"type":"Dangling","status":"False","lastTransitionTime":"2022-03-17T01:17:44Z","reason":"Found","message":"This FlowSchema references the PriorityLevelConfiguration object named \"catch-all\" and it exists"}]}} as fallback flow schema
I0317 10:17:53.212278   36544 sched.go:129] scenario: pod1 is bound to node10
I0317 10:17:53.212302   36544 scheduler.go:78] shutdown scheduler...
I0317 10:17:53.212313   36544 k8sapiserver.go:65] destroying API server
I0317 10:17:53.212334   36544 controller.go:186] Shutting down kubernetes service endpoint reconciler
I0317 10:17:53.238036   36544 apf_controller.go:326] Shutting down API Priority and Fairness config worker
I0317 10:17:53.238043   36544 cluster_authentication_trust_controller.go:463] Shutting down cluster_authentication_trust_controller controller
I0317 10:17:53.238079   36544 storage_flowcontrol.go:150] APF bootstrap ensurer is exiting
I0317 10:17:53.238217   36544 k8sapiserver.go:68] destroyed API server
Cleaning up etcd
Clean up finished
```

</details>

You'll see `pod1 is bound to node10`.

You can also see: all the nodes and feasible nodes after filtering:

```
minischeduler: Got Nodes successfully
minischeduler: got nodes: 11
minischeduler: Filtered Nodes successfully
minischeduler: feasibleNodes: 1
```
