package minisched

import (
	"fmt"

	"github.com/nakamasato/mini-kube-scheduler/minisched/plugins/score/nodenumber"
	"github.com/nakamasato/mini-kube-scheduler/minisched/queue"
	"github.com/nakamasato/mini-kube-scheduler/minisched/waitingpod"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/informers"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/nodeunschedulable"
)

type Scheduler struct {
	SchedulingQueue *queue.SchedulingQueue

	client clientset.Interface

	waitingPods map[types.UID]*waitingpod.WaitingPod

	filterPlugins   []framework.FilterPlugin
	preScorePlugins []framework.PreScorePlugin
	scorePlugins    []framework.ScorePlugin
	permitPlugins   []framework.PermitPlugin
}

func New(
	client clientset.Interface,
	informerFactory informers.SharedInformerFactory,
) (*Scheduler, error) {
	sched := &Scheduler{
		client:      client,
		waitingPods: map[types.UID]*waitingpod.WaitingPod{},
	}

	// filter plugin
	filterP, err := createFilterPlugins()
	if err != nil {
		return nil, fmt.Errorf("create filter plugins: %w", err)
	}
	sched.filterPlugins = filterP

	// prescore plugin
	preScoreP, err := createPreScorePlugins(sched)
	if err != nil {
		return nil, fmt.Errorf("create pre score plugins: %w", err)
	}
	sched.preScorePlugins = preScoreP

	// score plugin
	scoreP, err := createScorePlugins(sched)
	if err != nil {
		return nil, fmt.Errorf("create score plugins: %w", err)
	}
	sched.scorePlugins = scoreP

	// permit plugin
	permitP, err := createPermitPlugins(sched)
	if err != nil {
		return nil, fmt.Errorf("create permit plugins: %w", err)
	}
	sched.permitPlugins = permitP

	events, err := eventsToRegister(sched)
	if err != nil {
		return nil, fmt.Errorf("create gvks: %w", err)
	}

	sched.SchedulingQueue = queue.New(events)

	addAllEventHandlers(sched, informerFactory, unionedGVKs(events))

	return sched, nil
}

func createFilterPlugins() ([]framework.FilterPlugin, error) {
	// nodeunschedulable is FilterPlugin.
	nodeunschedulableplugin, err := createNodeUnschedulablePlugin()
	if err != nil {
		return nil, fmt.Errorf("create nodeunschedulable plugin: %w", err)
	}

	// We use nodeunschedulable plugin only.
	filterPlugins := []framework.FilterPlugin{
		nodeunschedulableplugin.(framework.FilterPlugin),
	}

	return filterPlugins, nil
}

var (
	nodeunschedulableplugin framework.Plugin
)

func createNodeUnschedulablePlugin() (framework.Plugin, error) {
	if nodeunschedulableplugin != nil {
		return nodeunschedulableplugin, nil
	}

	p, err := nodeunschedulable.New(nil, nil)
	nodeunschedulableplugin = p
	return p, err
}

func createPreScorePlugins(h waitingpod.Handle) ([]framework.PreScorePlugin, error) {
	nodenumberplugin, err := createNodeNumberPlugin(h)
	if err != nil {
		return nil, fmt.Errorf("create nodenumber plugin: %w", err)
	}

	// We use nodenumber plugin only.
	preScorePlugins := []framework.PreScorePlugin{
		nodenumberplugin.(framework.PreScorePlugin),
	}

	return preScorePlugins, nil
}

func createScorePlugins(h waitingpod.Handle) ([]framework.ScorePlugin, error) {
	nodenumberplugin, err := createNodeNumberPlugin(h)
	if err != nil {
		return nil, fmt.Errorf("create nodenumber plugin: %w", err)
	}

	// We use nodenumber plugin only.
	scorePlugins := []framework.ScorePlugin{
		nodenumberplugin.(framework.ScorePlugin),
	}

	return scorePlugins, nil
}

var (
	nodenumberplugin framework.Plugin
)

func createNodeNumberPlugin(h waitingpod.Handle) (framework.Plugin, error) {
	if nodenumberplugin != nil {
		return nodenumberplugin, nil
	}

	p, err := nodenumber.New(nil, h)
	nodenumberplugin = p

	return p, err
}

func createPermitPlugins(h waitingpod.Handle) ([]framework.PermitPlugin, error) {
	// nodenumber is PermitPlugin.
	nodenumberplugin, err := createNodeNumberPlugin(h)
	if err != nil {
		return nil, fmt.Errorf("create nodenumber plugin: %w", err)
	}

	// We use nodenumber plugin only.
	permitPlugins := []framework.PermitPlugin{
		nodenumberplugin.(framework.PermitPlugin),
	}

	return permitPlugins, nil
}

func eventsToRegister(h waitingpod.Handle) (map[framework.ClusterEvent]sets.String, error) {
	unschedulablePlugin, err := createNodeUnschedulablePlugin()
	if err != nil {
		return nil, fmt.Errorf("create node unschedulable plugin: %w", err)
	}
	nnumberPlugin, err := createNodeNumberPlugin(h)
	if err != nil {
		return nil, fmt.Errorf("create node number plugin: %w", err)
	}

	clusterEventMap := make(map[framework.ClusterEvent]sets.String)
	nunschedulablePluginEvents := unschedulablePlugin.(framework.EnqueueExtensions).EventsToRegister()
	registerClusterEvents(unschedulablePlugin.Name(), clusterEventMap, nunschedulablePluginEvents)
	nnumberPluginEvents := nnumberPlugin.(framework.EnqueueExtensions).EventsToRegister()
	registerClusterEvents(unschedulablePlugin.Name(), clusterEventMap, nnumberPluginEvents)

	return clusterEventMap, nil
}

func registerClusterEvents(name string, eventToPlugins map[framework.ClusterEvent]sets.String, evts []framework.ClusterEvent) {
	for _, evt := range evts {
		if eventToPlugins[evt] == nil {
			eventToPlugins[evt] = sets.NewString(name)
		} else {
			eventToPlugins[evt].Insert(name)
		}
	}
}

func unionedGVKs(m map[framework.ClusterEvent]sets.String) map[framework.GVK]framework.ActionType {
	gvkMap := make(map[framework.GVK]framework.ActionType)
	for evt := range m {
		if _, ok := gvkMap[evt.Resource]; ok {
			gvkMap[evt.Resource] |= evt.ActionType
		} else {
			gvkMap[evt.Resource] = evt.ActionType
		}
	}
	return gvkMap
}
