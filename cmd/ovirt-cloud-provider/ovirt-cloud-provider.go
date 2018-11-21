package main

import (
	"fmt"
	"io"
	"errors"
	"gopkg.in/gcfg.v1"

	"github.com/golang/glog"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/kubernetes/pkg/cloudprovider"
	"k8s.io/kubernetes/pkg/controller"

	"context"
	"github.com/ovirt/ovirt-openshift-extensions/internal"
	"k8s.io/api/core/v1"
)

// ProviderName is the canonical name the plugin will register under. It must be different the the in-tree
// implementation name, "ovirt". The addition of "ecp" stand for External-Cloud-Provider
const ProviderName = "ovirt-ecp"
const DefaultVMSearchQuery = "vms?follow=nics&search="

type OvirtNode struct {
	UUID      string
	Name      string
	IPAddress string
}

type ProviderConfig struct {
	Filters struct {
		VmsQuery string `gcfg:"vmsquery"`
	}
}

type CloudProvider struct {
	VmsQuery string
	internal.OvirtApi
}

// init will register the cloud provider
func init() {
	glog.Info("about to register the ovirt cloud provider to the cluster")
	cloudprovider.RegisterCloudProvider(
		ProviderName,
		func(config io.Reader) (cloudprovider.Interface, error) {
			if config == nil {
				return nil, fmt.Errorf("missing configuration file for ovirt cloud provider")
			}
			ovirtClient, err := internal.NewOvirt(config)
			if err != nil {
				return nil, err
			}

			providerConfig := ProviderConfig{}
			err = gcfg.ReadInto(&providerConfig, config)
			if err != nil {
				return nil, err
			}
			return NewOvirtProvider(&providerConfig, ovirtClient)
		})
}

func NewOvirtProvider(providerConfig *ProviderConfig, ovirtApi internal.OvirtApi) (*CloudProvider, error) {
	// TODO consider some basic validations for the search query although it can be tricky
	if ovirtApi.GetConnectionDetails().Url == "" {
		return nil, errors.New("oVirt engine url is empty")
	}

	vmsQuery := DefaultVMSearchQuery + providerConfig.Filters.VmsQuery
	return &CloudProvider{vmsQuery, ovirtApi}, nil

}

// Initialize provides the cloud with a kubernetes client builder and may spawn goroutines
// to perform housekeeping activities within the cloud provider.
func (*CloudProvider) Initialize(clientBuilder controller.ControllerClientBuilder) {

}

// LoadBalancer returns a balancer interface. Also returns true if the interface is supported, false otherwise.
func (*CloudProvider) LoadBalancer() (cloudprovider.LoadBalancer, bool) {
	return nil, false
}

// Instances returns an instances interface. Also returns true if the interface is supported, false otherwise.
func (p *CloudProvider) Instances() (cloudprovider.Instances, bool) {
	return p, true
}

// Zones returns a zones interface. Also returns true if the interface is supported, false otherwise.
func (*CloudProvider) Zones() (cloudprovider.Zones, bool) {
	return nil, false

}

// NodeAddressses returns an hostnames/external-ips of the calling node
// TODO how to detect a primary external IP? how to pass hostnames if we have it?
func (p *CloudProvider) NodeAddresses(context context.Context, name types.NodeName) ([]v1.NodeAddress, error) {
	vms, err := p.getVms()
	if err != nil {
		return nil, err
	}

	var vm = vms[string(name)]
	if vm.Id == "" {
		return nil, fmt.Errorf(
			"VM by the name %s does not exist."+
				" The VM may have been removed, or the search query criteria needs correction",
			name)
	}

	// TODO the old provider supplied hostnames - look for fqdn of VM maybe?. Consider implementing.
	addresses := extractNodeAddresses(vm)
	return addresses, nil
}

