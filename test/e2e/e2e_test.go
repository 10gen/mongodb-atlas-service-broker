package e2e

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/kubernetes-incubator/service-catalog/pkg/apis/servicecatalog/v1beta1"
	servicecatalog "github.com/kubernetes-sigs/service-catalog/pkg/client/clientset_generated/clientset"
	testutil "github.com/mongodb/mongodb-atlas-service-broker/test/util"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	name              = "atlas-service-broker"
	basicAuthUsername = "username"
	basicAuthPassword = "password"
)

var (
	kubeClient  *kubernetes.Clientset
	svcatClient *servicecatalog.Clientset

	atlasBaseURL    = testutil.GetEnvOrPanic("ATLAS_GROUP_ID")
	atlasGroupID    = testutil.GetEnvOrPanic("ATLAS_BASE_URL")
	atlasPublicKey  = testutil.GetEnvOrPanic("ATLAS_PUBLIC_KEY")
	atlasPrivateKey = testutil.GetEnvOrPanic("ATLAS_PRIVATE_KEY")
	image           = testutil.GetEnvOrPanic("DOCKER_IMAGE")
)

func TestMain(m *testing.M) {
	// Load Kubernetes client config.
	kubeConfigPath := testutil.GetEnvOrPanic("KUBECONFIG")
	config, err := clientcmd.BuildConfigFromFlags("", kubeConfigPath)
	if err != nil {
		panic(err)
	}

	kubeClient, err = kubernetes.NewForConfig(config)
	if err != nil {
		panic(err)
	}

	svcatClient, err = servicecatalog.NewForConfig(config)
	if err != nil {
		panic(err)
	}

	result := m.Run()
	os.Exit(result)
}

func TestCatalog(t *testing.T) {
	t.Parallel()

	namespace := setupTest(t)
	defer cleanupTest(t)

	// Ensure the service marketplace is empty.
	classes, _ := svcatClient.ServicecatalogV1beta1().ServiceClasses(namespace).List(metav1.ListOptions{})
	if assert.Emptyf(t, classes.Items, "Expected no service classes to exist") {
		return
	}

	// Wait up to five minutes for the service catalog to have been fetched and updated.
	err := testutil.Poll(5, func() (bool, error) {
		// The catalog will create a ServiceClass object for each service
		// offered by our broker.
		classes, err := svcatClient.ServicecatalogV1beta1().ServiceClasses(namespace).List(metav1.ListOptions{})
		if err != nil {
			return false, err
		}

		if len(classes.Items) == 0 {
			return false, nil
		}

		return true, nil
	})
	assert.NoError(t, err, "Expected service classes to have been updated")

	// Ensure service plans were generated as well. Both classes and plans
	// should have been generated by the broker.
	plans, _ := svcatClient.ServicecatalogV1beta1().ServicePlans(namespace).List(metav1.ListOptions{})
	assert.NotEmpty(t, plans.Items, "Expected service plans to exist")
}

// setupTest will create a new namespace for a single test and deploy the
// broker inside.
func setupTest(t *testing.T) string {
	namespace := namespaceForTest(t)

	_, err := kubeClient.CoreV1().Namespaces().Create(&v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: namespace},
	})
	if err != nil {
		t.Fatal(err)
	}

	err = deployBroker(namespace)
	if err != nil {
		t.Fatal(err)
	}

	err = registerBroker(namespace)
	if err != nil {
		fmt.Println(err)
		t.Fatal(err)
	}

	return namespace
}

// cleanupTest will destroy a test namespace and all its resources.
func cleanupTest(t *testing.T) {
	namespace := namespaceForTest(t)

	err := kubeClient.CoreV1().Namespaces().Delete(namespace, &metav1.DeleteOptions{})
	if err != nil {
		panic(err)
	}
}

// namespaceForTest will return a namespace name based on the current test.
func namespaceForTest(t *testing.T) string {
	return fmt.Sprintf("aosb-e2e-%s", strings.ToLower(t.Name()))
}

// deployBroker will deploy the broker inside the specified namespace.
func deployBroker(namespace string) error {
	numOfReplicas := int32(1)

	// Create deployment of the broker server.
	_, err := kubeClient.AppsV1().Deployments(namespace).Create(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &numOfReplicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": name,
				},
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": name,
					},
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						v1.Container{
							Name:            name,
							Image:           image,
							ImagePullPolicy: "Never",
							Ports: []v1.ContainerPort{
								v1.ContainerPort{
									ContainerPort: int32(4000),
								},
							},
							Env: []v1.EnvVar{
								v1.EnvVar{
									Name:  "ATLAS_GROUP_ID",
									Value: atlasGroupID,
								},
								v1.EnvVar{
									Name:  "ATLAS_BASE_URL",
									Value: atlasBaseURL,
								},
								v1.EnvVar{
									Name:  "ATLAS_PUBLIC_KEY",
									Value: atlasPublicKey,
								},
								v1.EnvVar{
									Name:  "ATLAS_PRIVATE_KEY",
									Value: atlasPrivateKey,
								},
								v1.EnvVar{
									Name:  "BROKER_USERNAME",
									Value: basicAuthUsername,
								},
								v1.EnvVar{
									Name:  "BROKER_PASSWORD",
									Value: basicAuthPassword,
								},
								v1.EnvVar{
									Name:  "BROKER_HOST",
									Value: "0.0.0.0",
								},
							},
						},
					},
				},
			},
		},
	})
	if err != nil {
		return err
	}

	// Create service to expose broker deployment internally.
	_, err = kubeClient.CoreV1().Services(namespace).Create(&v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				"app": name,
			},
		},
		Spec: v1.ServiceSpec{
			Selector: map[string]string{
				"app": name,
			},
			Ports: []v1.ServicePort{
				v1.ServicePort{
					Protocol:   v1.ProtocolTCP,
					Port:       80,
					TargetPort: intstr.FromInt(4000),
				},
			},
		},
	})

	return err
}

// registerBroker will register the broker deployed in deployBroker with the
// service catalog.
func registerBroker(namespace string) error {
	authSecretName := name + "-auth"

	// Create secret to hold the basic auth credentials for the broker.
	// The service catalog needs these to come from a secret.
	kubeClient.CoreV1().Secrets(namespace).Create(&v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: authSecretName,
		},
		Data: map[string][]byte{
			"username": []byte(basicAuthUsername),
			"password": []byte(basicAuthPassword),
		},
	})

	// Register broker with the service catalog. The URL points towards the
	// internal DNS name of the broker service.
	url := fmt.Sprintf("http://%s.%s", "atlas-service-broker", namespace)
	_, err := svcatClient.ServicecatalogV1beta1().ServiceBrokers(namespace).Create(&v1beta1.ServiceBroker{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1beta1.ServiceBrokerSpec{
			CommonServiceBrokerSpec: v1beta1.CommonServiceBrokerSpec{
				URL: url,
			},
			AuthInfo: &v1beta1.ServiceBrokerAuthInfo{
				Basic: &v1beta1.BasicAuthConfig{
					SecretRef: &v1beta1.LocalObjectReference{
						Name: authSecretName,
					},
				},
			},
		},
	})

	return err
}
