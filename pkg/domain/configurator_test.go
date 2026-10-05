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

package domain_test

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	vmschema "kubevirt.io/api/core/v1"
	libvirtxml "libvirt.org/go/libvirtxml"

	"kubevirt.io/vhostuser-network-binding-plugin/pkg/domain"
	"kubevirt.io/vhostuser-network-binding-plugin/pkg/dra/driver"
	"kubevirt.io/vhostuser-network-binding-plugin/pkg/utils"
)

// mockDRADriver implements driver.DRADriver for testing.
type mockDRADriver struct {
	// metadata maps claimName to VhostMetadata.
	metadata map[string]driver.VhostMetadata
	err      error
}

func (m mockDRADriver) GetVhostMetadata(claimName, _ string) (driver.VhostMetadata, error) {
	if m.err != nil {
		return driver.VhostMetadata{}, m.err
	}
	meta, ok := m.metadata[claimName]
	if !ok {
		return driver.VhostMetadata{}, fmt.Errorf("no metadata for claim %q", claimName)
	}
	return meta, nil
}

// vhostIface returns a vhostuser-bound Interface with the given name and optional mutators.
func vhostIface(name string) vmschema.Interface {
	return vmschema.Interface{Name: name, Binding: &vmschema.PluginBinding{Name: "vhostuser"}}
}

// draNetwork returns a Network with a ResourceClaim source.
func draNetwork(ifaceName, claimName, requestName string) vmschema.Network {
	return vmschema.Network{
		Name: ifaceName,
		NetworkSource: vmschema.NetworkSource{
			ResourceClaim: &vmschema.ClaimRequest{
				ClaimName:   claimName,
				RequestName: requestName,
			},
		},
	}
}

// buildVMI constructs a minimal VMI from the given interfaces and networks.
func buildVMI(ifaces []vmschema.Interface, networks []vmschema.Network) *vmschema.VirtualMachineInstance {
	return &vmschema.VirtualMachineInstance{
		Spec: vmschema.VirtualMachineInstanceSpec{
			Domain: vmschema.DomainSpec{
				Devices: vmschema.Devices{
					Interfaces: ifaces,
				},
			},
			Networks: networks,
		},
	}
}

type claimMetadata struct {
	name     string
	metadata driver.VhostMetadata
}

func claimMeta(name string) claimMetadata {
	return claimMetadata{
		name: name,
		metadata: driver.VhostMetadata{
			VhostPath: fmt.Sprintf("/var/run/vhost/%s.sock", name),
			MTU:       1500,
		},
	}
}

func (c claimMetadata) WithMTU(mtu uint) claimMetadata {
	c.metadata.MTU = mtu
	return c
}

func mockDriver(claims ...claimMetadata) mockDRADriver {
	metadata := make(map[string]driver.VhostMetadata, len(claims))
	for _, c := range claims {
		metadata[c.name] = c.metadata
	}
	return mockDRADriver{metadata: metadata}
}

// vhostSource builds the expected libvirtxml vhost-user source for a given socket path.
func vhostSource(path string) *libvirtxml.DomainInterfaceSource {
	return &libvirtxml.DomainInterfaceSource{
		VHostUser: &libvirtxml.DomainInterfaceSourceVHostUser{
			Chardev: &libvirtxml.DomainChardevSource{
				UNIX: &libvirtxml.DomainChardevSourceUNIX{
					Path: path,
					Mode: "server",
				},
			},
		},
	}
}

// ifaceDriver returns the expected DomainInterfaceDriver for a vhost-user interface.
func ifaceDriver() *libvirtxml.DomainInterfaceDriver {
	return &libvirtxml.DomainInterfaceDriver{
		TXQueueSize: domain.QueueSize,
		RXQueueSize: domain.QueueSize,
		Queues:      1,
	}
}

// pciAddr is a helper to build a DomainAddressPCI from four uint values.
func pciAddr(dom, bus, slot, fn uint) *libvirtxml.DomainAddressPCI {
	return &libvirtxml.DomainAddressPCI{
		Domain:   &dom,
		Bus:      &bus,
		Slot:     &slot,
		Function: &fn,
	}
}

