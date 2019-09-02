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

	atlasBaseURL    = testutil.GetEnvOrPanic("ATLAS_BASE_URL")
	atlasGroupID    = testutil.GetEnvOrPanic("ATLAS_GROUP_ID")
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
	if !assert.Emptyf(t, classes.Items, "Expected no service classes to exist") {
		return
	}

	// Wait up to 10 minutes for the service catalog to have been fetched and updated.
	err := testutil.Poll(10, func() (bool, error) {
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
	deploy := &appsv1.Deployment{}
	testutil.ReadInYAMLFileAndConvert("../../samples/kubernetes/used_for_e2e_tests/deploy.yaml", &deploy)

	// Environment Variable
	deploy.Spec.Template.Spec.Containers[0].Env = append(deploy.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{
		Name:  "ATLAS_BASE_URL",
		Value: atlasBaseURL,
	})

	_, err := kubeClient.AppsV1().Deployments(namespace).Create(deploy)

	if err != nil {
		return err
	}

	// Create service to expose the broker deployment internally.
	service := &v1.Service{}
	testutil.ReadInYAMLFileAndConvert("../../samples/kubernetes/used_for_e2e_tests/service.yaml", &service)
	_, err = kubeClient.CoreV1().Services(namespace).Create(service)

	return err
}

// Create secret and registerBroker will register the broker deployed in deployBroker with the
// service catalog.
func registerBroker(namespace string) error {
	authSecretName := name + "-auth"

	username := atlasPublicKey + "@" + atlasGroupID
	password := atlasPrivateKey

	// Create secret to hold the basic auth credentials for the broker.
	// The broker expects Atlas API credentials as part of the basic auth.
	kubeClient.CoreV1().Secrets(namespace).Create(&v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: authSecretName,
		},
		Data: map[string][]byte{
			"username": []byte(username),
			"password": []byte(password),
		},
	})

	servicebroker := v1beta1.ServiceBroker{}
	testutil.ReadInYAMLFileAndConvert("../../samples/kubernetes/service-broker.yaml", &servicebroker)
	servicebroker.Spec.URL = fmt.Sprintf("http://%s.%s", "atlas-service-broker", namespace)

	_, err := svcatClient.ServicecatalogV1beta1().ServiceBrokers(namespace).Create(&servicebroker)

	return err
}
