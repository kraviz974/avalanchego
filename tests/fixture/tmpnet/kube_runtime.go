// Copyright (C) 2019-2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package tmpnet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"slices"
	"strings"
	"time"

	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/ava-labs/avalanchego/config"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	restclient "k8s.io/client-go/rest"
)

// TODO(marun) need an easy way to cleanup stale nodes (maybe this suggests something cli-based)

const (
	containerName   = "avago"
	volumeName      = "data"
	volumeMountPath = "/data"

	statusCheckInterval = 500 * time.Millisecond
	// TODO(marun) Need to make this configurable
	// - EBS volume sizes are in GiB.
	// - A node will report unhealthy if less than 1GiB is available.
	// - On the local storage provider configured by kind, the volume
	// size doesn't matter size the volumes are just paths on the host
	// filesystem.
	volumeSize = "2Gi"
)

type KubeRuntime struct {
	node *Node
}

func (p *KubeRuntime) setNotRunning() {
	p.node.URI = ""
	p.node.StakingAddress = netip.AddrPort{}
}

// TODO(marun) Factor out common elements from node process and node pod
// On restart, readState is always called
// TODO(marun) When the node is not found to be running, clear the node URI?
func (p *KubeRuntime) readState(ctx context.Context) error {
	clientset, err := p.getClientset()
	if err != nil {
		return err
	}

	statefulSetName := p.getStatefulSetName()
	namespace := p.runtimeConfig().Namespace

	// Check if the statefulset exists
	scale, err := clientset.AppsV1().StatefulSets(namespace).GetScale(ctx, statefulSetName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		p.node.network.log.Info("statefulset not found")
		p.setNotRunning()
		return nil
	}
	if err != nil {
		return err
	}

	// Wait for the statefulset to have replicas?
	if scale.Spec.Replicas == 0 {
		p.setNotRunning()
		return nil
	}

	podName := statefulSetName + "-0"

	// Wait for the pod to become ready (otherwise it won't be accepting network connections)
	if err := WaitForPodCondition(ctx, clientset, namespace, podName, corev1.PodReady); err != nil {
		return err
	}

	pod, err := clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	addr, err := netip.ParseAddr(pod.Status.PodIP)
	if err != nil {
		return fmt.Errorf("failed to parse pod IP: %w", err)
	}

	// Assume default ports. No reason to vary when pods don't share port space.
	p.node.URI = fmt.Sprintf("http://%s:%d", pod.Status.PodIP, config.DefaultHTTPPort)
	p.node.StakingAddress = netip.AddrPortFrom(addr, config.DefaultStakingPort)

	return nil
}

// TODO(marun) Maybe better with a timestamp used as input to generateName and include uuid and node id as labels?
func (p *KubeRuntime) getStatefulSetName() string {
	nodeIDString := p.node.NodeID.String()
	unwantedNodeIDPrefix := "NodeID-"
	startIndex := len(unwantedNodeIDPrefix)
	endIndex := len(unwantedNodeIDPrefix) + 8
	return p.node.network.UUID + "-" + strings.ToLower(nodeIDString[startIndex:endIndex])
}

func (p *KubeRuntime) getFlagsForPod() (FlagsMap, error) {
	flags, err := p.node.composeFlags()
	if err != nil {
		return nil, err
	}
	// The data dir path is fixed for the pod
	flags[config.DataDirKey] = volumeMountPath
	// The node must bind to the pod IP to enable the kubelet to access the http port for the readiness check
	flags[config.HTTPHostKey] = "0.0.0.0"
	return flags, nil
}

