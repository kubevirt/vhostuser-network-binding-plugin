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

package ovsdpdk_test

import (
	"fmt"
	"math"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	resourceapi "k8s.io/api/resource/v1"
	drametadata "k8s.io/dynamic-resource-allocation/api/metadata"

	"kubevirt.io/vhostuser-network-binding-plugin/pkg/dra/ovsdpdk"
)

// mockProvider implements metadata.DRAMetadataProvider for testing.
type mockProvider struct {
	dm             *drametadata.DeviceMetadata
	err            error
	capturedDriver string
}

func (m *mockProvider) Read(driverName, _, _ string) (*drametadata.DeviceMetadata, error) {
	m.capturedDriver = driverName
	return m.dm, m.err
}

// deviceMetadata builds a minimal DeviceMetadata with the given attributes.
func deviceMetadata(attrs map[resourceapi.QualifiedName]resourceapi.DeviceAttribute) *drametadata.DeviceMetadata {
	return &drametadata.DeviceMetadata{
		Requests: []drametadata.DeviceMetadataRequest{
			{
				Name: "vhost-port",
				Devices: []drametadata.Device{
					{
						Driver:     ovsdpdk.DriverName,
						Pool:       "node-0",
						Name:       "dev0",
						Attributes: attrs,
					},
				},
			},
		},
	}
}

func ovsDeviceMetadata(vhostPath string, mtu int64) *drametadata.DeviceMetadata {
	return deviceMetadata(map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
		resourceapi.QualifiedName(ovsdpdk.VhostPathKey): {StringValue: &vhostPath},
		resourceapi.QualifiedName(ovsdpdk.MTUKey):       {IntValue: &mtu},
	})
}

