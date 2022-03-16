package minisched

import (
	"fmt"

	"github.com/nakamasato/mini-kube-scheduler/minisched/queue"
	"k8s.io/client-go/informers"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/nodename"
)

type Scheduler struct {
	SchedulingQueue *queue.SchedulingQueue

	client clientset.Interface

	filterPlugins []framework.FilterPlugin
}

func New(
	client clientset.Interface,
	informerFactory informers.SharedInformerFactory,
) (*Scheduler, error) {
	filterP, err := createFilterPlugins()
	if err != nil {
		return nil, fmt.Errorf("create filter plugins: %w", err)
	}
	sched := &Scheduler{
		SchedulingQueue: queue.New(),
		client:          client,
		filterPlugins:   filterP,
	}

	addAllEventHandlers(sched, informerFactory)

	return sched, nil
}

func createFilterPlugins() ([]framework.FilterPlugin, error) {
	// nodename is FilterPlugin.
	nodenameplugin, err := nodename.New(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create nodename plugin: %w", err)
	}

	// We use nodename plugin only.
	filterPlugins := []framework.FilterPlugin{
		nodenameplugin.(framework.FilterPlugin),
	}

	return filterPlugins, nil
}
