package nodenumber

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/nakamasato/mini-kube-scheduler/minisched/waitingpod"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

// NodeNumber is a score plugin that returns 10 if the last char of the target Pod's and Node's name is the same integer, otherwise returns 0. <- PreScore and Score Plugin
// And it will delay the binding of pod by {node suffix number} seconds. <- PermitPlugin
// IMPORTANT NOTE: this plugin only handle single digit numbers only.
type NodeNumber struct {
	h waitingpod.Handle
}

var _ framework.ScorePlugin = &NodeNumber{}
var _ framework.PreScorePlugin = &NodeNumber{}
var _ framework.PermitPlugin = &NodeNumber{}

// Name is the name of the plugin used in the plugin registry and configurations.
const Name = "NodeNumber"
const preScoreStateKey = "PreScore" + Name

// Name returns name of the plugin. It is used in logs, etc.
func (pl *NodeNumber) Name() string {
	return Name
}

// preScoreState computed at PreScore and used at Score.
type preScoreState struct {
	podSuffixNumber int
}

// Clone implements the mandatory Clone interface. We don't really copy the data since
// there is no need for that.
func (s *preScoreState) Clone() framework.StateData {
	return s
}

// PreScore gets the last character of the given Pod and store it into CycleState with a key named `podSuffixNumber`
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
	// Write data to CycleState
	state.Write(preScoreStateKey, s)

	return nil
}

// Score reads the podSuffixNumber from the CycleState, gets the last character of the nodeName,
// and returns 10 if the suffix numbers are same,
// otherwise return 0.
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

// Permit delays binding by calling wp.Allow after waiting the number of seconds
func (pl *NodeNumber) Permit(ctx context.Context, state *framework.CycleState, p *v1.Pod, nodeName string) (*framework.Status, time.Duration) {
	nodeNameLastChar := nodeName[len(nodeName)-1:]

	nodenum, err := strconv.Atoi(nodeNameLastChar)
	if err != nil {
		// return allow(success) even if its suffix is non-number.
		return nil, 0
	}

	// allow pod after {nodenum} seconds
	time.AfterFunc(time.Duration(nodenum)*time.Second, func() {
		wp := pl.h.GetWaitingPod(p.GetUID())
		wp.Allow(pl.Name())
	})

	timeout := time.Duration(10) * time.Second
	return framework.NewStatus(framework.Wait, ""), timeout
}

// ScoreExtensions of the Score plugin.
func (pl *NodeNumber) ScoreExtensions() framework.ScoreExtensions {
	return nil
}

// New initializes a new plugin and returns it.
func New(_ runtime.Object, h waitingpod.Handle) (framework.Plugin, error) {
	return &NodeNumber{h: h}, nil
}

// To implement EnqueueExtensions
func (pl *NodeNumber) EventsToRegister() []framework.ClusterEvent {
	return []framework.ClusterEvent{
		{Resource: framework.Node, ActionType: framework.Add},
	}
}