var _ = Describe("OvsDpdkDriver", func() {
	const (
		claimName   = "net1"
		requestName = "vhost-port"
		vhostPath   = "/var/run/ovsdpdk/vhost-user/net1/vhost.sock"
	)

	Describe("GetVhostMetadata", func() {
		Context("when the provider returns metadata with the vhost-user-path attribute", func() {
			It("returns VhostMetadata with the correct path", func() {
				provider := &mockProvider{dm: ovsDeviceMetadata(vhostPath, 1500)}
				d := ovsdpdk.NewOvsDpdkDriver(provider)

				result, err := d.GetVhostMetadata(claimName, requestName)
				Expect(err).ToNot(HaveOccurred())
				Expect(result.VhostPath).To(Equal(vhostPath))
			})

			It("passes the OVS-DPDK driver name to the provider", func() {
				provider := &mockProvider{dm: ovsDeviceMetadata(vhostPath, 1500)}
				d := ovsdpdk.NewOvsDpdkDriver(provider)

				_, err := d.GetVhostMetadata(claimName, requestName)
				Expect(err).ToNot(HaveOccurred())
				Expect(provider.capturedDriver).To(Equal(ovsdpdk.DriverName))
			})
		})

		Context("when the provider returns an error", func() {
			It("propagates the error", func() {
				provider := &mockProvider{err: fmt.Errorf("metadata not found")}
				d := ovsdpdk.NewOvsDpdkDriver(provider)

				_, err := d.GetVhostMetadata(claimName, requestName)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("metadata not found"))
			})
		})

		Context("when the metadata contains no vhost-user-path attribute", func() {
			It("returns an error mentioning the attribute key and claim", func() {
				provider := &mockProvider{dm: deviceMetadata(map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
					"some-other-key": {StringValue: new("some-value")},
				})}
				d := ovsdpdk.NewOvsDpdkDriver(provider)

				_, err := d.GetVhostMetadata(claimName, requestName)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(ovsdpdk.VhostPathKey))
				Expect(err.Error()).To(ContainSubstring(claimName))
			})
		})

		Context("when the vhost-user-path attribute is not a string", func() {
			It("returns an error mentioning the attribute key", func() {
				intVal := int64(42)
				dm := &drametadata.DeviceMetadata{
					Requests: []drametadata.DeviceMetadataRequest{
						{
							Name: requestName,
							Devices: []drametadata.Device{
								{
									Attributes: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
										resourceapi.QualifiedName(ovsdpdk.VhostPathKey): {IntValue: &intVal},
									},
								},
							},
						},
					},
				}
				provider := &mockProvider{dm: dm}
				d := ovsdpdk.NewOvsDpdkDriver(provider)

				_, err := d.GetVhostMetadata(claimName, requestName)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(ovsdpdk.VhostPathKey))
				Expect(err.Error()).To(ContainSubstring("not a string"))
			})
		})

		Context("mtu attribute", func() {
			It("sets MTU in VhostMetadata when the attribute is present and valid", func() {
				provider := &mockProvider{dm: ovsDeviceMetadata(vhostPath, 1500)}
				d := ovsdpdk.NewOvsDpdkDriver(provider)

				result, err := d.GetVhostMetadata(claimName, requestName)
				Expect(err).ToNot(HaveOccurred())
				Expect(result.MTU).To(Equal(uint(1500)))
			})

			It("accepts the minimum valid MTU of 68", func() {
				provider := &mockProvider{dm: ovsDeviceMetadata(vhostPath, 68)}
				d := ovsdpdk.NewOvsDpdkDriver(provider)

				result, err := d.GetVhostMetadata(claimName, requestName)
				Expect(err).ToNot(HaveOccurred())
				Expect(result.MTU).To(Equal(uint(68)))
			})

			It("accepts the maximum valid MTU of 9702", func() {
				provider := &mockProvider{dm: ovsDeviceMetadata(vhostPath, 9702)}
				d := ovsdpdk.NewOvsDpdkDriver(provider)

				result, err := d.GetVhostMetadata(claimName, requestName)
				Expect(err).ToNot(HaveOccurred())
				Expect(result.MTU).To(Equal(uint(9702)))
			})

			It("returns an error when the mtu attribute is absent", func() {
				provider := &mockProvider{dm: deviceMetadata(map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
					resourceapi.QualifiedName(ovsdpdk.VhostPathKey): {StringValue: new(vhostPath)},
				})}
				d := ovsdpdk.NewOvsDpdkDriver(provider)

				_, err := d.GetVhostMetadata(claimName, requestName)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(ovsdpdk.MTUKey))
			})

			It("returns an error when the mtu attribute is not an integer", func() {
				provider := &mockProvider{dm: deviceMetadata(map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
					resourceapi.QualifiedName(ovsdpdk.VhostPathKey): {StringValue: new(vhostPath)},
					resourceapi.QualifiedName(ovsdpdk.MTUKey):       {StringValue: new("1500")},
				})}
				d := ovsdpdk.NewOvsDpdkDriver(provider)

				_, err := d.GetVhostMetadata(claimName, requestName)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(ovsdpdk.MTUKey))
				Expect(err.Error()).To(ContainSubstring("not an integer"))
			})

			DescribeTable("returns an error when the mtu value is below the minimum",
				func(mtu int64) {
					provider := &mockProvider{dm: ovsDeviceMetadata(vhostPath, mtu)}
					d := ovsdpdk.NewOvsDpdkDriver(provider)

					_, err := d.GetVhostMetadata(claimName, requestName)
					Expect(err).To(HaveOccurred())
					Expect(err.Error()).To(ContainSubstring(ovsdpdk.MTUKey))
					Expect(err.Error()).To(ContainSubstring("below minimum"))
				},
				Entry("negative", int64(-1)),
				Entry("zero", int64(0)),
				Entry("below 68", int64(67)),
			)

			DescribeTable("returns an error when the mtu value is above the maximum",
				func(mtu int64) {
					provider := &mockProvider{dm: ovsDeviceMetadata(vhostPath, mtu)}
					d := ovsdpdk.NewOvsDpdkDriver(provider)

					_, err := d.GetVhostMetadata(claimName, requestName)
					Expect(err).To(HaveOccurred())
					Expect(err.Error()).To(ContainSubstring(ovsdpdk.MTUKey))
					Expect(err.Error()).To(ContainSubstring("above maximum"))
				},
				Entry("one-off", int64(9703)),
				Entry("above", int64(10000)),
				Entry("maxInt64", int64(math.MaxInt64)),
			)
		})
	})
})
