package ksuite

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/k3d-io/k3d/v5/cmd/util"
	k3dcluster "github.com/k3d-io/k3d/v5/pkg/client"
	"github.com/k3d-io/k3d/v5/pkg/config"
	"github.com/k3d-io/k3d/v5/pkg/config/v1alpha5"
	"github.com/k3d-io/k3d/v5/pkg/runtimes"
	k3d "github.com/k3d-io/k3d/v5/pkg/types"
	"github.com/k3d-io/k3d/v5/version"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

var InternalNodePort = uint16(30090)

func CreateCluster(ctx context.Context) (*Cluster, error) {
	tag := version.K3sVersion
	simpleClusterConfig := v1alpha5.SimpleConfig{
		Image:   fmt.Sprintf("%s:%s", k3d.DefaultK3sImageRepo, tag),
		Servers: 1,
	}
	simpleClusterConfig.Name = uuid.New().String()

	simpleClusterConfig.Options.K3dOptions.Wait = true

	registryPort := findFreePort(10562)

	simpleClusterConfig.Registries.Create = &v1alpha5.SimpleConfigRegistryCreateConfig{
		Name:     simpleClusterConfig.Name,
		Host:     "0.0.0.0",
		HostPort: strconv.Itoa(int(registryPort)),
	}

	exposeAPI := &k3d.ExposureOpts{}
	var err error
	if simpleClusterConfig.ExposeAPI.HostPort == "" {
		exposeAPI, err = util.ParsePortExposureSpec("random", k3d.DefaultAPIPort)
		if err != nil {
			return nil, err
		}
	}
	simpleClusterConfig.ExposeAPI = v1alpha5.SimpleExposureOpts{
		Host:     exposeAPI.Host,
		HostIP:   exposeAPI.Binding.HostIP,
		HostPort: exposeAPI.Binding.HostPort,
	}

	// Expose the loadbalancer (allows for ingress)
	exposedIngressPort := findFreePort(9090)
	simpleClusterConfig.Ports = append(simpleClusterConfig.Ports, v1alpha5.PortWithNodeFilters{
		Port:        fmt.Sprintf("%d:80", exposedIngressPort),
		NodeFilters: []string{"loadbalancer"},
	})

	// Expose the agent (allows for node ports)
	exposedNodePort := findFreePort(9091)
	simpleClusterConfig.Ports = append(simpleClusterConfig.Ports, v1alpha5.PortWithNodeFilters{
		Port:        fmt.Sprintf("%d:%d", exposedNodePort, InternalNodePort),
		NodeFilters: []string{"agent"},
	})

	clusterConfig, _ := config.TransformSimpleToClusterConfig(ctx, runtimes.SelectedRuntime, simpleClusterConfig, "")

	clusterConfig, _ = config.ProcessClusterConfig(*clusterConfig)

	err = k3dcluster.ClusterRun(ctx, runtimes.SelectedRuntime, clusterConfig)
	defer func() {
		if err != nil {
			_ = k3dcluster.ClusterDelete(ctx, runtimes.SelectedRuntime, &clusterConfig.Cluster, k3d.ClusterDeleteOpts{})
		}
	}()
	if err != nil {
		return nil, err
	}

	var conf *api.Config
	conf, err = k3dcluster.KubeconfigGet(ctx, runtimes.SelectedRuntime, &clusterConfig.Cluster)
	if err != nil {
		return nil, err
	}

	out := &Cluster{
		K3D:              &clusterConfig.Cluster,
		RegistryAddr:     fmt.Sprintf("localhost:%d", registryPort),
		IngressPort:      exposedIngressPort,
		ExposedNodePort:  exposedNodePort,
		InternalNodePort: InternalNodePort,
	}

	out.Context, out.Cancel = context.WithCancelCause(ctx)
	context.AfterFunc(out.Context, func() {
		_ = DeleteCluster(ctx, out)
	})
	out.KubeConfig = clientcmd.NewDefaultClientConfig(*conf, nil)
	kubeConfDir := filepath.Join(os.TempDir(), simpleClusterConfig.Name)
	out.KubeConfPath = filepath.Join(kubeConfDir, "kube_config")
	err = clientcmd.WriteToFile(*conf, out.KubeConfPath)
	if err != nil {
		return nil, err
	}

	return out, nil
}

func DeleteCluster(ctx context.Context, cl *Cluster) error {
	err := k3dcluster.ClusterDelete(ctx, runtimes.SelectedRuntime, cl.K3D, k3d.ClusterDeleteOpts{})
	if err != nil {
		return err
	}

	_ = os.Remove(cl.KubeConfPath)

	cl.Cancel(nil)

	return nil
}

type Cluster struct {
	Context          context.Context
	Cancel           context.CancelCauseFunc
	KubeConfPath     string
	KubeConfig       clientcmd.ClientConfig
	K3D              *k3d.Cluster
	RegistryAddr     string
	IngressPort      uint16
	ExposedNodePort  uint16
	InternalNodePort uint16
}

func findFreePort(from uint16) uint16 {
	for !isPortFree(int(from)) {
		from++
	}

	return from
}

func isPortFree(port int) bool {
	conn, _ := net.DialTimeout("tcp", ":"+strconv.Itoa(port), 3*time.Second)
	if conn != nil {
		defer func(conn net.Conn) {
			_ = conn.Close()
		}(conn)
		return false
	}
	return true
}
