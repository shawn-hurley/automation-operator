package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"runtime"
	"strings"

	"github.com/automationbroker/automation-operator/pkg/crd"
	stub "github.com/automationbroker/automation-operator/pkg/handler"
	"github.com/automationbroker/bundle-lib/bundle"
	sdk "github.com/operator-framework/operator-sdk/pkg/sdk"
	k8sutil "github.com/operator-framework/operator-sdk/pkg/util/k8sutil"
	sdkVersion "github.com/operator-framework/operator-sdk/version"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	yaml "gopkg.in/yaml.v2"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/sirupsen/logrus"
)

func init() {
	pflag.Int("resync", 5, "time in seconds that the resources will be re synced")
	// TODO: Deal with config file after this initial attempt.
	pflag.String("configFile", "", "config file that should should be used. The config will override all other command line values")
	pflag.String("api-version", "", "Kubernetes apiVersion and has a format of $GROUP_NAME/$VERSION (e.g app.example.com/v1alpha1)")
	pflag.String("kind", "", "Kubernetes CustomResourceDefintion kind. (e.g AppService)")
	pflag.String("apb-image", "", "Apb Image path for which we can get the APB spec.")
	pflag.String("plan", "", "Plan that the operator should be interacting with for an APB.")
	pflag.Parse()
	viper.BindPFlags(pflag.CommandLine)
}

