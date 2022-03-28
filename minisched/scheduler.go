package minisched

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/nakamasato/mini-kube-scheduler/minisched/waitingpod"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"k8s.io/apimachinery/pkg/util/sets"
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

	state := framework.NewCycleState()

	// get nodes
	nodes, err := sched.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		klog.Error(err)
		return
	}
	klog.Info("minischeduler: Got Nodes successfully")
	klog.Info("minischeduler: got nodes: ", len(nodes.Items))

	// filter
	feasibleNodes, err := sched.RunFilterPlugins(ctx, nil, pod, nodes.Items)
	if err != nil {
		klog.Error(err)
		sched.ErrorFunc(pod, err)
		return
	}

	klog.Info("minischeduler: ran filter plugins successfully")
	klog.Info("minischeduler: feasible nodes: ", len(feasibleNodes))

	// pre score
	status := sched.RunPreScorePlugins(ctx, state, pod, feasibleNodes)
	if !status.IsSuccess() {
		klog.Error(status.AsError())
		sched.ErrorFunc(pod, err)
		return
	}
	klog.Info("minischeduler: ran pre score plugins successfully")

	// score
	score, status := sched.RunScorePlugins(ctx, state, pod, feasibleNodes)
	if !status.IsSuccess() {
		klog.Error(status.AsError())
		sched.ErrorFunc(pod, err)
		return
	}

	klog.Info("minischeduler: ran score plugins successfully")
	klog.Info("minischeduler: score results", score)

	// select node by score
	nodeName, err := sched.selectNode(score)
	if err != nil {
		klog.Error(err)
		sched.ErrorFunc(pod, err)
		return
	}

	status = sched.RunPermitPlugins(ctx, state, pod, nodeName)
	if status.Code() != framework.Wait && !status.IsSuccess() {
		klog.Error(status.AsError())
		sched.ErrorFunc(pod, err)
		return
	}

	go func() {
		ctx := ctx

		status := sched.WaitOnPermit(ctx, pod)
		if !status.IsSuccess() {
			klog.Error(status.AsError())
			sched.ErrorFunc(pod, err)
			return
		}

		if err := sched.Bind(ctx, pod, nodeName); err != nil {
			klog.Error(err)
			sched.ErrorFunc(pod, err)
			return
		}
		klog.Info("minischeduler: Bind Pod successfully")
	}()
}

