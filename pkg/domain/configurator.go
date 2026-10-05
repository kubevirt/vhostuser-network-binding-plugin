/*
 * This file is part of the kubevirt project
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * Copyright the KubeVirt Authors.
 *
 */

package domain

import (
	"fmt"

	vmschema "kubevirt.io/api/core/v1"
	libvirtxml "libvirt.org/go/libvirtxml"

	"kubevirt.io/vhostuser-network-binding-plugin/pkg/dra/driver"
	"kubevirt.io/vhostuser-network-binding-plugin/pkg/utils"
)

const (
	// MaxQueueNum is the maximum number if virtqueues.
	MaxQueueNum = uint(256)
	// QueueSize is the default queue size.
	QueueSize = uint(1024)
)

type VhostUserInterface struct {
	VmiSpecIface *vmschema.Interface
	Metadata     driver.VhostMetadata
}

type VhostUserNetworkConfigurator struct {
	interfaces            []*VhostUserInterface
	queues                uint
	useVirtioTransitional bool
}

type ClaimInfo struct {
	ClaimName   string
	RequestName string
}

// NewVhostUserNetworkConfigurator creates a configurator for all vhost-user
// interfaces in the VMI.
func NewVhostUserNetworkConfigurator(
	vmi *vmschema.VirtualMachineInstance,
	draDriver driver.DRADriver,
	bindingPluginName string,
) (*VhostUserNetworkConfigurator, error) {

	vhostIfaces, err := getVhostUserInterfaces(vmi, draDriver, bindingPluginName)
	if err != nil {
		return nil, err
	}
	if len(vhostIfaces) == 0 {
		return nil, fmt.Errorf("no vhost interfaces found")
	}

	queues := computeQueues(vmi)
	useVirtioTransitional := vmi.Spec.Domain.Devices.UseVirtioTransitional != nil && *vmi.Spec.Domain.Devices.UseVirtioTransitional

	return &VhostUserNetworkConfigurator{
		interfaces:            vhostIfaces,
		queues:                queues,
		useVirtioTransitional: useVirtioTransitional,
	}, nil
}

// computeQueues returns the number of virtio queues to configure for vhost-user
// interfaces.
func computeQueues(vmi *vmschema.VirtualMachineInstance) uint {
	mq := vmi.Spec.Domain.Devices.NetworkInterfaceMultiQueue
	if mq == nil || !*mq {
		return 1
	}
	cpuSpec := vmi.Spec.Domain.CPU
	if cpuSpec == nil {
		return 1
	}

	cores := cpuSpec.Cores
	sockets := cpuSpec.Sockets
	threads := cpuSpec.Threads

	if cores == 0 {
		cores = 1
	}
	if sockets == 0 {
		sockets = 1
	}
	if threads == 0 {
		threads = 1
	}

	queues := uint(cores * sockets * threads)
	if queues > MaxQueueNum {
		return MaxQueueNum
	}
	return queues

}

// modelType returns the virtio model type string to use for the interface.
func (p VhostUserNetworkConfigurator) modelType() string {
	if p.useVirtioTransitional {
		return "virtio-transitional"
	}
	return "virtio"
}

func (p VhostUserNetworkConfigurator) Mutate(domain *libvirtxml.Domain) (*libvirtxml.Domain, error) {
	if domain.Devices == nil {
		domain.Devices = &libvirtxml.DomainDeviceList{}
	}

	for _, vhostIface := range p.interfaces {
		generatedIface, err := p.generateDomainInterface(vhostIface)
		if err != nil {
			return nil, fmt.Errorf("%s: failed to generate domain interface spec for iface: %v", vhostIface.VmiSpecIface.Name, err)
		}

		if iface := lookupIfaceByAliasName(domain.Devices.Interfaces, vhostIface.VmiSpecIface.Name); iface != nil {
			*iface = *generatedIface
		} else {
			domain.Devices.Interfaces = append(domain.Devices.Interfaces, *generatedIface)
		}
	}

	utils.EnsureSharedMemoryBacking(domain)

	return domain, nil
}

