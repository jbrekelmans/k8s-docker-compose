package get

import (
	"fmt"

	"github.com/jbrekelmans/kube-compose/pkg/config"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	clientV1 "k8s.io/client-go/kubernetes/typed/core/v1"
)

type getRunner struct {
	cfg              *config.Config
	k8sClientset     *kubernetes.Clientset
	k8sServiceClient clientV1.ServiceInterface
}

type KubeComposeService struct {
	Service        string
	Hostname       string
	Namespace      string
	ClusterIP      string
	ExternalIP     []string
	LoadBalancerIP string
}

func Service(cfg *config.Config, serviceName string) (KubeComposeService, error) {
	composeService := KubeComposeService{}
	g := &getRunner{
		cfg: cfg,
	}
	service := cfg.CanonicalComposeFile.Services[serviceName]
	if service == nil {
		return composeService, fmt.Errorf("no service named %#v exists", serviceName)
	}
	result, err := g.run(service)
	if err != nil {
		return composeService, err
	}
	composeService = KubeComposeService{
		Service:        result.Name,
		Hostname:       result.Name + "." + result.Namespace + ".svc.cluster.local",
		Namespace:      result.Namespace,
		ClusterIP:      result.Spec.ClusterIP,
		ExternalIP:     result.Spec.ExternalIPs,
		LoadBalancerIP: result.Spec.LoadBalancerIP,
	}
	return composeService, nil
}

func (g *getRunner) initKubernetesClientset() error {
	k8sClientset, err := kubernetes.NewForConfig(g.cfg.KubeConfig)
	if err != nil {
		return err
	}
	g.k8sClientset = k8sClientset
	g.k8sServiceClient = g.k8sClientset.CoreV1().Services(g.cfg.Namespace)
	return nil
}

func (g *getRunner) getK8sServiceResource(service *config.Service) (*v1.Service, error) {
	options := &metav1.GetOptions{}
	result, err := g.k8sServiceClient.Get(service.NameEscaped()+"-"+g.cfg.EnvironmentID, *options)
	if err != nil {
		return result, err
	}
	return result, nil
}

func (g *getRunner) run(service *config.Service) (*v1.Service, error) {
	err := g.initKubernetesClientset()
	if err != nil {
		return nil, err
	}
	result, err := g.getK8sServiceResource(service)
	if err != nil {
		return result, err
	}
	return result, nil
}