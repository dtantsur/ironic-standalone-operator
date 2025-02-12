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
	"crypto/tls"
	"flag"
	"fmt"
	"os"
	"strings"

	"k8s.io/client-go/kubernetes"
	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/rest"

	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	cliflag "k8s.io/component-base/cli/flag"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	metal3iov1alpha1 "github.com/metal3-io/ironic-standalone-operator/api/v1alpha1"
	"github.com/metal3-io/ironic-standalone-operator/internal/controller"
	"github.com/metal3-io/ironic-standalone-operator/pkg/ironic"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = k8sruntime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(metal3iov1alpha1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

const (
	TLSVersion12 = "TLS12"
	TLSVersion13 = "TLS13"
)

var tlsSupportedVersions = []string{TLSVersion12, TLSVersion13}

type TLSOptions struct {
	TLSMaxVersion   string
	TLSMinVersion   string
	TLSCipherSuites string
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var webhookPort int
	var watchNamespace string
	var tlsOptions TLSOptions
	var controllerConcurrency int
	var clusterDomain string
	var versionInfo ironic.VersionInfo
	var installedVersion string
	var featureGates map[string]bool

	tlsCipherPreferredValues := cliflag.PreferredTLSCipherNames()
	tlsCipherInsecureValues := cliflag.InsecureTLSCipherNames()

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.IntVar(&webhookPort, "webhook-port", 9443, "Port to use for webhooks (0 to disable)")
	flag.StringVar(&watchNamespace, "namespace", os.Getenv("WATCH_NAMESPACE"),
		"Namespace that the controller watches to reconcile resources.")
	flag.StringVar(&tlsOptions.TLSMinVersion, "tls-min-version", TLSVersion12,
		"The minimum TLS version in use by the webhook server.\n"+
			fmt.Sprintf("Possible values are %s.", strings.Join(tlsSupportedVersions, ", ")))
	flag.StringVar(&tlsOptions.TLSMaxVersion, "tls-max-version", TLSVersion13,
		"The maximum TLS version in use by the webhook server.\n"+
			fmt.Sprintf("Possible values are %s.", strings.Join(tlsSupportedVersions, ", ")))
	flag.StringVar(&tlsOptions.TLSCipherSuites, "tls-cipher-suites", "",
		"Comma-separated list of cipher suites for the webhook server. "+
			"If omitted, the default Go cipher suites will be used. \n"+
			"Preferred values: "+strings.Join(tlsCipherPreferredValues, ", ")+". \n"+
			"Insecure values: "+strings.Join(tlsCipherInsecureValues, ", ")+".")
	flag.IntVar(&controllerConcurrency, "controller-concurrency", 0,
		"Number of resources of each type to process simultaneously.")
	flag.StringVar(&clusterDomain, "cluster-domain", os.Getenv("CLUSTER_DOMAIN"),
		"Domain name of the current cluster, e.g. cluster.local.")

	flag.StringVar(&versionInfo.IronicImage, "ironic-image", os.Getenv("IRONIC_IMAGE"),
		"Ironic image to install.")
	flag.StringVar(&versionInfo.MariaDBImage, "mariadb-image", os.Getenv("MARIADB_IMAGE"),
		"MariaDB image to install.")
	flag.StringVar(&versionInfo.RamdiskDownloaderImage, "ramdisk-downloader-image", os.Getenv("RAMDISK_DOWNLOADER_IMAGE"),
		"Ramdisk downloader image to install.")
	flag.StringVar(&versionInfo.KeepalivedImage, "keepalived-image", os.Getenv("KEEPALIVED_IMAGE"),
		"Keepalived image to install.")
	flag.StringVar(&installedVersion, "ironic-version", os.Getenv("IRONIC_VERSION"),
		"Branch of Ironic that the operator installs.")

	featureGatesFlag := cliflag.NewMapStringBool(&featureGates)
	if defaultFeatureGates := os.Getenv("FEATURE_GATES"); defaultFeatureGates != "" {
		if err := featureGatesFlag.Set(defaultFeatureGates); err != nil {
			setupLog.Error(err, "unable to parse feature gates")
			os.Exit(1)
		}
	}
	flag.Var(featureGatesFlag, "feature-gates", "A set of key=value pairs that describe feature gates:\n"+
		strings.Join(metal3iov1alpha1.CurrentFeatureGate.KnownFeatures(), "\n"))

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	if installedVersion == "" {
		versionInfo.InstalledVersion = ironic.DefaultVersion
	} else {
		parsedVersion, err := metal3iov1alpha1.ParseVersion(installedVersion)
		if err != nil {
			setupLog.Error(err, "unable to parse ironic-version")
			os.Exit(1)
		}
		versionInfo.InstalledVersion = parsedVersion
	}

	if err := metal3iov1alpha1.CurrentFeatureGate.SetFromMap(featureGates); err != nil {
		setupLog.Error(err, "unable to parse feature gates")
		os.Exit(1)
	}

	setupLog.Info("enabling features", "FeatureGate", metal3iov1alpha1.CurrentFeatureGate.String())

	config := ctrl.GetConfigOrDie()
	kubeClient := kubernetes.NewForConfigOrDie(rest.AddUserAgent(config, "ironic-standalone-operator"))

	tlsOptionOverrides, err := GetTLSOptionOverrideFuncs(tlsOptions)
	if err != nil {
		setupLog.Error(err, "unable to add TLS settings to the webhook server")
		os.Exit(1)
	}

	var watchNamespaces map[string]cache.Config
	if watchNamespace != "" {
		watchNamespaces = map[string]cache.Config{
			watchNamespace: {},
		}
	}

	ctrlOpts := ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress:    metricsAddr,
			FilterProvider: filters.WithAuthenticationAndAuthorization,
		},
		WebhookServer: webhook.NewServer(webhook.Options{
			Port:    webhookPort,
			TLSOpts: tlsOptionOverrides,
		}),
		HealthProbeBindAddress:  probeAddr,
		LeaderElection:          enableLeaderElection,
		LeaderElectionID:        "ironic.metal3.io",
		LeaderElectionNamespace: watchNamespace,
		Cache: cache.Options{
			DefaultNamespaces: watchNamespaces,
		},
	}

	mgr, err := ctrl.NewManager(config, ctrlOpts)
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err = (&controller.IronicReconciler{
		Client:      mgr.GetClient(),
		KubeClient:  kubeClient,
		Scheme:      mgr.GetScheme(),
		Log:         ctrl.Log.WithName("controllers").WithName("Ironic"),
		Domain:      clusterDomain,
		VersionInfo: versionInfo,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Ironic")
		os.Exit(1)
	}
	if webhookPort != 0 {
		if err = (&metal3iov1alpha1.Ironic{}).SetupWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create webhook", "webhook", "Ironic")
			os.Exit(1)
		}
	}
	if err = (&controller.IronicDatabaseReconciler{
		Client:      mgr.GetClient(),
		KubeClient:  kubeClient,
		Scheme:      mgr.GetScheme(),
		Log:         ctrl.Log.WithName("controllers").WithName("IronicDatabase"),
		VersionInfo: versionInfo,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "IronicDatabase")
		os.Exit(1)
	}
	if webhookPort != 0 {
		if err = (&metal3iov1alpha1.IronicDatabase{}).SetupWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create webhook", "webhook", "IronicDatabase")
			os.Exit(1)
		}
	}
	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

