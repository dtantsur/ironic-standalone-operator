/*

Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	ginkgotypes "github.com/onsi/ginkgo/v2/types"
	. "github.com/onsi/gomega"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/baremetal/apiversions"
	"github.com/gophercloud/gophercloud/v2/openstack/baremetal/v1/conductors"
	"github.com/gophercloud/gophercloud/v2/openstack/baremetal/v1/nodes"
	mariadbapi "github.com/mariadb-operator/mariadb-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	yaml "sigs.k8s.io/yaml"

	metal3api "github.com/metal3-io/ironic-standalone-operator/api/v1alpha1"
	"github.com/metal3-io/ironic-standalone-operator/test/helpers"
)

// NOTE(dtantsur): these two constants refer to the Ironic API version (which
// is different from the version of Ironic itself). Versions are incremented
// every time the API is changed. The listing of all versions is here:
// https://docs.openstack.org/ironic/latest/contributor/webapi-version-history.html
const (
	// NOTE(dtantsur): latest is now at least 1.99, so we can rely on this
	// value to check that specifying Version: 33.0 actually installs 33.0.
	apiVersionIn310 = "1.99"
	apiVersionIn320 = "1.101"
	apiVersionIn330 = "1.104"
	// Update this periodically to make sure we're installing the latest version by default.
	knownAPIMinorVersion = 104

	numberOfNodes = 100

	clusterTypeKind = "kind"
)

var ctx context.Context
var k8sClient client.Client
var clientset *kubernetes.Clientset

var clusterType string
var ironicIPs []string

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Functional tests")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx = context.TODO()

	kubeconfigPath := os.Getenv("KUBECONFIG")
	if kubeconfigPath == "" {
		kubeconfigPath = os.Getenv("HOME") + "/.kube/config"
	}
	Expect(kubeconfigPath).To(BeAnExistingFile(), "Failed to get the kubeconfig file for the cluster")

	var err error

	err = metal3api.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())
	err = mariadbapi.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	Expect(err).NotTo(HaveOccurred())

	clientset, err = kubernetes.NewForConfig(cfg)
	Expect(err).NotTo(HaveOccurred())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	clusterType = os.Getenv("CLUSTER_TYPE")
	if clusterType == "" {
		clusterType = clusterTypeKind // for now
	}

	ironicIP := os.Getenv("IRONIC_IP")
	if clusterType == clusterTypeKind {
		Expect(ironicIP).NotTo(BeEmpty())
		ironicIPs = []string{ironicIP}
	} else {
		listOptions := metav1.ListOptions{
			LabelSelector: "node-role.kubernetes.io/control-plane",
		}
		nodes, err := clientset.CoreV1().Nodes().List(ctx, listOptions)
		Expect(err).NotTo(HaveOccurred())
		for _, node := range nodes.Items {
			for _, addr := range node.Status.Addresses {
				if addr.Type == corev1.NodeInternalIP {
					ironicIPs = append(ironicIPs, addr.Address)
				}
			}
		}
		Expect(ironicIPs).NotTo(BeEmpty())
	}

	helpers.LoadIronicCert()
	helpers.LoadCustomImages()
	helpers.LoadDatabaseParams()
})

func logResources(ironic *metal3api.Ironic, suffix string) {
	deployName := ironic.Name + "-service"
	if ironic.Spec.HighAvailability {
		deploy, err := clientset.AppsV1().DaemonSets(ironic.Namespace).Get(ctx, deployName, metav1.GetOptions{})
		if err == nil {
			GinkgoWriter.Printf(".. status of daemon set: %+v\n", deploy.Status)
			writeYAML(deploy, deploy.Namespace, deploy.Name, "daemonset"+suffix)
		} else if !k8serrors.IsNotFound(err) {
			Expect(err).NotTo(HaveOccurred())
		}
	} else {
		deploy, err := clientset.AppsV1().Deployments(ironic.Namespace).Get(ctx, deployName, metav1.GetOptions{})
		if err == nil {
			GinkgoWriter.Printf(".. status of deployment: %+v\n", deploy.Status)
			writeYAML(deploy, deploy.Namespace, deploy.Name, "deployment"+suffix)
		} else if !k8serrors.IsNotFound(err) {
			Expect(err).NotTo(HaveOccurred())
		}
	}

	pods, err := clientset.CoreV1().Pods(ironic.Namespace).List(ctx, metav1.ListOptions{})
	Expect(err).NotTo(HaveOccurred())

	for _, pod := range pods.Items {
		var notReady []string
		if pod.Status.Phase != corev1.PodSucceeded {
			for _, cont := range pod.Status.InitContainerStatuses {
				if !cont.Ready {
					notReady = append(notReady, cont.Name)
				}
			}
			for _, cont := range pod.Status.ContainerStatuses {
				if !cont.Ready {
					notReady = append(notReady, cont.Name)
				}
			}
		}
		GinkgoWriter.Printf("... status of pod %s: %s, not ready: %+v\n", pod.Name, pod.Status.Phase, notReady)
	}

	CollectLogs(ironic.Namespace)
}

func WaitForIronic(name types.NamespacedName) *metal3api.Ironic {
	ironic := &metal3api.Ironic{}

	By("waiting for Ironic deployment")

	Eventually(func() bool {
		err := k8sClient.Get(ctx, name, ironic)
		Expect(err).NotTo(HaveOccurred())

		writeYAML(ironic, ironic.Namespace, ironic.Name, "ironic")
		GinkgoWriter.Printf("Current status of Ironic: %+v\n", ironic.Status)

		cond := meta.FindStatusCondition(ironic.Status.Conditions, string(metal3api.IronicStatusReady))
		if cond != nil && cond.ObservedGeneration >= ironic.Generation {
			expectedVersion := ironic.Spec.Version
			if expectedVersion != "" {
				Expect(ironic.Status.RequestedVersion).To(Equal(expectedVersion))
			} else {
				Expect(ironic.Status.RequestedVersion).ToNot(BeEmpty())
			}

			if cond.Status == metav1.ConditionTrue {
				if expectedVersion != "" {
					Expect(ironic.Status.InstalledVersion).To(Equal(expectedVersion))
				} else {
					Expect(ironic.Status.InstalledVersion).ToNot(BeEmpty())
				}
				logResources(ironic, "")

				// See https://github.com/metal3-io/ironic-standalone-operator/issues/304
				return !podsAreBeingDeleted(ctx, name.Namespace, name.Name)
			}
		}

		logResources(ironic, "")
		return false
	}).WithTimeout(5 * time.Minute).WithPolling(10 * time.Second).Should(BeTrue())

	return ironic
}

func WaitForUpgrade(name types.NamespacedName, toVersion string) *metal3api.Ironic {
	ironic := &metal3api.Ironic{}
	suffix := "-upgraded-" + toVersion

	By("waiting for Ironic deployment")

	Eventually(func() bool {
		err := k8sClient.Get(ctx, name, ironic)
		Expect(err).NotTo(HaveOccurred())

		writeYAML(ironic, ironic.Namespace, ironic.Name, "ironic")
		GinkgoWriter.Printf("Current status of Ironic: %+v\n", ironic.Status)

		cond := meta.FindStatusCondition(ironic.Status.Conditions, string(metal3api.IronicStatusReady))
		Expect(cond).ToNot(BeNil(), "on upgrade, the Ready condition must always be present")

		Expect(ironic.Spec.Version).To(Equal(toVersion), "unexpected Version in the Spec")
		upgradeAcknowledged := ironic.Status.RequestedVersion == toVersion
		upgraded := ironic.Status.InstalledVersion == toVersion
		if upgraded {
			Expect(upgradeAcknowledged).To(BeTrue(), "InstalledVersion set before RequestedVersion")
		}

		if upgradeAcknowledged && cond.Status == metav1.ConditionTrue {
			Expect(upgraded).To(BeTrue(), "the Ready condition set before InstalledVersion")
			logResources(ironic, suffix)

			// See https://github.com/metal3-io/ironic-standalone-operator/issues/304
			return !podsAreBeingDeleted(ctx, name.Namespace, name.Name)
		}

		Expect(upgraded).To(BeFalse(), "InstalledVersion set before the Ready condition")

		logResources(ironic, suffix)
		return false
	}).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(BeTrue())

	return ironic
}

func WaitForIronicFailure(name types.NamespacedName, message string, tolerateReady bool) {
	ironic := &metal3api.Ironic{}

	By("waiting for Ironic deployment to fail")

	Eventually(func() bool {
		err := k8sClient.Get(ctx, name, ironic)
		Expect(err).NotTo(HaveOccurred())

		writeYAML(ironic, ironic.Namespace, ironic.Name, "ironic")
		GinkgoWriter.Printf("Current status of Ironic: %+v\n", ironic.Status)

		cond := meta.FindStatusCondition(ironic.Status.Conditions, string(metal3api.IronicStatusReady))
		if cond == nil {
			GinkgoWriter.Printf("No Ready condition yet\n")
			return false
		}
		if !tolerateReady {
			Expect(cond.Status).To(Equal(metav1.ConditionFalse), "Unexpected Ready status")
		}
		if cond.Reason != metal3api.IronicReasonFailed {
			return false
		}

		if !strings.Contains(cond.Message, message) {
			GinkgoWriter.Printf("Different error for now: %s\n", cond.Message)
			return false
		}

		return true
	}).WithTimeout(90 * time.Second).WithPolling(5 * time.Second).Should(BeTrue())
}

func writeYAML(obj interface{}, namespace, name, typ string) {
	fileDir := fmt.Sprintf("%s/%s", os.Getenv("LOGDIR"), namespace)
	err := os.MkdirAll(fileDir, 0o755)
	Expect(err).NotTo(HaveOccurred())

	fileName := fmt.Sprintf("%s/%s_%s.yaml", fileDir, typ, name)
	yamlData, err := yaml.Marshal(obj)
	Expect(err).NotTo(HaveOccurred())
	err = os.WriteFile(fileName, yamlData, 0o600)
	Expect(err).NotTo(HaveOccurred())
}

type TestAssumptions struct {
	// Assume that TLS is used
	withTLS bool

	// Assume that this secret is used for accessing the API
	apiSecret *corev1.Secret

	// Verify that an active conductor with this name exists
	activeConductor string

	// Verify that the maximum available version equals this one
	maxAPIVersion string

	// Verify that the downloader is disabled
	disableDownloader bool

	// Assume that HA is used
	withHA bool

	// Assume presence of ironic-prometheus-exporter
	withPrometheusExporter bool
}

func verifyAPIVersion(ctx context.Context, cli *gophercloud.ServiceClient, assumptions TestAssumptions) {
	By("checking Ironic API versions")

	version, err := apiversions.Get(ctx, cli, "v1").Extract()
	Expect(err).NotTo(HaveOccurred())
	if assumptions.maxAPIVersion != "" {
		Expect(version.Version).To(Equal(assumptions.maxAPIVersion))
	} else if !helpers.UsesCustomImage() {
		// NOTE(dtantsur): we cannot make any assumptions about the provided image and version,
		// so this check only runs when they are not set.
		var minorVersion int
		_, err = fmt.Sscanf(version.Version, "1.%d", &minorVersion)
		Expect(err).NotTo(HaveOccurred())
		Expect(minorVersion).To(BeNumerically(">=", knownAPIMinorVersion))
	}
}

func getIronicPods(ctx context.Context, namespace, name string) []corev1.Pod {
	listOptions := metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s-service", metal3api.IronicAppLabel, name),
	}
	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, listOptions)
	Expect(err).NotTo(HaveOccurred())
	return pods.Items
}

func podsAreBeingDeleted(ctx context.Context, namespace, name string) (result bool) {
	pods := getIronicPods(ctx, namespace, name)
	for _, pod := range pods {
		if pod.DeletionTimestamp != nil {
			GinkgoWriter.Printf("Pod %s/%s is being deleted (possibly a quirk of minikube)\n", pod.Namespace, pod.Name)
			result = true
		}
	}
	return
}

func getCurrentIronicIPs(ctx context.Context, namespace, name string) []string {
	if clusterType == clusterTypeKind {
		return ironicIPs
	}

	pods := getIronicPods(ctx, namespace, name)
	Expect(pods).NotTo(BeEmpty())

	addresses := make([]string, 0, len(pods))
	for _, pod := range pods {
		// Only use one address per pod, no need for both IP families
		if pod.Status.Phase == corev1.PodRunning {
			addresses = append(addresses, pod.Status.HostIP)
		}
	}
	return addresses
}

func verifyHTTPD(ctx context.Context, currentIronicIPs []string, assumptions TestAssumptions) {
	By("checking the httpd server existence and the ramdisk downloader")

	httpClient := helpers.NewHTTPClient()

	expectedCode := 200
	if assumptions.disableDownloader {
		expectedCode = 404
	}

	// NOTE(dtantsur): each Ironic replica has its own httpd, so verify them all independently
	for _, ironicIP := range currentIronicIPs {
		testURL := fmt.Sprintf("http://%s/images/ironic-python-agent.kernel", net.JoinHostPort(ironicIP, "6180"))
		statusCode := helpers.GetStatusCode(ctx, &httpClient, testURL)
		Expect(statusCode).To(Equal(expectedCode))
	}

	if assumptions.withTLS {
		for _, ironicIP := range currentIronicIPs {
			testURL := fmt.Sprintf("https://%s/redfish/", net.JoinHostPort(ironicIP, "6183"))
			statusCode := helpers.GetStatusCode(ctx, &httpClient, testURL)
			// NOTE(dtantsur): without any valid virtual media images nothing will return a success code (not even /redfish/).
			// We get 200, 403 or 404 depending on a few factors. Check at least that we don't have 5xx.
			Expect(statusCode).To(BeNumerically("<", 500))
		}
	}
}

func verifyAuthentication(ctx context.Context, ironicURLs []string) {
	By("checking Ironic authentication")

	for _, ironicURL := range ironicURLs {
		cli, err := helpers.NewNoAuthClient(ironicURL)
		Expect(err).NotTo(HaveOccurred())
		_, err = nodes.List(cli, nodes.ListOpts{}).AllPages(ctx)
		Expect(err).To(HaveOccurred())
		Expect(gophercloud.ResponseCodeIs(err, 401)).To(BeTrue())
	}
}

func verifyRPC(ctx context.Context, ironicIPs []string, withTLS bool) {
	By("checking RPC authentication")

	httpClient := helpers.NewHTTPClient()

	for _, ironicIP := range ironicIPs {
		proto := "http://"
		if withTLS {
			proto = "https://"
		}
		testURL := proto + net.JoinHostPort(ironicIP, "6189")
		statusCode := helpers.GetStatusCode(ctx, &httpClient, testURL)
		Expect(statusCode).To(Equal(401))
	}
}

func verifyConductorList(ctx context.Context, cli *gophercloud.ServiceClient, assumptions TestAssumptions) {
	conductorPager, err := conductors.List(cli, conductors.ListOpts{}).AllPages(ctx)
	Expect(err).NotTo(HaveOccurred())
	conductors, err := conductors.ExtractConductors(conductorPager)
	Expect(err).NotTo(HaveOccurred())

	var aliveConductors []string
	for _, cond := range conductors {
		if cond.Alive {
			aliveConductors = append(aliveConductors, cond.Hostname)
		}
	}
	if assumptions.activeConductor != "" {
		Expect(aliveConductors).To(ContainElement(assumptions.activeConductor))
	} else {
		Expect(aliveConductors).NotTo(BeEmpty())
	}
}

func verifyPrometheusExporter(ctx context.Context, currentIronicIPs []string) {
	By("checking ironic-prometheus-exporter")

	httpClient := helpers.NewHTTPClient()

	// NOTE(dtantsur): each Ironic replica has its own exporter, so verify them all independently
	for _, ironicIP := range currentIronicIPs {
		testURL := fmt.Sprintf("http://%s/metrics", net.JoinHostPort(ironicIP, "9608"))
		statusCode := helpers.GetStatusCode(ctx, &httpClient, testURL)
		Expect(statusCode).To(Equal(200))
	}
}

func stressTest(ctx context.Context, clients []*gophercloud.ServiceClient) {
	group := rand.Intn(1000)

	drivers := []string{"ipmi", "redfish"}
	nodeUUIDs := make([]string, 0, numberOfNodes)
	for idx := range numberOfNodes {
		// NOTE(dtantsur): Ironic replicas are clustered, we can pick any at random
		cli := clients[rand.Intn(len(clients))]

		node, err := nodes.Create(ctx, cli, nodes.CreateOpts{
			Driver: drivers[rand.Intn(len(drivers))],
			Name:   fmt.Sprintf("node-%d-%d", group, idx),
		}).Extract()
		Expect(err).NotTo(HaveOccurred())
		nodeUUIDs = append(nodeUUIDs, node.UUID)
	}

	// NOTE(dtantsur): it's useful to leave some nodes in place to check upgrades
	numberToDelete := numberOfNodes * 9 / 10
	for i := range numberToDelete {
		nodeUUID := nodeUUIDs[i]
		cli := clients[rand.Intn(len(clients))]
		err := nodes.Delete(ctx, cli, nodeUUID).ExtractErr()
		Expect(err).NotTo(HaveOccurred())
	}
}

func VerifyIronic(ironic *metal3api.Ironic, assumptions TestAssumptions) {
	writeYAML(ironic, ironic.Namespace, ironic.Name, "ironic")

	proto := "http"
	if assumptions.withTLS {
		proto = "https"
	}

	By("checking the service")

	svc, err := clientset.CoreV1().Services(ironic.Namespace).Get(ctx, ironic.Name, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred())

	writeYAML(svc, svc.Namespace, svc.Name, "service")

	Expect(svc.Spec.ClusterIPs).ToNot(BeEmpty())

	By("fetching the authentication secret")

	secret := assumptions.apiSecret
	if secret == nil {
		secret, err = clientset.CoreV1().Secrets(ironic.Namespace).Get(ctx, ironic.Spec.APICredentialsName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
	} else {
		Expect(ironic.Spec.APICredentialsName).To(Equal(secret.Name))
	}

	// NOTE(dtantsur): in the HA scenario, verify that one Ironic exists on each node.
	currentIronicIPs := ironicIPs
	if !assumptions.withHA {
		currentIronicIPs = getCurrentIronicIPs(ctx, ironic.Namespace, ironic.Name)
	}
	GinkgoWriter.Printf("Ironic detected at the following host IPs: %+v\n", currentIronicIPs)
	ironicURLs := make([]string, 0, len(currentIronicIPs))
	for _, ironicIP := range currentIronicIPs {
		ironicURLs = append(ironicURLs, fmt.Sprintf("%s://%s", proto, net.JoinHostPort(ironicIP, "6385")))
	}

	// Do not let the test get stuck here in case of connection issues
	withTimeout, cancel := context.WithTimeout(ctx, 1*time.Minute)
	defer cancel()

	verifyHTTPD(withTimeout, currentIronicIPs, assumptions)

	verifyAuthentication(withTimeout, ironicURLs)

	if assumptions.withHA {
		verifyRPC(withTimeout, currentIronicIPs, assumptions.withTLS)
	}

	if assumptions.withPrometheusExporter {
		verifyPrometheusExporter(withTimeout, currentIronicIPs)
	}

	clients := make([]*gophercloud.ServiceClient, 0, len(ironicURLs))

	for _, ironicURL := range ironicURLs {
		cli, err := helpers.NewHTTPBasicClient(ironicURL, secret)
		Expect(err).NotTo(HaveOccurred())
		cli.Microversion = "1.81" // minimum version supported by BMO
		verifyAPIVersion(withTimeout, cli, assumptions)
		clients = append(clients, cli)
	}

	By("checking Ironic conductor list")

	for _, cli := range clients {
		verifyConductorList(ctx, cli, assumptions)
	}

	By("creating and deleting a lot of Nodes")

	withTimeout, cancel = context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	stressTest(withTimeout, clients)
}

func writeContainerLogs(pod *corev1.Pod, containerName, logDir string) {
	podLogOpts := corev1.PodLogOptions{Container: containerName}
	req := clientset.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &podLogOpts)
	podLogs, err := req.Stream(ctx)
	if err != nil {
		// Not fatal in many cases, report and move on
		GinkgoWriter.Printf("Warning: logs not available for pod %s container %s: %s\n", pod.Name, containerName, err)
		return
	}
	defer podLogs.Close()

	targetFileName := fmt.Sprintf("%s/%s.log", logDir, containerName)
	logFile, err := os.Create(targetFileName)
	Expect(err).NotTo(HaveOccurred())
	defer logFile.Close()

	_, err = io.Copy(logFile, podLogs)
	Expect(err).NotTo(HaveOccurred())
}

func collectDatabaseLogs(namespace string) {
	listOptions := client.ListOptions{Namespace: namespace}

	databases := &mariadbapi.DatabaseList{}
	err := k8sClient.List(ctx, databases, &listOptions)
	Expect(err).NotTo(HaveOccurred())

	for _, db := range databases.Items {
		writeYAML(&db, namespace, db.Name, "db")
	}

	users := &mariadbapi.UserList{}
	err = k8sClient.List(ctx, users, &listOptions)
	Expect(err).NotTo(HaveOccurred())

	for _, usr := range users.Items {
		writeYAML(&usr, namespace, usr.Name, "user")
	}

	grants := &mariadbapi.GrantList{}
	err = k8sClient.List(ctx, grants, &listOptions)
	Expect(err).NotTo(HaveOccurred())

	for _, grant := range grants.Items {
		writeYAML(&grant, namespace, grant.Name, "grant")
	}
}

func CollectLogs(namespace string) {
	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	Expect(err).NotTo(HaveOccurred())

	for _, pod := range pods.Items {
		logDir := fmt.Sprintf("%s/%s/pod_%s", os.Getenv("LOGDIR"), namespace, pod.Name)
		err = os.MkdirAll(logDir, 0o755)
		Expect(err).NotTo(HaveOccurred())

		writeYAML(&pod, namespace, pod.Name, "pod")

		for _, cont := range pod.Status.ContainerStatuses {
			writeContainerLogs(&pod, cont.Name, logDir)
		}
		for _, cont := range pod.Status.InitContainerStatuses {
			writeContainerLogs(&pod, cont.Name, logDir)
		}
	}

	rsets, err := clientset.AppsV1().ReplicaSets(namespace).List(ctx, metav1.ListOptions{})
	Expect(err).NotTo(HaveOccurred())

	for _, rset := range rsets.Items {
		logDir := fmt.Sprintf("%s/%s/replicaset_%s", os.Getenv("LOGDIR"), namespace, rset.Name)
		err = os.MkdirAll(logDir, 0o755)
		Expect(err).NotTo(HaveOccurred())

		writeYAML(&rset, namespace, rset.Name, "replicaset")
	}

	jobs, err := clientset.BatchV1().Jobs(namespace).List(ctx, metav1.ListOptions{})
	Expect(err).NotTo(HaveOccurred())

	for _, job := range jobs.Items {
		logDir := fmt.Sprintf("%s/%s/job_%s", os.Getenv("LOGDIR"), namespace, job.Name)
		err = os.MkdirAll(logDir, 0o755)
		Expect(err).NotTo(HaveOccurred())

		writeYAML(&job, namespace, job.Name, "job")
	}

	collectDatabaseLogs(namespace)
}

func DeleteAndWait(ironic *metal3api.Ironic) {
	By("deleting Ironic")

	name := types.NamespacedName{
		Name:      ironic.Name,
		Namespace: ironic.Namespace,
	}
	err := k8sClient.Delete(ctx, ironic)
	Expect(err).NotTo(HaveOccurred())

	Eventually(func() bool {
		err := k8sClient.Get(ctx, name, ironic)
		if err != nil && k8serrors.IsNotFound(err) {
			return true
		}
		GinkgoWriter.Println("Ironic", name, "not deleted yet, error is", err)
		Expect(err).NotTo(HaveOccurred())
		return false
	}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(BeTrue())
}

// Copied from kubectl pkg/cmd/events/events.go.
func eventTime(event corev1.Event) time.Time {
	if event.Series != nil {
		return event.Series.LastObservedTime.Time
	}
	if !event.LastTimestamp.Time.IsZero() {
		return event.LastTimestamp.Time
	}
	return event.EventTime.Time
}

func saveEvents(namespace string) {
	events, err := clientset.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{})
	Expect(err).NotTo(HaveOccurred())

	targetFileName := fmt.Sprintf("%s/%s/events.yaml", os.Getenv("LOGDIR"), namespace)
	logFile, err := os.Create(targetFileName)
	Expect(err).NotTo(HaveOccurred())
	defer logFile.Close()

	compareEvents := func(i, j corev1.Event) int {
		return eventTime(i).Compare(eventTime(j))
	}
	slices.SortFunc(events.Items, compareEvents)

	for _, event := range events.Items {
		yamlData, err := yaml.Marshal(event)
		Expect(err).NotTo(HaveOccurred())

		_, err = logFile.WriteString("---\n")
		Expect(err).NotTo(HaveOccurred())
		_, err = logFile.Write(yamlData)
		Expect(err).NotTo(HaveOccurred())
	}
}

func testUpgrade(ironicVersionOld string, ironicVersionNew string, apiVersionOld string, apiVersionNew string, namespace string) {
	helpers.SkipIfCustomImage()

	name := types.NamespacedName{
		Name:      "test-ironic",
		Namespace: namespace,
	}

	ironic := helpers.NewIronic(ctx, k8sClient, name, metal3api.IronicSpec{
		Version: ironicVersionOld,
	})
	DeferCleanup(func() {
		CollectLogs(namespace)
		DeleteAndWait(ironic)
	})

	ironic = WaitForIronic(name)
	VerifyIronic(ironic, TestAssumptions{maxAPIVersion: apiVersionOld})

	By("upgrading to Ironic " + ironicVersionNew)

	patch := client.MergeFrom(ironic.DeepCopy())
	ironic.Spec.Version = ironicVersionNew
	err := k8sClient.Patch(ctx, ironic, patch)
	Expect(err).NotTo(HaveOccurred())

	ironic = WaitForUpgrade(name, ironicVersionNew)
	VerifyIronic(ironic, TestAssumptions{maxAPIVersion: apiVersionNew})

	By(fmt.Sprintf("downgrading to Ironic %s (without a database)", ironicVersionOld))

	patch = client.MergeFrom(ironic.DeepCopy())
	ironic.Spec.Version = ironicVersionOld
	err = k8sClient.Patch(ctx, ironic, patch)
	Expect(err).NotTo(HaveOccurred())

	ironic = WaitForUpgrade(name, ironicVersionOld)
	VerifyIronic(ironic, TestAssumptions{maxAPIVersion: apiVersionOld})
}

func testUpgradeHA(ironicVersionOld string, ironicVersionNew string, apiVersionOld string, apiVersionNew string, namespace string) {
	helpers.SkipIfCustomImage()

	name := types.NamespacedName{
		Name:      "test-ironic",
		Namespace: namespace,
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name.Name + "-api",
			Namespace: namespace,
		},
		Data: map[string][]byte{
			corev1.BasicAuthUsernameKey: []byte("admin"),
			corev1.BasicAuthPasswordKey: []byte("test-password"),
		},
		Type: corev1.SecretTypeBasicAuth,
	}
	err := k8sClient.Create(ctx, secret)
	Expect(err).NotTo(HaveOccurred())

	ironic := helpers.NewIronic(ctx, k8sClient, name, metal3api.IronicSpec{
		Database:         helpers.CreateDatabase(ctx, k8sClient, name),
		HighAvailability: true,
		Version:          ironicVersionOld,
	})
	DeferCleanup(func() {
		CollectLogs(namespace)
		DeleteAndWait(ironic)
	})

	ironic = WaitForIronic(name)
	VerifyIronic(ironic, TestAssumptions{maxAPIVersion: apiVersionOld, withHA: true})

	By("upgrading to Ironic " + ironicVersionNew)

	patch := client.MergeFrom(ironic.DeepCopy())
	ironic.Spec.Version = ironicVersionNew
	err = k8sClient.Patch(ctx, ironic, patch)
	Expect(err).NotTo(HaveOccurred())

	ironic = WaitForUpgrade(name, ironicVersionNew)
	VerifyIronic(ironic, TestAssumptions{maxAPIVersion: apiVersionNew, withHA: true})
}

var _ = Describe("Ironic object tests", func() {
	var namespace string

	BeforeEach(func() {
		namespace = "test-" + CurrentSpecReport().LeafNodeLabels[0]
		nsSpec := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}

		_, err := clientset.CoreV1().Namespaces().Create(ctx, nsSpec, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			if !CurrentSpecReport().State.Is(ginkgotypes.SpecStateSkipped) {
				saveEvents(namespace)
			}
			_ = clientset.CoreV1().Namespaces().Delete(ctx, namespace, metav1.DeleteOptions{})
			Eventually(func() bool {
				_, err := clientset.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
				if err != nil && k8serrors.IsNotFound(err) {
					return true
				}
				GinkgoWriter.Println("Namespace", namespace, "not deleted yet, error is", err)
				Expect(err).NotTo(HaveOccurred())
				return false
			}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(BeTrue())
		})
	})

	It("creates Ironic without any parameters", Label("no-params"), func() {
		name := types.NamespacedName{
			Name:      "test-ironic",
			Namespace: namespace,
		}

		ironic := helpers.NewIronic(ctx, k8sClient, name, metal3api.IronicSpec{})
		DeferCleanup(func() {
			CollectLogs(namespace)
			DeleteAndWait(ironic)
		})

		ironic = WaitForIronic(name)
		VerifyIronic(ironic, TestAssumptions{})
	})

	It("creates Ironic with provided credentials", Label("api-secret"), func() {
		By("creating a failing Ironic with non-existent credentials")

		name := types.NamespacedName{
			Name:      "test-ironic",
			Namespace: namespace,
		}

		ironic := helpers.NewIronic(ctx, k8sClient, name, metal3api.IronicSpec{
			APICredentialsName: "banana",
		})
		DeferCleanup(func() {
			CollectLogs(namespace)
			DeleteAndWait(ironic)
		})

		WaitForIronicFailure(name, fmt.Sprintf("secret %s/banana not found", namespace), false)

		By("creating the secret and recovering the Ironic")

		secret := helpers.NewAuthSecret(ctx, k8sClient, namespace, name.Name+"-api")

		patch := client.MergeFrom(ironic.DeepCopy())
		ironic.Spec.APICredentialsName = secret.Name
		err := k8sClient.Patch(ctx, ironic, patch)
		Expect(err).NotTo(HaveOccurred())

		ironic = WaitForIronic(name)
		VerifyIronic(ironic, TestAssumptions{apiSecret: secret})

		By("changing the credentials")

		patch = client.MergeFrom(secret.DeepCopy())
		secret.Data[corev1.BasicAuthPasswordKey] = []byte("new-password")
		err = k8sClient.Patch(ctx, secret, patch)
		Expect(err).NotTo(HaveOccurred())

		// NOTE(dtantsur): this check is racy, so make sure the controller has time to catch up
		time.Sleep(1 * time.Second)

		ironic = WaitForIronic(name)
		VerifyIronic(ironic, TestAssumptions{apiSecret: secret})
	})

	It("creates Ironic with TLS", Label("tls"), func() {
		By("creating a failing Ironic with non-existent TLS")

		name := types.NamespacedName{
			Name:      "test-ironic",
			Namespace: namespace,
		}

		ironic := helpers.NewIronic(ctx, k8sClient, name, metal3api.IronicSpec{
			TLS: metal3api.TLS{
				CertificateName: "banana",
			},
		})
		DeferCleanup(func() {
			CollectLogs(namespace)
			DeleteAndWait(ironic)
		})

		WaitForIronicFailure(name, fmt.Sprintf("secret %s/banana not found", namespace), false)

		By("creating the secret and recovering the Ironic")

		secret := helpers.NewTLSSecret(ctx, k8sClient, namespace, name.Name+"-api")

		patch := client.MergeFrom(ironic.DeepCopy())
		ironic.Spec.TLS.CertificateName = secret.Name
		err := k8sClient.Patch(ctx, ironic, patch)
		Expect(err).NotTo(HaveOccurred())

		ironic = WaitForIronic(name)
		VerifyIronic(ironic, TestAssumptions{withTLS: true})
	})

	It("creates Ironic 31.0 and upgrades to 32.0", Label("v310-to-320", "upgrade"), func() {
		testUpgrade("31.0", "32.0", apiVersionIn310, apiVersionIn320, namespace)
	})

	It("creates Ironic 32.0 and upgrades to 33.0", Label("v320-to-330", "upgrade"), func() {
		testUpgrade("32.0", "33.0", apiVersionIn320, apiVersionIn330, namespace)
	})

	It("creates Ironic 33.0 and upgrades to latest", Label("v330-to-latest", "upgrade"), func() {
		testUpgrade("33.0", "latest", apiVersionIn330, "", namespace)
	})

	It("creates Ironic 31.0 with database, then upgrades it to 32.0, then 33.0", Label("db-v310-to-320-to-330", "upgrade"), func() {
		helpers.SkipIfCustomImage()

		name := types.NamespacedName{
			Name:      "test-ironic",
			Namespace: namespace,
		}

		ironic := helpers.NewIronic(ctx, k8sClient, name, metal3api.IronicSpec{
			Database: helpers.CreateDatabase(ctx, k8sClient, name),
			Version:  "31.0",
		})
		DeferCleanup(func() {
			CollectLogs(namespace)
			DeleteAndWait(ironic)
		})

		ironic = WaitForIronic(name)
		VerifyIronic(ironic, TestAssumptions{maxAPIVersion: apiVersionIn310})

		By("upgrading to Ironic 32.0")

		patch := client.MergeFrom(ironic.DeepCopy())
		ironic.Spec.Version = "32.0"
		err := k8sClient.Patch(ctx, ironic, patch)
		Expect(err).NotTo(HaveOccurred())

		ironic = WaitForUpgrade(name, "32.0")
		VerifyIronic(ironic, TestAssumptions{maxAPIVersion: apiVersionIn320})

		By("upgrading to Ironic 33.0")

		patch = client.MergeFrom(ironic.DeepCopy())
		ironic.Spec.Version = "33.0"
		err = k8sClient.Patch(ctx, ironic, patch)
		Expect(err).NotTo(HaveOccurred())

		ironic = WaitForUpgrade(name, "33.0")
		VerifyIronic(ironic, TestAssumptions{maxAPIVersion: apiVersionIn330})
	})

	It("refuses to downgrade Ironic with a database", Label("no-db-downgrade", "upgrade"), func() {
		helpers.SkipIfCustomImage()

		name := types.NamespacedName{
			Name:      "test-ironic",
			Namespace: namespace,
		}

		ironic := helpers.NewIronic(ctx, k8sClient, name, metal3api.IronicSpec{
			Database: helpers.CreateDatabase(ctx, k8sClient, name),
			Version:  "32.0",
		})
		DeferCleanup(func() {
			CollectLogs(namespace)
			DeleteAndWait(ironic)
		})

		ironic = WaitForIronic(name)
		VerifyIronic(ironic, TestAssumptions{maxAPIVersion: apiVersionIn320})

		By("downgrading to Ironic 31.0")

		patch := client.MergeFrom(ironic.DeepCopy())
		ironic.Spec.Version = "31.0"
		err := k8sClient.Patch(ctx, ironic, patch)
		Expect(err).NotTo(HaveOccurred())

		WaitForIronicFailure(name, "Ironic does not support downgrades", true)
	})

	It("creates Ironic 31.0 with HA and upgrades to 32.0", Label("ha-v310-to-320", "ha", "upgrade"), func() {
		testUpgradeHA("31.0", "32.0", apiVersionIn310, apiVersionIn320, namespace)
	})

	It("creates Ironic 32.0 with HA and upgrades to 33.0", Label("ha-v320-to-330", "ha", "upgrade"), func() {
		testUpgradeHA("32.0", "33.0", apiVersionIn320, apiVersionIn330, namespace)
	})

	It("creates Ironic 33.0 with HA and upgrades to latest", Label("ha-v330-to-latest", "ha", "upgrade"), func() {
		testUpgradeHA("33.0", "latest", apiVersionIn330, "", namespace)
	})

	It("creates Ironic with keepalived and DHCP", Label("keepalived-dnsmasq"), func() {
		name := types.NamespacedName{
			Name:      "test-ironic",
			Namespace: namespace,
		}

		ironic := helpers.NewIronic(ctx, k8sClient, name, metal3api.IronicSpec{
			Networking: metal3api.Networking{
				DHCP: &metal3api.DHCP{
					NetworkCIDR: os.Getenv("PROVISIONING_CIDR"),
					RangeBegin:  os.Getenv("PROVISIONING_RANGE_BEGIN"),
					RangeEnd:    os.Getenv("PROVISIONING_RANGE_END"),
				},
				Interface:        os.Getenv("PROVISIONING_INTERFACE"),
				IPAddress:        os.Getenv("PROVISIONING_IP"),
				IPAddressManager: metal3api.IPAddressManagerKeepalived,
			},
		})
		DeferCleanup(func() {
			CollectLogs(namespace)
			DeleteAndWait(ironic)
		})

		ironic = WaitForIronic(name)
		VerifyIronic(ironic, TestAssumptions{})
	})

	It("creates Ironic with database", Label("database"), func() {
		name := types.NamespacedName{
			Name:      "test-ironic",
			Namespace: namespace,
		}

		ironic := helpers.NewIronic(ctx, k8sClient, name, metal3api.IronicSpec{
			Database: helpers.CreateDatabase(ctx, k8sClient, name),
		})
		DeferCleanup(func() {
			CollectLogs(namespace)
			DeleteAndWait(ironic)
		})

		ironic = WaitForIronic(name)
		VerifyIronic(ironic, TestAssumptions{})
	})

	It("creates highly available Ironic", Label("ha-no-params", "ha"), func() {
		name := types.NamespacedName{
			Name:      "test-ironic",
			Namespace: namespace,
		}

		ironic := helpers.NewIronic(ctx, k8sClient, name, metal3api.IronicSpec{
			Database:         helpers.CreateDatabase(ctx, k8sClient, name),
			HighAvailability: true,
		})
		DeferCleanup(func() {
			CollectLogs(namespace)
			DeleteAndWait(ironic)
		})

		ironic = WaitForIronic(name)
		VerifyIronic(ironic, TestAssumptions{withHA: true})
	})

	It("creates highly available Ironic with TLS and credentials", Label("ha-tls-api-secret", "ha"), func() {
		name := types.NamespacedName{
			Name:      "test-ironic",
			Namespace: namespace,
		}

		apiSecret := helpers.NewAuthSecret(ctx, k8sClient, namespace, name.Name+"-api")

		tlsSecret := helpers.NewTLSSecret(ctx, k8sClient, namespace, name.Name+"-tls")

		ironic := helpers.NewIronic(ctx, k8sClient, name, metal3api.IronicSpec{
			APICredentialsName: apiSecret.Name,
			Database:           helpers.CreateDatabase(ctx, k8sClient, name),
			HighAvailability:   true,
			TLS: metal3api.TLS{
				CertificateName: tlsSecret.Name,
			},
		})
		DeferCleanup(func() {
			CollectLogs(namespace)
			DeleteAndWait(ironic)
		})

		ironic = WaitForIronic(name)
		VerifyIronic(ironic, TestAssumptions{
			apiSecret: apiSecret,
			withHA:    true,
			withTLS:   true,
		})
	})

	It("creates Ironic with extraConfig", Label("extra-config"), func() {
		name := types.NamespacedName{
			Name:      "test-ironic",
			Namespace: namespace,
		}

		conductorName := "test-conductor"

		ironic := helpers.NewIronic(ctx, k8sClient, name, metal3api.IronicSpec{
			ExtraConfig: []metal3api.ExtraConfig{
				{
					Name:  "host",
					Value: conductorName,
				},
			},
		})
		DeferCleanup(func() {
			CollectLogs(namespace)
			DeleteAndWait(ironic)
		})

		ironic = WaitForIronic(name)
		VerifyIronic(ironic, TestAssumptions{activeConductor: conductorName})
	})

	It("creates Ironic with disabled downloader", Label("disabled-downloader"), func() {
		name := types.NamespacedName{
			Name:      "test-ironic",
			Namespace: namespace,
		}

		ironic := helpers.NewIronic(ctx, k8sClient, name, metal3api.IronicSpec{
			DeployRamdisk: metal3api.DeployRamdisk{
				DisableDownloader: true,
			},
		})
		DeferCleanup(func() {
			CollectLogs(namespace)
			DeleteAndWait(ironic)
		})

		ironic = WaitForIronic(name)
		VerifyIronic(ironic, TestAssumptions{disableDownloader: true})
	})

	It("creates Ironic with prometheus exporter", Label("prometheus-exporter"), func() {

		name := types.NamespacedName{
			Name:      "test-ironic",
			Namespace: namespace,
		}

		ironic := helpers.NewIronic(ctx, k8sClient, name, metal3api.IronicSpec{
			PrometheusExporter: &metal3api.PrometheusExporter{
				DisableServiceMonitor: os.Getenv("HAS_SERVICE_MONITOR") != "true",
				Enabled:               true,
			},
		})
		DeferCleanup(func() {
			CollectLogs(namespace)
			DeleteAndWait(ironic)
		})

		ironic = WaitForIronic(name)
		VerifyIronic(ironic, TestAssumptions{withPrometheusExporter: true})
	})
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
})