var _ = Describe("vhostuser network configurator", func() {
	Context("constructor", func() {
		DescribeTable("should fail given",
			func(ifaces []vmschema.Interface, networks []vmschema.Network, drv driver.DRADriver) {
				vmi := buildVMI(ifaces, networks)
				_, err := domain.NewVhostUserNetworkConfigurator(vmi, drv, "vhostuser")
				Expect(err).To(HaveOccurred())
			},
			Entry("no interfaces",
				nil,
				[]vmschema.Network{draNetwork("default", "default", "vhost-port")},
				mockDriver(claimMeta("default")),
			),
			Entry("interface with no vhostuser binding method",
				[]vmschema.Interface{{Name: "default", InterfaceBindingMethod: vmschema.InterfaceBindingMethod{Bridge: &vmschema.InterfaceBridge{}}}},
				[]vmschema.Network{draNetwork("default", "default", "vhost-port")},
				mockDriver(claimMeta("default")),
			),
			Entry("interface with no vhostuser binding plugin",
				[]vmschema.Interface{{Name: "default", Binding: &vmschema.PluginBinding{Name: "no-vhostuser"}}},
				[]vmschema.Network{draNetwork("default", "default", "vhost-port")},
				mockDriver(claimMeta("default")),
			),
			Entry("vhostuser interface with no matching network",
				[]vmschema.Interface{vhostIface("default")},
				nil,
				mockDriver(claimMeta("default")),
			),
			Entry("DRA driver returns an error",
				[]vmschema.Interface{vhostIface("default")},
				[]vmschema.Network{draNetwork("default", "default", "vhost-port")},
				mockDRADriver{err: fmt.Errorf("driver failure")},
			),
			Entry("unsupported interface model",
				[]vmschema.Interface{{Name: "default", Binding: &vmschema.PluginBinding{Name: "vhostuser"}, Model: "e1000"}},
				[]vmschema.Network{draNetwork("default", "default", "vhost-port")},
				mockDriver(claimMeta("default")),
			),
		)

		It("should fail given interface with invalid PCI address", func() {
			ifaces := []vmschema.Interface{{
				Name:       "default",
				Binding:    &vmschema.PluginBinding{Name: "vhostuser"},
				PciAddress: "invalid-pci-address",
			}}
			networks := []vmschema.Network{draNetwork("default", "default", "vhost-port")}
			vmi := buildVMI(ifaces, networks)

			testMutator, err := domain.NewVhostUserNetworkConfigurator(vmi, mockDriver(claimMeta("default")), "vhostuser")
			Expect(err).ToNot(HaveOccurred())

			_, err = testMutator.Mutate(&libvirtxml.Domain{})
			Expect(err).To(HaveOccurred())
		})

		It("should fail if the dynamic binding plugin name does not match", func() {
			ifaces := []vmschema.Interface{{
				Name:    "default",
				Binding: &vmschema.PluginBinding{Name: "vhostuser"},
			}}
			networks := []vmschema.Network{draNetwork("default", "default", "vhost-port")}
			vmi := buildVMI(ifaces, networks)

			_, err := domain.NewVhostUserNetworkConfigurator(vmi, mockDriver(claimMeta("default")), "different-binding-name")
			Expect(err).To(HaveOccurred())
		})

		DescribeTable("should accept interface with model",
			func(model string) {
				ifaces := []vmschema.Interface{{Name: "default", Binding: &vmschema.PluginBinding{Name: "vhostuser"}, Model: model}}
				networks := []vmschema.Network{draNetwork("default", "default", "vhost-port")}
				vmi := buildVMI(ifaces, networks)

				_, err := domain.NewVhostUserNetworkConfigurator(vmi, mockDriver(claimMeta("default")), "vhostuser")
				Expect(err).ToNot(HaveOccurred())
			},
			Entry("empty (default)", ""),
			Entry("virtio", "virtio"),
		)
	})

	Context("generate domain spec interface", func() {
		DescribeTable("should add interface to domain spec given iface with",
			func(iface vmschema.Interface, drv driver.DRADriver, expectedDomainIface libvirtxml.DomainInterface) {
				ifaces := []vmschema.Interface{iface}
				networks := []vmschema.Network{draNetwork(iface.Name, iface.Name, "vhost-port")}
				vmi := buildVMI(ifaces, networks)

				testMutator, err := domain.NewVhostUserNetworkConfigurator(vmi, drv, "vhostuser")
				Expect(err).ToNot(HaveOccurred())

				mutatedDomain, err := testMutator.Mutate(&libvirtxml.Domain{})
				Expect(err).ToNot(HaveOccurred())
				Expect(mutatedDomain.Devices.Interfaces).To(Equal([]libvirtxml.DomainInterface{expectedDomainIface}))
			},
			Entry("vhostuser binding plugin",
				vhostIface("default"),
				mockDriver(claimMeta("default")),
				libvirtxml.DomainInterface{
					Alias:  utils.NewUserDefinedAlias("default"),
					Model:  &libvirtxml.DomainInterfaceModel{Type: "virtio"},
					Source: vhostSource("/var/run/vhost/default.sock"),
					Driver: ifaceDriver(),
					MTU:    &libvirtxml.DomainInterfaceMTU{Size: 1500},
				},
			),
			Entry("PCI address",
				vmschema.Interface{Name: "default", Binding: &vmschema.PluginBinding{Name: "vhostuser"}, PciAddress: "0000:02:02.0"},
				mockDriver(claimMeta("default")),
				libvirtxml.DomainInterface{
					Alias:   utils.NewUserDefinedAlias("default"),
					Model:   &libvirtxml.DomainInterfaceModel{Type: "virtio"},
					Source:  vhostSource("/var/run/vhost/default.sock"),
					Address: &libvirtxml.DomainAddress{PCI: pciAddr(0, 2, 2, 0)},
					Driver:  ifaceDriver(),
					MTU:     &libvirtxml.DomainInterfaceMTU{Size: 1500},
				},
			),
			Entry("MAC address",
				vmschema.Interface{Name: "default", Binding: &vmschema.PluginBinding{Name: "vhostuser"}, MacAddress: "02:02:02:02:02:02"},
				mockDriver(claimMeta("default")),
				libvirtxml.DomainInterface{
					Alias:  utils.NewUserDefinedAlias("default"),
					Model:  &libvirtxml.DomainInterfaceModel{Type: "virtio"},
					Source: vhostSource("/var/run/vhost/default.sock"),
					MAC:    &libvirtxml.DomainInterfaceMAC{Address: "02:02:02:02:02:02"},
					Driver: ifaceDriver(),
					MTU:    &libvirtxml.DomainInterfaceMTU{Size: 1500},
				},
			),
			Entry("ACPI address",
				vmschema.Interface{Name: "default", Binding: &vmschema.PluginBinding{Name: "vhostuser"}, ACPIIndex: 2},
				mockDriver(claimMeta("default")),
				libvirtxml.DomainInterface{
					Alias:  utils.NewUserDefinedAlias("default"),
					Model:  &libvirtxml.DomainInterfaceModel{Type: "virtio"},
					Source: vhostSource("/var/run/vhost/default.sock"),
					ACPI:   &libvirtxml.DomainDeviceACPI{Index: uint(2)},
					Driver: ifaceDriver(),
					MTU:    &libvirtxml.DomainInterfaceMTU{Size: 1500},
				},
			),
			Entry("MTU set in device metadata",
				vhostIface("default"),
				mockDriver(claimMeta("default").WithMTU(9000)),
				libvirtxml.DomainInterface{
					Alias:  utils.NewUserDefinedAlias("default"),
					Model:  &libvirtxml.DomainInterfaceModel{Type: "virtio"},
					Source: vhostSource("/var/run/vhost/default.sock"),
					Driver: ifaceDriver(),
					MTU:    &libvirtxml.DomainInterfaceMTU{Size: 9000},
				},
			),
		)

		It("should set vhost-user source mode to server", func() {
			ifaces := []vmschema.Interface{vhostIface("default")}
			networks := []vmschema.Network{draNetwork("default", "default", "vhost-port")}
			vmi := buildVMI(ifaces, networks)

			testMutator, err := domain.NewVhostUserNetworkConfigurator(vmi, mockDriver(claimMeta("default")), "vhostuser")
			Expect(err).ToNot(HaveOccurred())

			mutatedDomain, err := testMutator.Mutate(&libvirtxml.Domain{})
			Expect(err).ToNot(HaveOccurred())
			Expect(mutatedDomain.Devices.Interfaces[0].Source.VHostUser.Chardev.UNIX.Mode).To(Equal("server"))
		})

		It("should not override other interfaces", func() {
			ifaces := []vmschema.Interface{
				vhostIface("default"),
				{Name: "secondary", InterfaceBindingMethod: vmschema.InterfaceBindingMethod{Bridge: &vmschema.InterfaceBridge{}}},
			}
			networks := []vmschema.Network{
				draNetwork("default", "default", "vhost-port"),
				{Name: "secondary", NetworkSource: vmschema.NetworkSource{Multus: &vmschema.MultusNetwork{NetworkName: "sec"}}},
			}
			vmi := buildVMI(ifaces, networks)

			testMutator, err := domain.NewVhostUserNetworkConfigurator(vmi, mockDriver(claimMeta("default")), "vhostuser")
			Expect(err).ToNot(HaveOccurred())

			existingIface := libvirtxml.DomainInterface{Alias: utils.NewUserDefinedAlias("existing-iface")}
			testDomain := &libvirtxml.Domain{
				Devices: &libvirtxml.DomainDeviceList{
					Interfaces: []libvirtxml.DomainInterface{existingIface},
				},
			}

			mutatedDomain, err := testMutator.Mutate(testDomain)
			Expect(err).ToNot(HaveOccurred())
			Expect(mutatedDomain.Devices.Interfaces).To(Equal([]libvirtxml.DomainInterface{
				existingIface,
				{
					Alias:  utils.NewUserDefinedAlias("default"),
					Model:  &libvirtxml.DomainInterfaceModel{Type: "virtio"},
					Source: vhostSource("/var/run/vhost/default.sock"),
					Driver: ifaceDriver(),
					MTU:    &libvirtxml.DomainInterfaceMTU{Size: 1500},
				},
			}))
		})

		It("should set domain interface correctly when executed more than once", func() {
			ifaces := []vmschema.Interface{vhostIface("default")}
			networks := []vmschema.Network{draNetwork("default", "default", "vhost-port")}
			vmi := buildVMI(ifaces, networks)

			testMutator, err := domain.NewVhostUserNetworkConfigurator(vmi, mockDriver(claimMeta("default")), "vhostuser")
			Expect(err).ToNot(HaveOccurred())

			mutatedDomain, err := testMutator.Mutate(&libvirtxml.Domain{})
			Expect(err).ToNot(HaveOccurred())

			mutatedAgain, err := testMutator.Mutate(mutatedDomain)
			Expect(err).ToNot(HaveOccurred())
			Expect(mutatedAgain).To(Equal(mutatedDomain))
		})

		It("should handle multiple vhostuser interfaces with distinct socket paths", func() {
			ifaces := []vmschema.Interface{
				vhostIface("default"),
				{Name: "net1", Binding: &vmschema.PluginBinding{Name: "vhostuser"}, MacAddress: "02:00:00:00:00:01"},
				{Name: "net2", Binding: &vmschema.PluginBinding{Name: "vhostuser"}, MacAddress: "02:00:00:00:00:02", PciAddress: "0000:03:00.0"},
			}
			networks := []vmschema.Network{
				draNetwork("default", "default", "vhost-port"),
				draNetwork("net1", "net1", "vhost-port"),
				draNetwork("net2", "net2", "vhost-port"),
			}
			vmi := buildVMI(ifaces, networks)

			testMutator, err := domain.NewVhostUserNetworkConfigurator(vmi,
				mockDriver(claimMeta("default"), claimMeta("net1"), claimMeta("net2")), "vhostuser",
			)
			Expect(err).ToNot(HaveOccurred())

			mutatedDomain, err := testMutator.Mutate(&libvirtxml.Domain{})
			Expect(err).ToNot(HaveOccurred())
			Expect(mutatedDomain.Devices.Interfaces).To(Equal([]libvirtxml.DomainInterface{
				{
					Alias:  utils.NewUserDefinedAlias("default"),
					Model:  &libvirtxml.DomainInterfaceModel{Type: "virtio"},
					Source: vhostSource("/var/run/vhost/default.sock"),
					Driver: ifaceDriver(),
					MTU:    &libvirtxml.DomainInterfaceMTU{Size: 1500},
				},
				{
					Alias:  utils.NewUserDefinedAlias("net1"),
					Model:  &libvirtxml.DomainInterfaceModel{Type: "virtio"},
					Source: vhostSource("/var/run/vhost/net1.sock"),
					MAC:    &libvirtxml.DomainInterfaceMAC{Address: "02:00:00:00:00:01"},
					Driver: ifaceDriver(),
					MTU:    &libvirtxml.DomainInterfaceMTU{Size: 1500},
				},
				{
					Alias:   utils.NewUserDefinedAlias("net2"),
					Model:   &libvirtxml.DomainInterfaceModel{Type: "virtio"},
					Source:  vhostSource("/var/run/vhost/net2.sock"),
					MAC:     &libvirtxml.DomainInterfaceMAC{Address: "02:00:00:00:00:02"},
					Address: &libvirtxml.DomainAddress{PCI: pciAddr(0, 3, 0, 0)},
					Driver:  ifaceDriver(),
					MTU:     &libvirtxml.DomainInterfaceMTU{Size: 1500},
				},
			}))
		})

		It("should replace existing interface with same name", func() {
			ifaces := []vmschema.Interface{vhostIface("default")}
			networks := []vmschema.Network{draNetwork("default", "default", "vhost-port")}
			vmi := buildVMI(ifaces, networks)

			testMutator, err := domain.NewVhostUserNetworkConfigurator(vmi, mockDriver(claimMeta("default")), "vhostuser")
			Expect(err).ToNot(HaveOccurred())

			existingIface := libvirtxml.DomainInterface{
				Alias: utils.NewUserDefinedAlias("default"),
				Source: &libvirtxml.DomainInterfaceSource{
					Bridge: &libvirtxml.DomainInterfaceSourceBridge{Bridge: "br0"},
				},
			}
			testDomain := &libvirtxml.Domain{
				Devices: &libvirtxml.DomainDeviceList{
					Interfaces: []libvirtxml.DomainInterface{existingIface},
				},
			}

			mutatedDomain, err := testMutator.Mutate(testDomain)
			Expect(err).ToNot(HaveOccurred())
			Expect(mutatedDomain.Devices.Interfaces).To(HaveLen(1))
			Expect(mutatedDomain.Devices.Interfaces[0]).To(Equal(libvirtxml.DomainInterface{
				Alias:  utils.NewUserDefinedAlias("default"),
				Model:  &libvirtxml.DomainInterfaceModel{Type: "virtio"},
				Source: vhostSource("/var/run/vhost/default.sock"),
				Driver: ifaceDriver(),
				MTU:    &libvirtxml.DomainInterfaceMTU{Size: 1500},
			}))
		})

		It("should handle mixed vhostuser and non-vhostuser interfaces", func() {
			ifaces := []vmschema.Interface{
				vhostIface("default"),
				{Name: "multus1", InterfaceBindingMethod: vmschema.InterfaceBindingMethod{Bridge: &vmschema.InterfaceBridge{}}},
				vhostIface("multus2"),
			}
			networks := []vmschema.Network{
				draNetwork("default", "default", "vhost-port"),
				{Name: "multus1", NetworkSource: vmschema.NetworkSource{Multus: &vmschema.MultusNetwork{NetworkName: "net1"}}},
				draNetwork("multus2", "multus2", "vhost-port"),
			}
			vmi := buildVMI(ifaces, networks)

			testMutator, err := domain.NewVhostUserNetworkConfigurator(vmi,
				mockDriver(claimMeta("default"), claimMeta("multus2")), "vhostuser",
			)
			Expect(err).ToNot(HaveOccurred())

			existingBridgeIface := libvirtxml.DomainInterface{
				Alias: utils.NewUserDefinedAlias("multus1"),
				Source: &libvirtxml.DomainInterfaceSource{
					Bridge: &libvirtxml.DomainInterfaceSourceBridge{Bridge: "br0"},
				},
			}
			testDomain := &libvirtxml.Domain{
				Devices: &libvirtxml.DomainDeviceList{
					Interfaces: []libvirtxml.DomainInterface{existingBridgeIface},
				},
			}

			mutatedDomain, err := testMutator.Mutate(testDomain)
			Expect(err).ToNot(HaveOccurred())
			Expect(mutatedDomain.Devices.Interfaces).To(HaveLen(3))
			Expect(mutatedDomain.Devices.Interfaces[0].Source.Bridge).ToNot(BeNil())
			Expect(utils.AliasName(mutatedDomain.Devices.Interfaces[0].Alias)).To(Equal("multus1"))
			for _, iface := range mutatedDomain.Devices.Interfaces[1:] {
				Expect(iface.Source.VHostUser).ToNot(BeNil())
				Expect(iface.Source.VHostUser.Chardev.UNIX.Mode).To(Equal("server"))
			}
		})

		It("should set memory backing to shared", func() {
			ifaces := []vmschema.Interface{vhostIface("default")}
			networks := []vmschema.Network{draNetwork("default", "default", "vhost-port")}
			vmi := buildVMI(ifaces, networks)

			testMutator, err := domain.NewVhostUserNetworkConfigurator(vmi, mockDriver(claimMeta("default")), "vhostuser")
			Expect(err).ToNot(HaveOccurred())

			mutatedDomain, err := testMutator.Mutate(&libvirtxml.Domain{})
			Expect(err).ToNot(HaveOccurred())
			Expect(mutatedDomain.MemoryBacking).ToNot(BeNil())
			Expect(mutatedDomain.MemoryBacking.MemoryAccess).ToNot(BeNil())
			Expect(mutatedDomain.MemoryBacking.MemoryAccess.Mode).To(Equal("shared"))
		})

		It("should set memory backing to shared even if it exists with a different mode", func() {
			ifaces := []vmschema.Interface{vhostIface("default")}
			networks := []vmschema.Network{draNetwork("default", "default", "vhost-port")}
			vmi := buildVMI(ifaces, networks)

			testMutator, err := domain.NewVhostUserNetworkConfigurator(vmi, mockDriver(claimMeta("default")), "vhostuser")
			Expect(err).ToNot(HaveOccurred())

			testDomain := &libvirtxml.Domain{
				MemoryBacking: &libvirtxml.DomainMemoryBacking{
					MemoryAccess: &libvirtxml.DomainMemoryAccess{Mode: "private"},
				},
			}

			mutatedDomain, err := testMutator.Mutate(testDomain)
			Expect(err).ToNot(HaveOccurred())
			Expect(mutatedDomain.MemoryBacking.MemoryAccess.Mode).To(Equal("shared"))
		})

		It("should set memory backing access when backing exists but access is nil", func() {
			ifaces := []vmschema.Interface{vhostIface("default")}
			networks := []vmschema.Network{draNetwork("default", "default", "vhost-port")}
			vmi := buildVMI(ifaces, networks)

			testMutator, err := domain.NewVhostUserNetworkConfigurator(vmi, mockDriver(claimMeta("default")), "vhostuser")
			Expect(err).ToNot(HaveOccurred())

			testDomain := &libvirtxml.Domain{
				MemoryBacking: &libvirtxml.DomainMemoryBacking{},
			}

			mutatedDomain, err := testMutator.Mutate(testDomain)
			Expect(err).ToNot(HaveOccurred())
			Expect(mutatedDomain.MemoryBacking.MemoryAccess).ToNot(BeNil())
			Expect(mutatedDomain.MemoryBacking.MemoryAccess.Mode).To(Equal("shared"))
		})

		It("should set queues to 1 when multiqueue is disabled", func() {
			ifaces := []vmschema.Interface{vhostIface("default")}
			networks := []vmschema.Network{draNetwork("default", "default", "vhost-port")}
			mqDisabled := false
			vmi := buildVMI(ifaces, networks)
			vmi.Spec.Domain.Devices.NetworkInterfaceMultiQueue = &mqDisabled
			vmi.Spec.Domain.CPU = &vmschema.CPU{Cores: 4, Sockets: 1, Threads: 1}

			testMutator, err := domain.NewVhostUserNetworkConfigurator(vmi, mockDriver(claimMeta("default")), "vhostuser")
			Expect(err).ToNot(HaveOccurred())

			mutatedDomain, err := testMutator.Mutate(&libvirtxml.Domain{})
			Expect(err).ToNot(HaveOccurred())
			Expect(mutatedDomain.Devices.Interfaces).To(HaveLen(1))
			Expect(mutatedDomain.Devices.Interfaces[0].Driver).ToNot(BeNil())
			Expect(mutatedDomain.Devices.Interfaces[0].Driver.Queues).To(Equal(uint(1)))
		})

		It("should set queues to number of vCPUs when multiqueue is enabled", func() {
			ifaces := []vmschema.Interface{vhostIface("default")}
			networks := []vmschema.Network{draNetwork("default", "default", "vhost-port")}
			mqEnabled := true
			vmi := buildVMI(ifaces, networks)
			vmi.Spec.Domain.Devices.NetworkInterfaceMultiQueue = &mqEnabled
			vmi.Spec.Domain.CPU = &vmschema.CPU{Cores: 2, Sockets: 2, Threads: 2}

			testMutator, err := domain.NewVhostUserNetworkConfigurator(vmi, mockDriver(claimMeta("default")), "vhostuser")
			Expect(err).ToNot(HaveOccurred())

			mutatedDomain, err := testMutator.Mutate(&libvirtxml.Domain{})
			Expect(err).ToNot(HaveOccurred())
			Expect(mutatedDomain.Devices.Interfaces).To(HaveLen(1))
			Expect(mutatedDomain.Devices.Interfaces[0].Driver).ToNot(BeNil())
			// 2 cores * 2 sockets * 2 threads = 8
			Expect(mutatedDomain.Devices.Interfaces[0].Driver.Queues).To(Equal(uint(8)))
		})

		It("should set queues to 1 when multiqueue is enabled but CPU spec is nil", func() {
			ifaces := []vmschema.Interface{vhostIface("default")}
			networks := []vmschema.Network{draNetwork("default", "default", "vhost-port")}
			mqEnabled := true
			vmi := buildVMI(ifaces, networks)
			vmi.Spec.Domain.Devices.NetworkInterfaceMultiQueue = &mqEnabled
			// CPU spec intentionally left nil

			testMutator, err := domain.NewVhostUserNetworkConfigurator(vmi, mockDriver(claimMeta("default")), "vhostuser")
			Expect(err).ToNot(HaveOccurred())

			mutatedDomain, err := testMutator.Mutate(&libvirtxml.Domain{})
			Expect(err).ToNot(HaveOccurred())
			Expect(mutatedDomain.Devices.Interfaces).To(HaveLen(1))
			Expect(mutatedDomain.Devices.Interfaces[0].Driver).ToNot(BeNil())
			Expect(mutatedDomain.Devices.Interfaces[0].Driver.Queues).To(Equal(uint(1)))
		})

		It("should default zero CPU fields to 1 when computing queues", func() {
			ifaces := []vmschema.Interface{vhostIface("default")}
			networks := []vmschema.Network{draNetwork("default", "default", "vhost-port")}
			mqEnabled := true
			vmi := buildVMI(ifaces, networks)
			vmi.Spec.Domain.Devices.NetworkInterfaceMultiQueue = &mqEnabled
			vmi.Spec.Domain.CPU = &vmschema.CPU{Cores: 4}

			testMutator, err := domain.NewVhostUserNetworkConfigurator(vmi, mockDriver(claimMeta("default")), "vhostuser")
			Expect(err).ToNot(HaveOccurred())

			mutatedDomain, err := testMutator.Mutate(&libvirtxml.Domain{})
			Expect(err).ToNot(HaveOccurred())
			Expect(mutatedDomain.Devices.Interfaces[0].Driver.Queues).To(Equal(uint(4)))
		})

		It("should cap queues at 256 when vCPU count exceeds the maximum", func() {
			ifaces := []vmschema.Interface{vhostIface("default")}
			networks := []vmschema.Network{draNetwork("default", "default", "vhost-port")}
			mqEnabled := true
			vmi := buildVMI(ifaces, networks)
			vmi.Spec.Domain.Devices.NetworkInterfaceMultiQueue = &mqEnabled
			// 128 cores * 4 sockets * 1 thread = 512
			vmi.Spec.Domain.CPU = &vmschema.CPU{Cores: 128, Sockets: 4, Threads: 1}

			testMutator, err := domain.NewVhostUserNetworkConfigurator(vmi, mockDriver(claimMeta("default")), "vhostuser")
			Expect(err).ToNot(HaveOccurred())

			mutatedDomain, err := testMutator.Mutate(&libvirtxml.Domain{})
			Expect(err).ToNot(HaveOccurred())
			Expect(mutatedDomain.Devices.Interfaces).To(HaveLen(1))
			Expect(mutatedDomain.Devices.Interfaces[0].Driver).ToNot(BeNil())
			Expect(mutatedDomain.Devices.Interfaces[0].Driver.Queues).To(Equal(uint(256)))
		})

		It("should set TX and RX queue sizes to 1024", func() {
			ifaces := []vmschema.Interface{vhostIface("default")}
			networks := []vmschema.Network{draNetwork("default", "default", "vhost-port")}
			vmi := buildVMI(ifaces, networks)

			testMutator, err := domain.NewVhostUserNetworkConfigurator(vmi, mockDriver(claimMeta("default")), "vhostuser")
			Expect(err).ToNot(HaveOccurred())

			mutatedDomain, err := testMutator.Mutate(&libvirtxml.Domain{})
			Expect(err).ToNot(HaveOccurred())
			Expect(mutatedDomain.Devices.Interfaces).To(HaveLen(1))
			drv := mutatedDomain.Devices.Interfaces[0].Driver
			Expect(drv).ToNot(BeNil())
			Expect(drv.TXQueueSize).To(Equal(domain.QueueSize))
			Expect(drv.RXQueueSize).To(Equal(domain.QueueSize))
		})

		It("should apply multiqueue to all interfaces", func() {
			ifaces := []vmschema.Interface{
				vhostIface("default"),
				vhostIface("secondary"),
			}
			networks := []vmschema.Network{
				draNetwork("default", "default", "vhost-port"),
				draNetwork("secondary", "secondary", "vhost-port"),
			}
			mqEnabled := true
			vmi := buildVMI(ifaces, networks)
			vmi.Spec.Domain.Devices.NetworkInterfaceMultiQueue = &mqEnabled
			vmi.Spec.Domain.CPU = &vmschema.CPU{Cores: 4, Sockets: 1, Threads: 1}

			testMutator, err := domain.NewVhostUserNetworkConfigurator(vmi,
				mockDriver(claimMeta("default"), claimMeta("secondary")), "vhostuser",
			)
			Expect(err).ToNot(HaveOccurred())

			mutatedDomain, err := testMutator.Mutate(&libvirtxml.Domain{})
			Expect(err).ToNot(HaveOccurred())
			Expect(mutatedDomain.Devices.Interfaces).To(HaveLen(2))
			for _, iface := range mutatedDomain.Devices.Interfaces {
				Expect(iface.Driver).ToNot(BeNil())
				Expect(iface.Driver.Queues).To(Equal(uint(4)))
			}
		})

		It("should use virtio model when UseVirtioTransitional is false", func() {
			ifaces := []vmschema.Interface{vhostIface("default")}
			networks := []vmschema.Network{draNetwork("default", "default", "vhost-port")}
			uvt := false
			vmi := buildVMI(ifaces, networks)
			vmi.Spec.Domain.Devices.UseVirtioTransitional = &uvt

			testMutator, err := domain.NewVhostUserNetworkConfigurator(vmi, mockDriver(claimMeta("default")), "vhostuser")
			Expect(err).ToNot(HaveOccurred())

			mutatedDomain, err := testMutator.Mutate(&libvirtxml.Domain{})
			Expect(err).ToNot(HaveOccurred())
			Expect(mutatedDomain.Devices.Interfaces[0].Model).ToNot(BeNil())
			Expect(mutatedDomain.Devices.Interfaces[0].Model.Type).To(Equal("virtio"))
		})

		It("should use virtio-transitional model when UseVirtioTransitional is true", func() {
			ifaces := []vmschema.Interface{vhostIface("default")}
			networks := []vmschema.Network{draNetwork("default", "default", "vhost-port")}
			uvt := true
			vmi := buildVMI(ifaces, networks)
			vmi.Spec.Domain.Devices.UseVirtioTransitional = &uvt

			testMutator, err := domain.NewVhostUserNetworkConfigurator(vmi, mockDriver(claimMeta("default")), "vhostuser")
			Expect(err).ToNot(HaveOccurred())

			mutatedDomain, err := testMutator.Mutate(&libvirtxml.Domain{})
			Expect(err).ToNot(HaveOccurred())
			Expect(mutatedDomain.Devices.Interfaces[0].Model).ToNot(BeNil())
			Expect(mutatedDomain.Devices.Interfaces[0].Model.Type).To(Equal("virtio-transitional"))
		})

		It("should apply virtio-transitional to all vhost-user interfaces", func() {
			ifaces := []vmschema.Interface{
				vhostIface("default"),
				vhostIface("secondary"),
			}
			networks := []vmschema.Network{
				draNetwork("default", "default", "vhost-port"),
				draNetwork("secondary", "secondary", "vhost-port"),
			}
			uvt := true
			vmi := buildVMI(ifaces, networks)
			vmi.Spec.Domain.Devices.UseVirtioTransitional = &uvt

			testMutator, err := domain.NewVhostUserNetworkConfigurator(vmi,
				mockDriver(claimMeta("default"), claimMeta("secondary")), "vhostuser",
			)
			Expect(err).ToNot(HaveOccurred())

			mutatedDomain, err := testMutator.Mutate(&libvirtxml.Domain{})
			Expect(err).ToNot(HaveOccurred())
			Expect(mutatedDomain.Devices.Interfaces).To(HaveLen(2))
			for _, iface := range mutatedDomain.Devices.Interfaces {
				Expect(iface.Model).ToNot(BeNil())
				Expect(iface.Model.Type).To(Equal("virtio-transitional"))
			}
		})
	})
})