func (p VhostUserNetworkConfigurator) generateDomainInterface(vhostIface *VhostUserInterface) (*libvirtxml.DomainInterface, error) {
	// "dpdkvhostuser" ports are deprecated on OVS side. "server" mode is
	// generally preferred as it allows for OVS to restart and reconnect.
	mode := "server"

	domIface := &libvirtxml.DomainInterface{
		Alias: utils.NewUserDefinedAlias(vhostIface.VmiSpecIface.Name),
		Model: &libvirtxml.DomainInterfaceModel{Type: p.modelType()},
		Source: &libvirtxml.DomainInterfaceSource{
			VHostUser: &libvirtxml.DomainInterfaceSourceVHostUser{
				Chardev: &libvirtxml.DomainChardevSource{
					UNIX: &libvirtxml.DomainChardevSourceUNIX{
						Path: vhostIface.Metadata.VhostPath,
						Mode: mode,
					},
				},
			},
		},
		Driver: &libvirtxml.DomainInterfaceDriver{
			TXQueueSize: QueueSize,
			RXQueueSize: QueueSize,
			Queues:      p.queues,
		},
	}

	if vhostIface.VmiSpecIface.PciAddress != "" {
		pciAddr, err := utils.NewPCIAddress(vhostIface.VmiSpecIface.PciAddress)
		if err != nil {
			return nil, err
		}
		domIface.Address = &libvirtxml.DomainAddress{PCI: pciAddr}
	}

	if vhostIface.VmiSpecIface.MacAddress != "" {
		domIface.MAC = &libvirtxml.DomainInterfaceMAC{Address: vhostIface.VmiSpecIface.MacAddress}
	}

	if vhostIface.VmiSpecIface.ACPIIndex > 0 {
		domIface.ACPI = &libvirtxml.DomainDeviceACPI{Index: uint(vhostIface.VmiSpecIface.ACPIIndex)}
	}

	domIface.MTU = &libvirtxml.DomainInterfaceMTU{Size: vhostIface.Metadata.MTU}

	return domIface, nil
}

func lookupIfaceByAliasName(ifaces []libvirtxml.DomainInterface, name string) *libvirtxml.DomainInterface {
	for i, iface := range ifaces {
		if utils.AliasName(iface.Alias) == name {
			return &ifaces[i]
		}
	}
	return nil
}

func getVhostUserInterfaces(vmi *vmschema.VirtualMachineInstance, draDriver driver.DRADriver, bindingPluginName string) ([]*VhostUserInterface, error) {
	vhostIfaces := make([]*VhostUserInterface, 0)

	for i := range vmi.Spec.Domain.Devices.Interfaces {
		iface := &vmi.Spec.Domain.Devices.Interfaces[i]
		if iface.Binding == nil || iface.Binding.Name != bindingPluginName {
			continue
		}

		if iface.Model != "" && iface.Model != "virtio" {
			return nil, fmt.Errorf("interface %q: only virtio model supported, got %q", iface.Name, iface.Model)
		}

		claim, err := getClaimInfo(vmi, iface.Name)
		if err != nil {
			return nil, fmt.Errorf("interface %q: DRA claim: %w", iface.Name, err)
		}

		meta, err := draDriver.GetVhostMetadata(claim.ClaimName, claim.RequestName)
		if err != nil {
			return nil, fmt.Errorf("interface %q: DRA metadata: %w", iface.Name, err)
		}

		vhostIfaces = append(vhostIfaces, &VhostUserInterface{
			VmiSpecIface: iface,
			Metadata:     meta,
		})
	}
	return vhostIfaces, nil
}

func getClaimInfo(vmi *vmschema.VirtualMachineInstance, iface string) (*ClaimInfo, error) {
	for _, net := range vmi.Spec.Networks {
		if net.Name == iface {
			if net.ResourceClaim == nil {
				return nil, fmt.Errorf("network %q does not have ResourceClaim", iface)
			}
			return &ClaimInfo{
				ClaimName:   net.ResourceClaim.ClaimName,
				RequestName: net.ResourceClaim.RequestName,
			}, nil
		}
	}
	return nil, fmt.Errorf("no network found for interface %q", iface)
}