// WaitOnPermit will block, if the pod is a waiting pod, until the waiting pod is rejected or allowed.
func (sched *Scheduler) WaitOnPermit(ctx context.Context, pod *v1.Pod) *framework.Status {
	waitingPod := sched.waitingPods[pod.UID]
	if waitingPod == nil {
		return nil
	}
	defer delete(sched.waitingPods, pod.UID)

	klog.Info("minischeduler: Pod waiting on permit. pod: ", klog.KObj(pod))

	s := waitingPod.GetSignal()
	klog.Info("minischeduler: Successfully got signal from waitingPod. pod: ", klog.KObj(pod))

	if !s.IsSuccess() {
		if s.IsUnschedulable() {
			klog.Info("minischeduler: Pod rejected while waiting on permit. pod:", klog.KObj(pod), ", status: ", s.Message())

			s.SetFailedPlugin(s.FailedPlugin())
			return s
		}

		err := s.AsError()
		klog.ErrorS(err, "minischeduler: Failed waiting on permit for pod", "pod", klog.KObj(pod))
		return framework.AsStatus(fmt.Errorf("minischeduler: waiting on permit for pod: %w", err)).WithFailedPlugin(s.FailedPlugin())
	}
	return nil
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

func (sched *Scheduler) RunFilterPlugins(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodes []v1.Node) ([]*v1.Node, error) {
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

func (sched *Scheduler) RunPreScorePlugins(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodes []*v1.Node) *framework.Status {
	for _, pl := range sched.preScorePlugins {
		status := pl.PreScore(ctx, state, pod, nodes)
		if !status.IsSuccess() {
			return status
		}
	}

	return nil
}

func (sched *Scheduler) RunScorePlugins(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodes []*v1.Node) (framework.NodeScoreList, *framework.Status) {
	scoresMap := sched.createPluginToNodeScores(nodes)

	for index, n := range nodes {
		for _, pl := range sched.scorePlugins {
			score, status := pl.Score(ctx, state, pod, n.Name)
			klog.Infof("ScorePlugin: %s, pod: %s, node: %s, score: %d", pl.Name(), pod.Name, n.Name, score)
			if !status.IsSuccess() {
				return nil, status
			}
			scoresMap[pl.Name()][index] = framework.NodeScore{
				Name:  n.Name,
				Score: score,
			}
		}
	}

	// TODO: plugin weight & normalizeScore

	result := make(framework.NodeScoreList, 0, len(nodes))
	for i := range nodes {
		result = append(result, framework.NodeScore{Name: nodes[i].Name, Score: 0})
		for j := range scoresMap {
			result[i].Score += scoresMap[j][i].Score
		}
	}

	return result, nil
}

func (sched *Scheduler) RunPermitPlugins(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeName string) (status *framework.Status) {
	pluginsWaitTime := make(map[string]time.Duration)
	statusCode := framework.Success
	for _, pl := range sched.permitPlugins {
		status, timeout := pl.Permit(ctx, state, pod, nodeName)
		if !status.IsSuccess() {
			// reject
			if status.IsUnschedulable() {
				klog.InfoS("PermitPlugins: Pod rejected by permit plugin", "pod", klog.KObj(pod), "plugin", pl.Name(), "status", status.Message())
				status.SetFailedPlugin(pl.Name())
				return status
			}

			// wait
			if status.Code() == framework.Wait {
				pluginsWaitTime[pl.Name()] = timeout
				statusCode = framework.Wait
				continue
			}

			// other errors
			err := status.AsError()
			klog.ErrorS(err, "PermitPlugins: Failed running Permit plugin", "plugin", pl.Name(), "pod", klog.KObj(pod))
			return framework.AsStatus(fmt.Errorf("running Permit plugin %q: %w", pl.Name(), err)).WithFailedPlugin(pl.Name())
		}
	}

	if statusCode == framework.Wait {
		waitingPod := waitingpod.NewWaitingPod(pod, pluginsWaitTime)
		sched.waitingPods[pod.UID] = waitingPod
		msg := fmt.Sprintf("PermitPlugins: one or more plugins asked to wait and no plugin rejected pod %q", pod.Name)
		klog.Info("PermitPlugins: One or more plugins asked to wait and no plugin rejected pod. pod: ", klog.KObj(pod))
		return framework.NewStatus(framework.Wait, msg)
	}

	return nil
}

// Select the Node with highest score from NodeScoreList and return the node name
func (sched *Scheduler) selectNode(nodeScoreList framework.NodeScoreList) (string, error) {
	if len(nodeScoreList) == 0 {
		return "", fmt.Errorf("empty priorityList")
	}
	maxScore := nodeScoreList[0].Score
	selectedNodeName := nodeScoreList[0].Name
	cntOfMaxScore := 1
	for _, ns := range nodeScoreList[1:] {
		if ns.Score > maxScore {
			maxScore = ns.Score
			selectedNodeName = ns.Name
			cntOfMaxScore = 1
		} else if ns.Score == maxScore {
			cntOfMaxScore++
			if rand.Intn(cntOfMaxScore) == 0 {
				// Replace the candidate with probability of 1/cntOfMaxScore
				selectedNodeName = ns.Name
			}
		}
	}
	return selectedNodeName, nil
}

// Initialize a PluginToNodeScores (map of NodeScoreList for each node) for each scorePlugins.
// PluginToNodeScore(pluginName -> NodeScoreList(each element for each node))
func (sched *Scheduler) createPluginToNodeScores(nodes []*v1.Node) framework.PluginToNodeScores {
	pluginToNodeScores := make(framework.PluginToNodeScores, len(sched.scorePlugins))
	for _, pl := range sched.scorePlugins {
		pluginToNodeScores[pl.Name()] = make(framework.NodeScoreList, len(nodes))
	}

	return pluginToNodeScores
}

func (sched *Scheduler) GetWaitingPod(uid types.UID) *waitingpod.WaitingPod {
	return sched.waitingPods[uid]
}

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