func printVersion() {
	logrus.Infof("Go Version: %s", runtime.Version())
	logrus.Infof("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH)
	logrus.Infof("operator-sdk Version: %v", sdkVersion.Version)
}

type apb struct {
	Version string `yaml:"api-version" json:"api-version"`
	Kind    string `yaml:"kind" json:"kind"`
	Image   string `yaml:"image" json:"image"`
	Plan    string `yaml:"plan" json:"plan"`
}

func main() {
	printVersion()

	var apbs []apb
	if c := viper.GetString("configFile"); c != "" {
		// Only use the config file.
		b, err := ioutil.ReadFile(c)
		if err != nil {
			logrus.Fatalf("Failed to get config file: %v", err)
		}
		viper.SetConfigType("yaml")
		err = viper.ReadConfig(bytes.NewBuffer(b))
		if err != nil {
			logrus.Fatalf("Failed to get config file: %v", err)
		}
		m := viper.GetStringMap("APBs")
		logrus.Infof("config output: %#v", m)
		for _, o := range m {
			d, err := json.Marshal(o)
			if err != nil {
				logrus.Fatalf("Failed to get config file: %v", err)
			}
			a := apb{}
			err = json.Unmarshal([]byte(d), &a)
			if err != nil {
				logrus.Fatalf("Failed to get config file: %v", err)
			}
			logrus.Infof("config output: %#v", a)
			apbs = append(apbs, a)
		}
	} else {
		apbs = append(apbs, apb{
			Version: viper.GetString("api-version"),
			Kind:    viper.GetString("kind"),
			Image:   viper.GetString("apb-image"),
			Plan:    viper.GetString("plan"),
		})
	}

	namespace, err := k8sutil.GetWatchNamespace()
	if err != nil {
		logrus.Fatalf("Failed to get watch namespace: %v", err)
	}
	resyncPeriod := viper.GetInt("resync")

	// Initialize Cluster Config.
	bundle.InitializeClusterConfig(bundle.ClusterConfig{
		PullPolicy:    "always",
		SandboxRole:   "admin",
		Namespace:     namespace,
		KeepNamespace: true,
	})

	m := map[string]crd.SpecPlan{}
	for _, a := range apbs {
		spec := getSpec(a.Image)
		//register type with gvk
		registerType(a.Version, a.Kind)

		logrus.Infof("Watching %s, %s, %s, %d", a.Version, a.Kind, namespace, resyncPeriod)
		sdk.Watch(a.Version, a.Kind, namespace, resyncPeriod)
		plan, ok := spec.GetPlan(a.Plan)
		if !ok {
			logrus.Errorf("unable to find plan: %v in the spec for apb-image: %v", viper.GetString("plan"), viper.GetString("apb-image"))
			os.Exit(2)
		}
		m[fmt.Sprintf("%v:%v", a.Version, a.Kind)] = crd.SpecPlan{
			Spec: spec,
			Plan: plan,
		}
	}
	sdk.Handle(stub.NewHandler(m))
	sdk.Run(context.TODO())
}

func registerType(resource, kind string) {
	gv := strings.Split(resource, "/")
	//	schemeGroupVersion := schema.GroupVersion{Group: gv[0], Version: gv[1]}
	schemeBuilder := k8sruntime.NewSchemeBuilder(func(s *k8sruntime.Scheme) error {
		s.AddKnownTypeWithName(schema.GroupVersionKind{
			Group:   gv[0],
			Version: gv[1],
			Kind:    kind,
		}, &unstructured.Unstructured{})
		return nil
	})
	k8sutil.AddToSDKScheme(schemeBuilder.AddToScheme)
}

func getSpec(image string) bundle.Spec {
	spec := `
version: 1.0
name: postgresql-apb
description: SCL PostgreSQL apb implementation
bindable: true
async: optional
tags:
  - database
  - postgresql
metadata:
  documentationUrl: https://www.postgresql.org/docs/
  longDescription: An apb that deploys postgresql 9.4, 9.5, or 9.6.
  dependencies:
    - 'registry.access.redhat.com/rhscl/postgresql-94-rhel7'
    - 'registry.access.redhat.com/rhscl/postgresql-95-rhel7'
    - 'registry.access.redhat.com/rhscl/postgresql-96-rhel7'
  displayName: PostgreSQL (APB)
  console.openshift.io/iconClass: icon-postgresql
  providerDisplayName: "Red Hat, Inc."
plans:
  - name: dev
    description: A single DB server with no storage
    free: true
    metadata:
      displayName: Development
      longDescription:
        This plan provides a single non-HA PostgreSQL server without
        persistent storage
      cost: $0.00
    parameters:
      - name: postgresql_database
        default: admin
        type: string
        title: PostgreSQL Database Name
        pattern: "^[a-zA-Z_][a-zA-Z0-9_]*$"
        required: true
      - name: postgresql_user
        default: admin
        title: PostgreSQL User
        type: string
        maxlength: 63
        pattern: "^[a-zA-Z_][a-zA-Z0-9_]*$"
        required: true
      - name: postgresql_password
        type: string
        title: PostgreSQL Password
        display_type: password
        pattern: "^[a-zA-Z0-9_~!@#$%^&*()-=<>,.?;:|]+$"
        required: true
      - name: postgresql_version
        default: '9.6'
        enum: ['9.6', '9.5', '9.4']
        type: enum
        title: PostgreSQL Version
        required: true
        updatable: true
    updates_to:
      - prod
  - name: prod
    description: A single DB server with persistent storage
    free: true
    metadata:
      displayName: Production
      longDescription:
        This plan provides a single non-HA PostgreSQL server with
        persistent storage
      cost: $0.00
    parameters:
      - name: postgresql_database
        default: admin
        type: string
        title: PostgreSQL Database Name
        pattern: "^[a-zA-Z_][a-zA-Z0-9_]*$"
        required: true
      - name: postgresql_user
        default: admin
        title: PostgreSQL User
        type: string
        maxlength: 63
        pattern: "^[a-zA-Z_][a-zA-Z0-9_]*$"
        required: true
      - name: postgresql_password
        type: string
        title: PostgreSQL Password
        display_type: password
        pattern: "^[a-zA-Z0-9_~!@#$%^&*()-=<>,.?;:|]+$"
        required: true
      - name: postgresql_version
        default: '9.6'
        enum: ['9.6', '9.5', '9.4']
        type: enum
        title: PostgreSQL Version
        required: true
        updatable: true
      - name: postgresql_volume_size
        type: enum
        default: '1Gi'
        enum: ['1Gi', '5Gi', '10Gi']
        title: PostgreSQL Volume Size
        required: true
    updates_to:
      - dev
`

	s := bundle.Spec{}
	err := yaml.Unmarshal([]byte(spec), &s)
	if err != nil {
		logrus.Infof("unable to get the spec - %v", err)
	}
	s.Image = image
	s.Runtime = 2
	return s
}
