package ksuite

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"os"
	"slices"
)

var _ suite.SetupAllSuite = &KubeSuite{}
var _ suite.TearDownAllSuite = &KubeSuite{}
var _ suite.AfterTest = &KubeSuite{}

type KubeSuite struct {
	suite.Suite
	Cluster *Cluster
	Kube    kubernetes.Interface
	Config  *rest.Config

	// Don't touch the namespaces specified.
	SkipCleanNamespaces []string
	// Don't touch the namespaces that match the selectors.
	SkipCleanNamespaceSelectors []string
	// A K3D registry string pointing to external registry.
	UseRegistry string
}

func (k *KubeSuite) AfterTest(_, _ string) {
	ctx := context.Background()
	namespaces, err := k.Kube.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return
	}

	skipNamespaces := append([]string{
		"default",
		"kube-system",
		"kube-public",
	}, k.SkipCleanNamespaces...)

	skipLabels := append([]string{}, k.SkipCleanNamespaceSelectors...)

	sels := make([]labels.Selector, 0, len(skipLabels))
	for _, v := range skipLabels {
		s, err := labels.Parse(v)
		if err == nil {
			sels = append(sels, s)
		}
	}

	for _, v := range namespaces.Items {
		if slices.Contains(skipNamespaces, v.Name) ||
			slices.ContainsFunc(sels, func(val labels.Selector) bool {
				return val.Matches(labels.Set(v.Labels))
			}) {
			continue
		}

		err = k.Kube.CoreV1().Namespaces().Delete(ctx, v.Name, *metav1.NewDeleteOptions(0))
		if err != nil {
			panic(err)
		}
	}
}

func (k *KubeSuite) TearDownSuite() {
	ctx := context.Background()
	if k.Cluster != nil {
		err := DeleteCluster(ctx, k.Cluster)
		if err != nil {
			fmt.Println("Failed to clean up cluster.", err.Error())
		}
	}
}

func (k *KubeSuite) SetupSuite() {
	ctx := context.Background()
	var err error
	if k.UseRegistry == "" {
		k.UseRegistry = os.Getenv("K3D_REGISTRY")
	}

	k.Cluster, err = CreateCluster(ctx, WithUseRegistry(k.UseRegistry))
	if err != nil {
		panic(err)
	}

	k.Config, err = clientcmd.BuildConfigFromFlags("", k.Cluster.KubeConfPath)
	if err != nil {
		panic(err)
	}

	k.Kube, err = kubernetes.NewForConfig(k.Config)
	if err != nil {
		panic(err)
	}

	_ = os.Setenv(clientcmd.RecommendedConfigPathEnvVar, k.Cluster.KubeConfPath)
}
