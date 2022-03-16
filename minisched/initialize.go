package minisched

import (
	"fmt"

	"github.com/nakamasato/mini-kube-scheduler/minisched/plugins/score/nodenumber"
	"github.com/nakamasato/mini-kube-scheduler/minisched/queue"
	"k8s.io/client-go/informers"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/nodeunschedulable"
)

type Scheduler struct {
	SchedulingQueue *queue.SchedulingQueue

	client clientset.Interface

	filterPlugins []framework.FilterPlugin

	scorePlugins []framework.ScorePlugin
}

func New(
	client clientset.Interface,
	informerFactory informers.SharedInformerFactory,
) (*Scheduler, error) {
	// filter plugin
	filterP, err := createFilterPlugins()
	if err != nil {
		return nil, fmt.Errorf("create filter plugins: %w", err)
	}

	// score plugin
	scoreP, err := createScorePlugins()
	if err != nil {
		return nil, fmt.Errorf("create score plugins: %w", err)
	}

	sched := &Scheduler{
		SchedulingQueue: queue.New(),
		client:          client,
		filterPlugins:   filterP,
		scorePlugins:    scoreP,
	}

	addAllEventHandlers(sched, informerFactory)

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

func createScorePlugins() ([]framework.ScorePlugin, error) {
	// nodenumber is FilterPlugin.
	nodenumberplugin, err := createNodeNumberPlugin()
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

func createNodeNumberPlugin() (framework.Plugin, error) {
	if nodenumberplugin != nil {
		return nodenumberplugin, nil
	}

	p, err := nodenumber.New(nil, nil)
	nodenumberplugin = p

	return p, err
}