// Start the node as a kubernetes statefulset.
func (p *KubeRuntime) Start(ctx context.Context) error {
	// TODO(marun) Handle the case where the target namespace doesn't exist

	var (
		log             = p.node.network.log
		runtimeConfig   = p.runtimeConfig()
		namespace       = runtimeConfig.Namespace
		statefulSetName = p.getStatefulSetName()
	)

	clientset, err := p.getClientset()
	if err != nil {
		return err
	}

	exists := true
	_, err = clientset.AppsV1().StatefulSets(namespace).Get(ctx, statefulSetName, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to retrieve statefulset %s/%s: %w", namespace, statefulSetName, err)
		}
		exists = false
	}

	if exists {
		scale, err := clientset.AppsV1().StatefulSets(namespace).GetScale(ctx, statefulSetName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to retrieve scale for statefulset %s/%s: %w", namespace, statefulSetName, err)
		}

		if scale.Spec.Replicas != 0 {
			log.Info("node is already running",
				zap.Stringer("nodeID", p.node.NodeID),
			)
			return nil
		}

		scale.Spec.Replicas = 1
		_, err = clientset.AppsV1().StatefulSets(runtimeConfig.Namespace).UpdateScale(
			ctx,
			statefulSetName,
			scale,
			metav1.UpdateOptions{},
		)
		if err != nil {
			return fmt.Errorf("failed to scale up statefulset for %s: %w", p.node.NodeID.String(), err)
		}
		return nil
	}

	// Statefulset needs to be created

	flags, err := p.getFlagsForPod()
	if err != nil {
		return err
	}

	// Create a statefulset for the pod and wait for it to become ready
	statefulSet := NewNodeStatefulSet(
		p.getStatefulSetName(),
		false, // generateName
		runtimeConfig.Image,
		containerName,
		volumeName,
		volumeSize,
		volumeMountPath,
		p.node.getLabels(),
		flags,
	)

	createdStatefulSet, err := clientset.AppsV1().StatefulSets(runtimeConfig.Namespace).Create(
		ctx,
		statefulSet,
		metav1.CreateOptions{},
	)
	if err != nil {
		return fmt.Errorf("failed to create statefulset: %w", err)
	}
	log.Debug("created statefulset",
		zap.String("namespace", runtimeConfig.Namespace),
		zap.String("name", createdStatefulSet.Name),
	)

	bootstrapIPs, _ := p.node.network.GetBootstrapIPsAndIDs(nil)
	if len(bootstrapIPs) > 0 {
		return nil
	}

	log.Info("waiting for node pod to start running so that subsequent nodes will have a bootstrap target",
		zap.String("nodeID", p.node.NodeID.String()),
	)

	return wait.PollImmediateInfiniteWithContext(ctx, statusCheckInterval, func(_ context.Context) (bool, error) {
		err := p.checkRunning(ctx)
		if err != nil {
			log.Debug("failed to check if node is running",
				zap.String("nodeID", p.node.NodeID.String()),
				zap.Error(err),
			)
		}
		return err == nil, nil
	})
}

