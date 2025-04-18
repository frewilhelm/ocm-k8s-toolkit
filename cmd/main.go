/*
Copyright 2024.

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
	// +kubebuilder:scaffold:imports
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"net/http"
	"os"
	"time"

	// to ensure that exec-entrypoint and run can make use of them.
	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"github.com/fluxcd/pkg/runtime/events"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/controller/component"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/controller/configuration"
	cfgclient "github.com/open-component-model/ocm-k8s-toolkit/internal/controller/configuration/client"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/controller/localization"
	locclient "github.com/open-component-model/ocm-k8s-toolkit/internal/controller/localization/client"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/controller/ocmrepository"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/controller/replication"
	"github.com/open-component-model/ocm-k8s-toolkit/internal/controller/resource"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/ociartifact"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/ocm"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

type RegistryParams struct {
	RegistryAddr               string
	RegistryInsecureSkipVerify bool
	RootCA                     string
	RegistryPingTimeout        time.Duration
	RegistryPingInterval       time.Duration
}

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(v1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

//nolint:funlen // this is the main function
func main() {
	var (
		metricsAddr                string
		enableLeaderElection       bool
		probeAddr                  string
		secureMetrics              bool
		enableHTTP2                bool
		eventsAddr                 string
		registryAddr               string
		rootCA                     string
		registryInsecureSkipVerify bool
		registryPingTimeout        time.Duration
	)

	const (
		registryPingInterval       = 5 * time.Second
		registryPingTimeoutDefault = 2 * time.Minute
	)

	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metric endpoint binds to. "+
		"Use the port :8080. If not set, it will be 0 in order to disable the metrics server")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", false,
		"If set the metrics endpoint is served securely")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	flag.StringVar(&eventsAddr, "events-addr", "", "The address of the events receiver.")
	flag.StringVar(
		&registryAddr,
		"registry-addr",
		"ocm-k8s-toolkit-zot-registry.ocm-k8s-toolkit-system.svc.cluster.local:5000",
		"The address of the registry (The default points to the internal registry that is deployed per default along the controllers).",
	)
	flag.StringVar(&rootCA, "rootCA", "", "path to the root CA certificate required to establish https connection to the registry.")
	flag.BoolVar(&registryInsecureSkipVerify, "registry-insecure-skip-verify", false, "Skip verification of the certificate that the registry is using.")
	flag.DurationVar(&registryPingTimeout, "registry-ping-timeout", registryPingTimeoutDefault, "Timeout to wait for the registry to become available.")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	ctx := context.Background()

	// Configure the registry access and make sure the registry is available as it is required as storage for
	// ocm component descriptors and resources.
	registry, err := configureRegistryAccess(ctx, &RegistryParams{
		RegistryAddr:               registryAddr,
		RegistryInsecureSkipVerify: registryInsecureSkipVerify,
		RootCA:                     rootCA,
		RegistryPingTimeout:        registryPingTimeout,
		RegistryPingInterval:       registryPingInterval,
	})
	if err != nil {
		setupLog.Error(err, "unable to connect to registry", "registry-addr", registryAddr)
		os.Exit(1)
	}

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("disabling http/2")
		c.NextProtos = []string{"http/1.1"}
	}

	tlsOpts := []func(*tls.Config){}
	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	webhookServer := webhook.NewServer(webhook.Options{
		TLSOpts: tlsOpts,
	})

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress:   metricsAddr,
			SecureServing: secureMetrics,
			TLSOpts:       tlsOpts,
		},
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "56490b8c.ocm.software",
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	var eventsRecorder *events.Recorder
	if eventsRecorder, err = events.NewRecorder(mgr, ctrl.Log, eventsAddr, "ocm-k8s-toolkit"); err != nil {
		setupLog.Error(err, "unable to create event recorder")
		os.Exit(1)
	}

	if err = (&ocmrepository.Reconciler{
		BaseReconciler: &ocm.BaseReconciler{
			Client:        mgr.GetClient(),
			Scheme:        mgr.GetScheme(),
			EventRecorder: eventsRecorder,
		},
	}).SetupWithManager(ctx, mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "OCMRepository")
		os.Exit(1)
	}

	if err = (&component.Reconciler{
		BaseReconciler: &ocm.BaseReconciler{
			Client:        mgr.GetClient(),
			Scheme:        mgr.GetScheme(),
			EventRecorder: eventsRecorder,
		},
		Registry: registry,
	}).SetupWithManager(ctx, mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Component")
		os.Exit(1)
	}

	if err = (&resource.Reconciler{
		BaseReconciler: &ocm.BaseReconciler{
			Client:        mgr.GetClient(),
			Scheme:        mgr.GetScheme(),
			EventRecorder: eventsRecorder,
		},
		Registry: registry,
	}).SetupWithManager(ctx, mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Resource")
		os.Exit(1)
	}

	if err = (&localization.Reconciler{
		BaseReconciler: &ocm.BaseReconciler{
			Client:        mgr.GetClient(),
			Scheme:        mgr.GetScheme(),
			EventRecorder: eventsRecorder,
		},
		Registry:           registry,
		LocalizationClient: locclient.NewClientWithRegistry(mgr.GetClient(), registry, mgr.GetScheme()),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "LocalizedResource")
		os.Exit(1)
	}

	if err = (&configuration.Reconciler{
		BaseReconciler: &ocm.BaseReconciler{
			Client:        mgr.GetClient(),
			Scheme:        mgr.GetScheme(),
			EventRecorder: eventsRecorder,
		},
		Registry:     registry,
		ConfigClient: cfgclient.NewClientWithRegistry(mgr.GetClient(), registry, mgr.GetScheme()),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ConfiguredResource")
		os.Exit(1)
	}

	if err = (&replication.Reconciler{
		BaseReconciler: &ocm.BaseReconciler{
			Client:        mgr.GetClient(),
			Scheme:        mgr.GetScheme(),
			EventRecorder: eventsRecorder,
		},
	}).SetupWithManager(ctx, mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Replication")
		os.Exit(1)
	}

	// +kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	go func() {
		// Block until our controller manager is elected leader. We presume our
		// entire process will terminate if we lose leadership, so we don't need
		// to handle that.
		<-mgr.Elected()
	}()

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func getHTTPClientWithTLS(rootCAFile string) (*http.Client, error) {
	var c []byte
	var err error
	if c, err = os.ReadFile(rootCAFile); err != nil {
		return nil, err
	}

	rootCAs, err := x509.SystemCertPool()
	if err != nil {
		return nil, err
	}

	if ok := rootCAs.AppendCertsFromPEM(c); !ok {
		return nil, errors.New("failed to append root CA certificate to pool")
	}

	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
				RootCAs:    rootCAs,
			},
		},
	}, nil
}

func configureRegistryAccess(ctx context.Context, params *RegistryParams) (*ociartifact.Registry, error) {
	registry, err := ociartifact.NewRegistry(params.RegistryAddr)
	if err != nil {
		return nil, err
	}
	registry.PlainHTTP = params.RegistryInsecureSkipVerify

	// If HTTPS is enabled, the root CA certificate must be configured.
	if !params.RegistryInsecureSkipVerify {
		if params.RootCA == "" {
			return nil, errors.New("rootCA is required when registry-insecure-skip-verify is false")
		}

		httpClient, err := getHTTPClientWithTLS(params.RootCA)
		if err != nil {
			return nil, err
		}

		registry.Client = httpClient
	}

	// Check if the registry is accessible.
	if err := checkIfRegistryAvailable(ctx, registry, params); err != nil {
		return nil, err
	}

	return registry, nil
}

func checkIfRegistryAvailable(ctx context.Context, registry *ociartifact.Registry, params *RegistryParams) error {
	timeoutChan := time.After(params.RegistryPingTimeout)
	for {
		err := registry.Ping(ctx)
		if err == nil {
			// Registry is there. Continue.
			return nil
		}

		select {
		case <-timeoutChan:
			// Timeout expired, registry not available.
			return err
		default:
			// Retry the ping after a brief delay.
			time.Sleep(params.RegistryPingInterval)
		}
	}
}
