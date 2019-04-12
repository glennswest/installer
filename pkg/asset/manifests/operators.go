// Package manifests deals with creating manifests for all manifests to be installed for the cluster
package manifests

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/ghodss/yaml"
	"github.com/pkg/errors"

	"github.com/openshift/installer/pkg/asset"
	"github.com/openshift/installer/pkg/asset/installconfig"
	"github.com/openshift/installer/pkg/asset/templates/content/bootkube"
	"github.com/openshift/installer/pkg/asset/tls"
	"github.com/openshift/installer/pkg/types"
)

const (
	manifestDir = "manifests"
)

var (
	kubeSysConfigPath = filepath.Join(manifestDir, "cluster-config.yaml")

	_ asset.WritableAsset = (*Manifests)(nil)

	customTmplFuncs = template.FuncMap{
		"indent": indent,
		"add": func(i, j int) int {
			return i + j
		},
	}
)

// Manifests generates the dependent operator config.yaml files
type Manifests struct {
	KubeSysConfig *configurationObject
	FileList      []*asset.File
}

type genericData map[string]string

// Name returns a human friendly name for the operator
func (m *Manifests) Name() string {
	return "Common Manifests"
}

// Dependencies returns all of the dependencies directly needed by a
// Manifests asset.
func (m *Manifests) Dependencies() []asset.Asset {
	return []asset.Asset{
		&installconfig.ClusterID{},
		&installconfig.InstallConfig{},
		&Ingress{},
		&DNS{},
		&Infrastructure{},
		&Networking{},
		&tls.RootCA{},
		&tls.EtcdCA{},
		&tls.EtcdSignerCertKey{},
		&tls.EtcdCABundle{},
		&tls.EtcdSignerClientCertKey{},
		&tls.EtcdClientCertKey{},
		&tls.EtcdMetricCABundle{},
		&tls.EtcdMetricSignerClientCertKey{},
		&tls.MCSCertKey{},

		&bootkube.CVOOverrides{},
		&bootkube.EtcdServiceKubeSystem{},
		&bootkube.HostEtcdServiceEndpointsKubeSystem{},
		&bootkube.HostEtcdServiceKubeSystem{},
		&bootkube.KubeCloudConfig{},
		&bootkube.KubeSystemConfigmapEtcdCA{},
		&bootkube.KubeSystemConfigmapEtcdServingCA{},
		&bootkube.KubeSystemConfigmapRootCA{},
		&bootkube.KubeSystemSecretEtcdClient{},
		&bootkube.KubeSystemSecretEtcdClientCADeprecated{},
		&bootkube.KubeSystemSecretEtcdSigner{},
		&bootkube.KubeSystemSecretEtcdSignerClient{},
		&bootkube.MachineConfigServerTLSSecret{},
		&bootkube.OpenshiftConfigConfigmapEtcdMetricServingCA{},
		&bootkube.OpenshiftConfigSecretEtcdMetricClient{},
		&bootkube.OpenshiftConfigSecretPullSecret{},
		&bootkube.OpenshiftMachineConfigOperator{},
		&bootkube.Pull{},
	}
}

// Generate generates the respective operator config.yml files
func (m *Manifests) Generate(dependencies asset.Parents) error {
	ingress := &Ingress{}
	dns := &DNS{}
	network := &Networking{}
	infra := &Infrastructure{}
	installConfig := &installconfig.InstallConfig{}
	dependencies.Get(installConfig, ingress, dns, network, infra)

	redactedConfig, err := redactedInstallConfig(*installConfig.Config)
	if err != nil {
		return errors.Wrap(err, "failed to redact install-config")
	}
	// mao go to kube-system config map
	m.KubeSysConfig = configMap("kube-system", "cluster-config-v1", genericData{
		"install-config": string(redactedConfig),
	})
	kubeSysConfigData, err := yaml.Marshal(m.KubeSysConfig)
	if err != nil {
		return errors.Wrap(err, "failed to create kube-system/cluster-config-v1 configmap")
	}

	m.FileList = []*asset.File{
		{
			Filename: kubeSysConfigPath,
			Data:     kubeSysConfigData,
		},
	}
	m.FileList = append(m.FileList, m.generateBootKubeManifests(dependencies)...)

	m.FileList = append(m.FileList, ingress.Files()...)
	m.FileList = append(m.FileList, dns.Files()...)
	m.FileList = append(m.FileList, network.Files()...)
	m.FileList = append(m.FileList, infra.Files()...)

	asset.SortFiles(m.FileList)

	return nil
}

// Files returns the files generated by the asset.
func (m *Manifests) Files() []*asset.File {
	return m.FileList
}