// Stop the pod by setting the replicas to zero on the statefulset.
func (p *KubeRuntime) InitiateStop(ctx context.Context) error {
	clientset, err := p.getClientset()
	if err != nil {
		return err
	}
	statefulSetName := p.getStatefulSetName()
	scale, err := clientset.AppsV1().StatefulSets(p.runtimeConfig().Namespace).GetScale(ctx, statefulSetName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if scale.Spec.Replicas == 0 {
		p.setNotRunning()
		return nil
	}
	scale.Spec.Replicas = 0
	_, err = clientset.AppsV1().StatefulSets(p.runtimeConfig().Namespace).UpdateScale(
		ctx,
		statefulSetName,
		scale,
		metav1.UpdateOptions{},
	)
	return err
}

// Waits for the node process to stop.
// TODO(marun) Consider using a watch instead
func (p *KubeRuntime) WaitForStopped(ctx context.Context) error {
	clientset, err := p.getClientset()
	if err != nil {
		return err
	}
	statefulSetName := p.getStatefulSetName()
	namespace := p.runtimeConfig().Namespace

	ticker := time.NewTicker(defaultNodeTickerInterval)
	defer ticker.Stop()
	for {
		scale, err := clientset.AppsV1().StatefulSets(namespace).GetScale(ctx, statefulSetName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			p.setNotRunning()
			return nil
		}
		if err != nil {
			return fmt.Errorf("failed to retrieve scale of statefulset: %w", err)
		}
		if scale.Status.Replicas == 0 {
			p.setNotRunning()
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("failed to see statefulset for node %q scale down before timeout: %w", p.node.NodeID, ctx.Err())
		case <-ticker.C:
		}
	}
}

// Restarts the node
func (p *KubeRuntime) Restart(ctx context.Context) error {
	log := p.node.network.log

	// Save node to disk
	if err := p.node.Write(); err != nil {
		return err
	}

	// Retrieve the statefulset
	statefulset, err := p.getStatefulSet(ctx)
	if err != nil {
		return err
	}

	patches := []map[string]any{}

	// Compare the flags and image defined on the statefulset with the
	// node's flags and image. Since conversion from FlagsMap to
	// EnvVar is lossy (a flag value is `any` and EnvVar.Value is a
	// string), need to compare on the []EnvVar side. So, no way to create
	// FlagsMap from EnvVar.
	// TODO(marun) Reconsider usage of FlagsMap instead of just map[string]string
	container := statefulset.Spec.Template.Spec.Containers[0]
	sortEnvVars(container.Env) // Ensure both are sorted
	flags, err := p.getFlagsForPod()
	if err != nil {
		return err
	}
	nodeEnv := flagsToEnvVarSlice(flags)
	if !slices.Equal(container.Env, nodeEnv) {
		patches = append(patches, map[string]any{
			"op":    "replace",
			"path":  "/spec/template/spec/containers/0/env",
			"value": envVarsToJSONValue(nodeEnv),
		})
	}

	nodeImage := p.runtimeConfig().Image
	if container.Image != nodeImage {
		patches = append(patches, map[string]any{
			"op":    "replace",
			"path":  "/spec/template/spec/containers/0/image",
			"value": nodeImage,
		})
	}

	if len(patches) == 0 {
		// TODO(marun) Rather than skipping restart, scale down and scale up the statefulset. Maybe optionally?
		log.Info("skipped restart - configuration unchanged")
		return nil
	}

	patchBytes, err := json.Marshal(patches)
	if err != nil {
		return err
	}

	clientset, err := p.getClientset()
	if err != nil {
		return err
	}
	runtimeConfig := p.runtimeConfig()

	// Apply the patch to the StatefulSet
	updatedStatefulSet, err := clientset.AppsV1().StatefulSets(runtimeConfig.Namespace).Patch(
		ctx,
		p.getStatefulSetName(),
		types.JSONPatchType,
		patchBytes,
		metav1.PatchOptions{},
	)
	if err != nil {
		return err
	}

	if updatedStatefulSet.Generation == statefulset.Generation {
		// Generation unchanged - no rollout expected
		return nil
	}

	replicas := int32(1)
	if err := wait.PollImmediateInfinite(statusCheckInterval, func() (bool, error) {
		statefulset, err := p.getStatefulSet(ctx)
		if err != nil {
			log.Debug("failed to retrieve statefulset",
				zap.Error(err),
			)
			return false, nil
		}
		status := statefulset.Status
		finishedRollingOut := (status.ObservedGeneration >= updatedStatefulSet.Generation &&
			status.Replicas == replicas &&
			status.ReadyReplicas == replicas &&
			status.CurrentReplicas == replicas &&
			status.UpdatedReplicas == replicas)
		if finishedRollingOut {
			log.Info("statefulset finished rolling out",
				zap.String("namespace", statefulset.Namespace),
				zap.String("name", statefulset.Name),
			)
		}
		return finishedRollingOut, nil
	}); err != nil {
		return fmt.Errorf("failed to wait for statefulset to finish rolling out: %w", err)
	}

	// TODO(marun) Poll loops like this need to use contexts
	if err := wait.PollImmediateInfinite(statusCheckInterval, func() (bool, error) {
		_, err := p.IsHealthy(ctx)
		// If no error is returned, the node must be accepting api
		// calls which means it might become healthy if the other
		// validators in the network are started.
		return err == nil, nil
	}); err != nil {
		return fmt.Errorf("failed to wait for the node to start accepting connections: %w", err)
	}

	return nil
}

func (p *KubeRuntime) checkRunning(ctx context.Context) error {
	err := p.readState(ctx)
	if err != nil {
		return err
	}
	if len(p.node.URI) == 0 {
		return errNotRunning
	}
	return nil
}

func (p *KubeRuntime) IsHealthy(ctx context.Context) (bool, error) {
	err := p.checkRunning(ctx)
	if err != nil {
		return false, err
	}

	// TODO(marun) Reuse this forwarded connection for more than a single health check
	uri, cancel, err := p.GetLocalURI(ctx)
	if err != nil {
		return false, err
	}
	defer cancel()

	healthReply, err := CheckNodeHealth(ctx, uri)
	if errors.Is(ErrUnrecoverableNodeHealthCheck, err) {
		return false, err
	} else if err != nil {
		// TODO(maybe debug log the error?)
		return false, nil
	}
	return healthReply.Healthy, nil
}

// TODO(marun) Support using a kubeconfig context
func (p *KubeRuntime) getKubeconfig() (*restclient.Config, error) {
	// TODO(marun) inClusterConfig requires an empty path. How best to ensure this?
	kubeconfigPath := os.Getenv("KUBECONFIG")
	if kubeconfigPath == "" {
		kubeconfigPath = p.runtimeConfig().ConfigPath
	}
	return clientcmd.BuildConfigFromFlags("", kubeconfigPath)
}

func (p *KubeRuntime) getClientset() (*kubernetes.Clientset, error) {
	kubeconfig, err := p.getKubeconfig()
	if err != nil {
		return nil, err
	}
	clientset, err := kubernetes.NewForConfig(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset: %w", err)
	}
	return clientset, nil
}

func (p *KubeRuntime) runtimeConfig() *KubeRuntimeConfig {
	return p.node.getRuntimeConfig().Kube
}

func (p *KubeRuntime) getStatefulSet(ctx context.Context) (*appsv1.StatefulSet, error) {
	clientset, err := p.getClientset()
	if err != nil {
		return nil, err
	}
	return clientset.AppsV1().StatefulSets(p.runtimeConfig().Namespace).Get(
		ctx,
		p.getStatefulSetName(),
		metav1.GetOptions{},
	)
}

func (p *KubeRuntime) forwardPort(ctx context.Context, port int) (uint16, chan struct{}, error) {
	kubeconfig, err := p.getKubeconfig()
	if err != nil {
		return 0, nil, err
	}
	clientset, err := p.getClientset()
	if err != nil {
		return 0, nil, err
	}

	statefulSetName := p.getStatefulSetName()
	namespace := p.runtimeConfig().Namespace

	podName := statefulSetName + "-0"

	// Wait for the pod to become ready (otherwise it won't be accepting network connections)
	if err := WaitForPodCondition(ctx, clientset, namespace, podName, corev1.PodReady); err != nil {
		return 0, nil, err
	}

	forwardedPort, stopChan, err := enableLocalForwardForPod(
		kubeconfig,
		namespace,
		podName,
		port,
		io.Discard, // Ignore stdout output
		os.Stderr,
	)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to enable local forward for pod: %w", err)
	}
	return forwardedPort, stopChan, nil
}

func (p *KubeRuntime) GetLocalURI(ctx context.Context) (string, func(), error) {
	if len(p.node.URI) == 0 {
		return "", func() {}, errNotRunning
	}

	// TODO(marun) Auto-detect whether this test code is running inside the cluster
	//             and use the URI directly

	port, stopChan, err := p.forwardPort(ctx, config.DefaultHTTPPort)
	if err != nil {
		return "", nil, err
	}
	return fmt.Sprintf("http://127.0.0.1:%d", port), func() { close(stopChan) }, nil
}

func (p *KubeRuntime) GetLocalStakingAddress(ctx context.Context) (netip.AddrPort, func(), error) {
	if p.node.StakingAddress == (netip.AddrPort{}) {
		return netip.AddrPort{}, func() {}, errNotRunning
	}

	// TODO(marun) Auto-detect whether this test code is running inside the cluster
	//             and use the URI directly

	port, stopChan, err := p.forwardPort(ctx, config.DefaultStakingPort)
	if err != nil {
		return netip.AddrPort{}, nil, err
	}
	return netip.AddrPortFrom(
		netip.AddrFrom4([4]byte{127, 0, 0, 1}),
		port,
	), func() { close(stopChan) }, nil
}
