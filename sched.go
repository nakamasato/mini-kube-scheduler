package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/nakamasato/mini-kube-scheduler/scheduler"
	"golang.org/x/xerrors"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/klog"

	"github.com/nakamasato/mini-kube-scheduler/k8sapiserver"
	"github.com/nakamasato/mini-kube-scheduler/scheduler/defaultconfig"
)

var ErrEmptyEnv = errors.New("env is needed, but empty")

// entry point.
func main() {
	if err := start(); err != nil {
		klog.Fatalf("failed with error on running scheduler: %+v", err)
	}
}

// start starts scheduler and needed k8s components.
func start() error {
	etcdurl := os.Getenv("KUBE_SCHEDULER_SIMULATOR_ETCD_URL")
	if etcdurl == "" {
		return xerrors.Errorf("get KUBE_SCHEDULER_SIMULATOR_ETCD_URL from env: %w", ErrEmptyEnv)
	}

	restclientCfg, apiShutdown, err := k8sapiserver.StartAPIServer(etcdurl)
	if err != nil {
		return xerrors.Errorf("start API server: %w", err)
	}
	defer apiShutdown()

	client := clientset.NewForConfigOrDie(restclientCfg)

	// pvshutdown, err := pvcontroller.StartPersistentVolumeController(client)
	// if err != nil {
	// 	return xerrors.Errorf("start pv controller: %w", err)
	// }
	// defer pvshutdown()

	sched := scheduler.NewSchedulerService(client, restclientCfg)

	sc, err := defaultconfig.DefaultSchedulerConfig()
	if err != nil {
		return xerrors.Errorf("create scheduler config")
	}

	if err := sched.StartScheduler(sc); err != nil {
		return xerrors.Errorf("start scheduler: %w", err)
	}
	defer sched.ShutdownScheduler()

	err = scenario(client)
	if err != nil {
		return xerrors.Errorf("start scenario: %w", err)
	}

	return nil
}

func scenario(client clientset.Interface) error {
	ctx := context.Background()

	// create node0 ~ node5, all nodes are unschedulable
	for i := 0; i < 5; i++ {
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

	// create node5 ~ node9, without unschedulable
	for i := 5; i < 10; i++ {
		suffix := strconv.Itoa(i)
		_, err := client.CoreV1().Nodes().Create(ctx, &v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node" + suffix,
			},
			Spec: v1.NodeSpec{},
		}, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("create node: %w", err)
		}
	}

	klog.Info("scenario: all nodes created")

	_, err := client.CoreV1().Pods("default").Create(ctx, &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod1"},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:  "container1",
					Image: "k8s.gcr.io/pause:3.5",
				},
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("create pod: %w", err)
	}

	klog.Info("scenario: pod1 created")

	_, err = client.CoreV1().Pods("default").Create(ctx, &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod8"},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:  "container1",
					Image: "k8s.gcr.io/pause:3.5",
				},
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("create pod: %w", err)
	}

	klog.Info("scenario: pod1 created")

	// wait to schedule
	time.Sleep(10 * time.Second)

	pod, err := client.CoreV1().Pods("default").Get(ctx, "pod1", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get pod: %w", err)
	}

	klog.Info("scenario: pod1 is bound to " + pod.Spec.NodeName)

	pod, err = client.CoreV1().Pods("default").Get(ctx, "pod8", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get pod: %w", err)
	}

	klog.Info("scenario: pod8 is bound to " + pod.Spec.NodeName)

	return nil
}
