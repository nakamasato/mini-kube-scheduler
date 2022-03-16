package minisched

import (
	"context"
	"math/rand"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"k8s.io/apimachinery/pkg/util/wait"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

func (sched *Scheduler) Run(ctx context.Context) {
	wait.UntilWithContext(ctx, sched.scheduleOne, 0)
}

func (sched *Scheduler) scheduleOne(ctx context.Context) {
	klog.Info("minischeduler: Try to get pod from queue....")
	pod := sched.SchedulingQueue.NextPod()
	klog.Info("minischeduler: Start schedule(" + pod.Name + ")")

	// get nodes
	nodes, err := sched.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		klog.Error(err)
		return
	}
	klog.Info("minischeduler: Got Nodes successfully")

	// filter
	feasibleNodes, status := sched.RunFilterPlugins(ctx, nil, pod, nodes.Items)
	if !status.IsSuccess() {
		klog.Error(status.AsError())
		return
	}
	if len(feasibleNodes) == 0 {
		klog.Info("no fasible nodes for " + pod.Name)
		return
	}

	// select node randomly
	selectedNode := feasibleNodes[rand.Intn(len(nodes.Items))]

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

func (sched *Scheduler) RunFilterPlugins(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodes []v1.Node) ([]*v1.Node, *framework.Status) {
	feasibleNodes := make([]*v1.Node, len(nodes))

	// TODO: consider about nominated pod
	statuses := make(framework.PluginToStatus)
	for _, n := range nodes {
		nodeInfo := framework.NewNodeInfo()
		nodeInfo.SetNode(&n)
		for _, pl := range sched.filterPlugins {
			status := pl.Filter(ctx, state, pod, nodeInfo)
			if !status.IsSuccess() {
				status.SetFailedPlugin(pl.Name())
				statuses[pl.Name()] = status
				return nil, statuses.Merge()
			}
			feasibleNodes = append(feasibleNodes, nodeInfo.Node())
		}
	}

	return feasibleNodes, statuses.Merge()
}