func (m *Manifests) generateBootKubeManifests(dependencies asset.Parents) []*asset.File {
	clusterID := &installconfig.ClusterID{}
	installConfig := &installconfig.InstallConfig{}
	etcdCA := &tls.EtcdCA{}
	mcsCertKey := &tls.MCSCertKey{}
	etcdClientCertKey := &tls.EtcdClientCertKey{}
	etcdMetricCABundle := &tls.EtcdMetricCABundle{}
	etcdMetricSignerClientCertKey := &tls.EtcdMetricSignerClientCertKey{}
	rootCA := &tls.RootCA{}
	etcdSignerCertKey := &tls.EtcdSignerCertKey{}
	etcdCABundle := &tls.EtcdCABundle{}
	etcdSignerClientCertKey := &tls.EtcdSignerClientCertKey{}
	dependencies.Get(
		clusterID,
		installConfig,
		etcdCA,
		etcdSignerCertKey,
		etcdCABundle,
		etcdSignerClientCertKey,
		etcdClientCertKey,
		etcdMetricCABundle,
		etcdMetricSignerClientCertKey,
		mcsCertKey,
		rootCA,
	)

	etcdEndpointHostnames := make([]string, *installConfig.Config.ControlPlane.Replicas)
	for i := range etcdEndpointHostnames {
		etcdEndpointHostnames[i] = fmt.Sprintf("etcd-%d", i)
	}

	templateData := &bootkubeTemplateData{
		CVOClusterID:          clusterID.UUID,
		EtcdCaBundle:          base64.StdEncoding.EncodeToString(etcdCABundle.Cert()),
		EtcdCaCert:            string(etcdCA.Cert()),
		EtcdClientCaCert:      base64.StdEncoding.EncodeToString(etcdCA.Cert()),
		EtcdClientCaKey:       base64.StdEncoding.EncodeToString(etcdCA.Key()),
		EtcdClientCert:        base64.StdEncoding.EncodeToString(etcdClientCertKey.Cert()),
		EtcdClientKey:         base64.StdEncoding.EncodeToString(etcdClientCertKey.Key()),
		EtcdEndpointDNSSuffix: installConfig.Config.ClusterDomain(),
		EtcdEndpointHostnames: etcdEndpointHostnames,
		EtcdMetricCaCert:      string(etcdMetricCABundle.Cert()),
		EtcdMetricClientCert:  base64.StdEncoding.EncodeToString(etcdMetricSignerClientCertKey.Cert()),
		EtcdMetricClientKey:   base64.StdEncoding.EncodeToString(etcdMetricSignerClientCertKey.Key()),
		EtcdSignerCert:        base64.StdEncoding.EncodeToString(etcdSignerCertKey.Cert()),
		EtcdSignerClientCert:  base64.StdEncoding.EncodeToString(etcdSignerClientCertKey.Cert()),
		EtcdSignerClientKey:   base64.StdEncoding.EncodeToString(etcdSignerClientCertKey.Key()),
		EtcdSignerKey:         base64.StdEncoding.EncodeToString(etcdSignerCertKey.Key()),
		McsTLSCert:            base64.StdEncoding.EncodeToString(mcsCertKey.Cert()),
		McsTLSKey:             base64.StdEncoding.EncodeToString(mcsCertKey.Key()),
		PullSecretBase64:      base64.StdEncoding.EncodeToString([]byte(installConfig.Config.PullSecret)),
		RootCaCert:            string(rootCA.Cert()),
	}

	files := []*asset.File{}
	for _, a := range []asset.WritableAsset{
		&bootkube.CVOOverrides{},
		&bootkube.EtcdServiceKubeSystem{},
		&bootkube.HostEtcdServiceEndpointsKubeSystem{},
		&bootkube.HostEtcdServiceKubeSystem{},
		&bootkube.KubeCloudConfig{},
		&bootkube.KubeSystemConfigmapEtcdCA{},
		&bootkube.KubeSystemConfigmapEtcdServingCA{},
		&bootkube.KubeSystemConfigmapRootCA{},
		&bootkube.KubeSystemSecretEtcdClient{},
		&bootkube.KubeSystemSecretEtcdClientCADeprecated{},
		&bootkube.KubeSystemSecretEtcdSigner{},
		&bootkube.KubeSystemSecretEtcdSignerClient{},
		&bootkube.MachineConfigServerTLSSecret{},
		&bootkube.OpenshiftConfigConfigmapEtcdMetricServingCA{},
		&bootkube.OpenshiftConfigSecretEtcdMetricClient{},
		&bootkube.OpenshiftConfigSecretPullSecret{},
		&bootkube.OpenshiftMachineConfigOperator{},
		&bootkube.Pull{},
	} {
		dependencies.Get(a)
		for _, f := range a.Files() {
			files = append(files, &asset.File{
				Filename: filepath.Join(manifestDir, strings.TrimSuffix(filepath.Base(f.Filename), ".template")),
				Data:     applyTemplateData(f.Data, templateData),
			})
		}
	}
	return files
}

func applyTemplateData(data []byte, templateData interface{}) []byte {
	template := template.Must(template.New("template").Funcs(customTmplFuncs).Parse(string(data)))
	buf := &bytes.Buffer{}
	if err := template.Execute(buf, templateData); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// Load returns the manifests asset from disk.
func (m *Manifests) Load(f asset.FileFetcher) (bool, error) {
	fileList, err := f.FetchByPattern(filepath.Join(manifestDir, "*"))
	if err != nil {
		return false, err
	}
	if len(fileList) == 0 {
		return false, nil
	}

	kubeSysConfig := &configurationObject{}
	var found bool
	for _, file := range fileList {
		if file.Filename == kubeSysConfigPath {
			if err := yaml.Unmarshal(file.Data, kubeSysConfig); err != nil {
				return false, errors.Wrap(err, "failed to unmarshal cluster-config.yaml")
			}
			found = true
		}
	}

	if !found {
		return false, nil

	}

	m.FileList, m.KubeSysConfig = fileList, kubeSysConfig

	asset.SortFiles(m.FileList)

	return true, nil
}

func redactedInstallConfig(config types.InstallConfig) ([]byte, error) {
	config.PullSecret = ""
	if config.Platform.VSphere != nil {
		p := *config.Platform.VSphere
		p.Username = ""
		p.Password = ""
		config.Platform.VSphere = &p
	}
	return yaml.Marshal(config)
}

func indent(indention int, v string) string {
	newline := "\n" + strings.Repeat(" ", indention)
	return strings.Replace(v, "\n", newline, -1)
}
