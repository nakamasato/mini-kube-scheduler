package scheduler

import (
	"context"

	"github.com/nakamasato/mini-kube-scheduler/minisched"

	"golang.org/x/xerrors"
	clientset "k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/events"
	"k8s.io/klog"
	v1beta2config "k8s.io/kube-scheduler/config/v1beta2"
	"k8s.io/kubernetes/pkg/scheduler"

)

// Service manages scheduler.
type Service struct {
	// function to shutdown scheduler.
	shutdownfn func()

	clientset           clientset.Interface
	restclientCfg       *restclient.Config
	currentSchedulerCfg *v1beta2config.KubeSchedulerConfiguration
}

// NewSchedulerService starts scheduler and return *Service.
func NewSchedulerService(client clientset.Interface, restclientCfg *restclient.Config) *Service {
	return &Service{clientset: client, restclientCfg: restclientCfg}
}

func (s *Service) RestartScheduler(cfg *v1beta2config.KubeSchedulerConfiguration) error {
	s.ShutdownScheduler()

	if err := s.StartScheduler(cfg); err != nil {
		return xerrors.Errorf("start scheduler: %w", err)
	}
	return nil
}

// StartScheduler starts scheduler.
func (s *Service) StartScheduler(versionedcfg *v1beta2config.KubeSchedulerConfiguration) error {
	clientSet := s.clientset
	ctx, cancel := context.WithCancel(context.Background())

	informerFactory := scheduler.NewInformerFactory(clientSet, 0)
	evtBroadcaster := events.NewBroadcaster(&events.EventSinkImpl{
		Interface: clientSet.EventsV1(),
	})

	evtBroadcaster.StartRecordingToSink(ctx.Done())

	s.currentSchedulerCfg = versionedcfg.DeepCopy()

	sched := minisched.New(
		clientSet,
		informerFactory,
	)

	informerFactory.Start(ctx.Done())
	informerFactory.WaitForCacheSync(ctx.Done())

	go sched.Run(ctx)

	s.shutdownfn = cancel

	return nil
}

func (s *Service) ShutdownScheduler() {
	if s.shutdownfn != nil {
		klog.Info("shutdown scheduler...")
		s.shutdownfn()
	}
}

func (s *Service) GetSchedulerConfig() *v1beta2config.KubeSchedulerConfiguration {
	return s.currentSchedulerCfg
}