// GetTLSOptionOverrideFuncs returns a list of TLS configuration overrides to be used
// by the webhook server.
func GetTLSOptionOverrideFuncs(options TLSOptions) ([]func(*tls.Config), error) {
	var tlsOptions []func(config *tls.Config)

	tlsMinVersion, err := GetTLSVersion(options.TLSMinVersion)
	if err != nil {
		return nil, err
	}

	tlsMaxVersion, err := GetTLSVersion(options.TLSMaxVersion)
	if err != nil {
		return nil, err
	}

	if tlsMaxVersion != 0 && tlsMinVersion > tlsMaxVersion {
		return nil, fmt.Errorf("TLS version flag min version (%s) is greater than max version (%s)",
			options.TLSMinVersion, options.TLSMaxVersion)
	}

	tlsOptions = append(tlsOptions, func(cfg *tls.Config) {
		cfg.MinVersion = tlsMinVersion
	})

	tlsOptions = append(tlsOptions, func(cfg *tls.Config) {
		cfg.MaxVersion = tlsMaxVersion
	})
	// Cipher suites should not be set if empty.
	if tlsMinVersion >= tls.VersionTLS13 &&
		options.TLSCipherSuites != "" {
		setupLog.Info("warning: Cipher suites should not be set for TLS version 1.3. Ignoring ciphers")
		options.TLSCipherSuites = ""
	}

	if options.TLSCipherSuites != "" {
		tlsCipherSuites := strings.Split(options.TLSCipherSuites, ",")
		suites, err := cliflag.TLSCipherSuites(tlsCipherSuites)
		if err != nil {
			return nil, err
		}

		insecureCipherValues := cliflag.InsecureTLSCipherNames()
		for _, cipher := range tlsCipherSuites {
			for _, insecureCipherName := range insecureCipherValues {
				if insecureCipherName == cipher {
					setupLog.Info(fmt.Sprintf("warning: use of insecure cipher '%s' detected.", cipher))
				}
			}
		}
		tlsOptions = append(tlsOptions, func(cfg *tls.Config) {
			cfg.CipherSuites = suites
		})
	}

	return tlsOptions, nil
}

// GetTLSVersion returns the corresponding tls.Version or error.
func GetTLSVersion(version string) (uint16, error) {
	var v uint16

	switch version {
	case TLSVersion12:
		v = tls.VersionTLS12
	case TLSVersion13:
		v = tls.VersionTLS13
	default:
		return 0, fmt.Errorf("unexpected TLS version %q (must be one of: %s)", version, strings.Join(tlsSupportedVersions, ", "))
	}
	return v, nil
}
