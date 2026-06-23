// Copyright 2026 RunWhen
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/runwhen-contrib/vpa-provisioner/internal/exclusion"
	"github.com/runwhen-contrib/vpa-provisioner/internal/prerequisites"
	"github.com/runwhen-contrib/vpa-provisioner/internal/provisioner"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var version = "dev"

func main() {
	var (
		kubeconfig            = flag.String("kubeconfig", "", "Path to kubeconfig (defaults to in-cluster or $KUBECONFIG)")
		excludeNamespaces     = flag.String("exclude-namespaces", "kube-system", "Comma-separated namespaces to skip")
		configMapName         = flag.String("config-map-name", "vpa-provisioner-config", "ConfigMap containing exclusion policy YAML (empty disables)")
		configMapNamespace    = flag.String("config-map-namespace", envOrDefault("POD_NAMESPACE", "vpa-provisioner"), "Namespace of the exclusion ConfigMap")
		skipPrerequisiteCheck = flag.Bool("skip-prerequisite-check", false, "Skip cluster prerequisite validation (not recommended)")
		requireRecommender      = flag.Bool("require-recommender", false, "Fail startup if the VPA Recommender deployment is not detected")
		showVersion           = flag.Bool("version", false, "Print version and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	cfg, err := restConfig(*kubeconfig)
	if err != nil {
		slog.Error("unable to build kubeconfig", "error", err)
		os.Exit(1)
	}

	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		slog.Error("unable to create kubernetes client", "error", err)
		os.Exit(1)
	}

	dynamicClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		slog.Error("unable to create dynamic client", "error", err)
		os.Exit(1)
	}

	if !*skipPrerequisiteCheck {
		discoveryClient, err := discovery.NewDiscoveryClientForConfig(cfg)
		if err != nil {
			slog.Error("unable to create discovery client", "error", err)
			os.Exit(1)
		}
		checker := prerequisites.NewChecker(
			clientset,
			dynamicClient,
			discoveryClient,
			prerequisites.Config{RequireRecommender: *requireRecommender},
		)
		if err := checker.Verify(context.Background()); err != nil {
			slog.Error("cluster prerequisites not satisfied", "error", err)
			os.Exit(1)
		}
	} else {
		slog.Warn("skipping cluster prerequisite validation")
	}

	baseExclusion := exclusion.ParseNamespaces(*excludeNamespaces)
	controller := provisioner.NewController(clientset, dynamicClient, provisioner.Config{
		BaseExclusion:      baseExclusion,
		ConfigMapName:      *configMapName,
		ConfigMapNamespace: *configMapNamespace,
	})

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	slog.Info("starting vpa-provisioner",
		"version", version,
		"exclude_namespaces", *excludeNamespaces,
		"config_map", fmt.Sprintf("%s/%s", *configMapNamespace, *configMapName),
	)
	if err := controller.Run(ctx, clientset); err != nil && ctx.Err() == nil {
		slog.Error("controller exited with error", "error", err)
		os.Exit(1)
	}
}

func restConfig(kubeconfigPath string) (*rest.Config, error) {
	if kubeconfigPath != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	}
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, nil
	}
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	configOverrides := &clientcmd.ConfigOverrides{}
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides).ClientConfig()
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