// extractNodeAddresses will return all addresses of the reported node
// TODO how to detect a primary external IP? how to pass hostnames if we have it?
func extractNodeAddresses(vm internal.VM) []v1.NodeAddress {
	addresses := make([]v1.NodeAddress,0)
	for _, nics := range vm.Nics.Nics {
		for _, dev := range nics.Devices.Devices {
			for _, ip := range dev.Ips.Ips {
				addresses = append(addresses, v1.NodeAddress{Address: ip.Address, Type:v1.NodeExternalIP})
			}
		}
	}
	return addresses
}

func (p *CloudProvider) InstanceID(context context.Context, nodeName types.NodeName) (string, error) {
	vms, err := p.getVms()
	return vms[string(nodeName)].Id, err
}

func (p *CloudProvider) getVms() (map[string]internal.VM, error) {
	vms, err := p.GetVMs(p.VmsQuery)

	var vmsMap = make(map[string]internal.VM, len(vms))
	for _, v := range vms {
		vmsMap[v.Name] = v
	}

	return vmsMap, err
}

// Clusters returns a clusters interface.  Also returns true if the interface is supported, false otherwise.
func (*CloudProvider) Clusters() (cloudprovider.Clusters, bool) {
	return nil, false
}

// Routes returns a routes interface along with whether the interface is supported.
func (*CloudProvider) Routes() (cloudprovider.Routes, bool) {
	return nil, false
}

// ProviderName returns the cloud provider ID.
func (*CloudProvider) ProviderName() string {
	return ProviderName
}

// ScrubDNS provides an opportunity for cloud-provider-specific code to process DNS settings for pods.
func (*CloudProvider) ScrubDNS(nameservers, searches []string) (nsOut, srchOut []string) {
	return nil, nil

}

// HasClusterID returns true if a ClusterID is required and set
func (*CloudProvider) HasClusterID() bool {
	return false
}

func (*CloudProvider) AddSSHKeyToAllInstances(context context.Context, user string, keyData []byte) error {
	return errors.New("NotImplemented")
}

func (*CloudProvider) CurrentNodeName(context context.Context, hostname string) (types.NodeName, error) {
	//var r types.NodeName = ""
	return types.NodeName(hostname), nil
}

// ExternalID returns the cloud provider ID of the node with the specified NodeName.
// Note that if the instance does not exist or is no longer running, we must return ("", cloudprovider.InstanceNotFound)
func (p *CloudProvider) ExternalID(nodeName types.NodeName) (string, error) {
	vms, err := p.getVms()
	if err != nil || vms[string(nodeName)].Id == "" {

	}
	return vms[string(nodeName)].Id, nil
}

// InstanceExistsByProviderID returns true if the instance for the given provider id still is running.
// If false is returned with no error, the instance will be immediately deleted by the cloud controller manager.
func (p *CloudProvider) InstanceExistsByProviderID(context context.Context, providerID string) (bool, error) {
	vms, err := p.getVms()
	if err != nil {

	}
	for _, v := range vms {
		// statuses of up, unknown, not-responding still most likely indicate a running
		// instance. First lets consider 'down' as the non existing instance.
		if v.Id == providerID {
			if v.Status == "down" {
				return false, nil
			} else {
				return true, nil
			}
		}
	}
	return false, fmt.Errorf("there is no instance with ID %s", providerID)
}

func (p *CloudProvider) InstanceShutdownByProviderID(context context.Context, providerID string) (bool, error) {
	// TODO implement
	return false, nil
}

// InstanceType returns the type of the specified instance.
func (p *CloudProvider) InstanceType(context context.Context, name types.NodeName) (string, error) {
	return ProviderName, nil
}

// InstanceTypeByProviderID returns the type of the specified instance.
func (p *CloudProvider) InstanceTypeByProviderID(context context.Context, providerID string) (string, error) {
	return "", cloudprovider.NotImplemented
}

func (p *CloudProvider) NodeAddressesByProviderID(context context.Context, providerID string) ([]v1.NodeAddress, error) {
	return []v1.NodeAddress{}, cloudprovider.NotImplemented
}
