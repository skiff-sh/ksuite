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

type CreateClusterOpts struct {
	Use         []string
	ExposePorts []v1alpha5.PortWithNodeFilters
}

type CreateClusterOpt func(c *CreateClusterOpts)

func WithUseRegistry(reg string) CreateClusterOpt {
	return func(c *CreateClusterOpts) {
		if reg == "" {
			return
		}
		c.Use = append(c.Use, reg)
	}
}

func WithIngressPort(host, con uint16) CreateClusterOpt {
	return func(c *CreateClusterOpts) {
		c.ExposePorts = append(c.ExposePorts, v1alpha5.PortWithNodeFilters{
			Port:        fmt.Sprintf("%d:%d", host, con),
			NodeFilters: []string{"loadbalancer"},
		})
	}
}

func WithNodePort(host, con uint16) CreateClusterOpt {
	return func(c *CreateClusterOpts) {
		c.ExposePorts = append(c.ExposePorts, v1alpha5.PortWithNodeFilters{
			Port:        fmt.Sprintf("%d:%d", host, con),
			NodeFilters: []string{"server:0"},
		})
	}
}

func CreateCluster(ctx context.Context, o ...CreateClusterOpt) (*Cluster, error) {
	op := &CreateClusterOpts{}
	for _, v := range o {
		v(op)
	}
	tag := version.K3sVersion
	simpleClusterConfig := v1alpha5.SimpleConfig{
		Image:   fmt.Sprintf("%s:%s", k3d.DefaultK3sImageRepo, tag),
		Servers: 1,
	}
	simpleClusterConfig.Name = uuid.New().String()

	simpleClusterConfig.Options.K3dOptions.Wait = true

	registryPort := FindFreePort(10562)

	if len(op.Use) > 0 {
		simpleClusterConfig.Registries.Use = op.Use
	} else {
		simpleClusterConfig.Registries.Create = &v1alpha5.SimpleConfigRegistryCreateConfig{
			Name:     simpleClusterConfig.Name,
			Host:     "0.0.0.0",
			HostPort: strconv.Itoa(int(registryPort)),
		}
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

	for _, v := range op.ExposePorts {
		simpleClusterConfig.Ports = append(simpleClusterConfig.Ports, v)
	}

	clusterConfig, err := config.TransformSimpleToClusterConfig(ctx, runtimes.SelectedRuntime, simpleClusterConfig, "")
	if err != nil {
		return nil, err
	}

	clusterConfig, err = config.ProcessClusterConfig(*clusterConfig)
	if err != nil {
		return nil, err
	}

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
		K3D:          &clusterConfig.Cluster,
		RegistryAddr: fmt.Sprintf("localhost:%d", registryPort),
	}

	out.HostIP, err = GetInternalIP()
	if err != nil {
		return nil, err
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
	Context      context.Context
	Cancel       context.CancelCauseFunc
	KubeConfPath string
	KubeConfig   clientcmd.ClientConfig
	K3D          *k3d.Cluster
	RegistryAddr string
	HostIP       string
}

func FindFreePort(from uint16) uint16 {
	for !IsPortFree(int(from)) {
		from++
	}

	return from
}

func IsPortFree(port int) bool {
	conn, _ := net.DialTimeout("tcp", ":"+strconv.Itoa(port), 3*time.Second)
	if conn != nil {
		defer func(conn net.Conn) {
			_ = conn.Close()
		}(conn)
		return false
	}
	return true
}

func GetInternalIP() (string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}

	for _, v := range addrs {
		if ipnet, ok := v.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String(), nil
			}
		}
	}

	return "", nil
}
