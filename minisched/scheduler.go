package minisched

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
		return
	}

	klog.Info("minischeduler: ran filter plugins successfully")
	klog.Info("minischeduler: feasible nodes: ", len(feasibleNodes))

	// score
	score, status := sched.RunScorePlugins(ctx, nil, pod, feasibleNodes)
	if !status.IsSuccess() {
		klog.Error(status.AsError())
		return
	}

	klog.Info("minischeduler: ran score plugins successfully")
	klog.Info("minischeduler: score results", score)

	// select node by score
	nodeName, err := sched.selectNode(score)
	if err != nil {
		klog.Error(err)
		return
	}

	if err := sched.Bind(ctx, pod, nodeName); err != nil {
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
