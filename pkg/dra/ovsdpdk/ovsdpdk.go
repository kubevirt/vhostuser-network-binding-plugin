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

// Package ovsdpdk implements [driver.DRADriver] for the OVS-DPDK DRA driver
// (https://github.com/amorenoz/dra-driver-ovsdpdk).
//
// It reads device metadata via a [metadata.DRAMetadataProvider] and extracts
// the vhost-user socket path from the driver-specific attribute.
package ovsdpdk

import (
	"fmt"

	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/klog/v2"

	"kubevirt.io/vhostuser-network-binding-plugin/pkg/dra/driver"
	"kubevirt.io/vhostuser-network-binding-plugin/pkg/dra/metadata"
)

const (
	// DriverName is the DRA driver name used by the OVS-DPDK DRA driver.
	DriverName = "ovsdpdk.k8snetworkplumbingwg.io"

	// VhostPathKey is the device attribute key that the OVS-DPDK DRA driver
	// uses to publish the vhost-user socket path.
	VhostPathKey = "vhost-user-path"

	// MTUKey is the mandatory device attribute key for the MTU value.
	MTUKey = "mtu"

	// minMTU is the minimum valid MTU value according to RFC 791.
	minMTU = 68

	// maxMTU is the maximum valid MTU value according to OVS-DPDK
	// (see https://github.com/openvswitch/ovs/blob/b30f621502752463807af7bb8bcf3a0c763544cf/lib/netdev-dpdk.c#L3502-L3521)
	maxMTU = 9702
)

// OvsDpdkDriver implements [driver.DRADriver] for the OVS-DPDK DRA driver.
type OvsDpdkDriver struct {
	meta metadata.DRAMetadataProvider
}

// NewOvsDpdkDriver returns an OvsDpdkDriver backed by the given provider.
func NewOvsDpdkDriver(meta metadata.DRAMetadataProvider) *OvsDpdkDriver {
	return &OvsDpdkDriver{meta: meta}
}

// GetVhostMetadata implements [driver.DRADriver].  It reads the OVS-DPDK DRA
// driver metadata for the given ResourceClaimTemplate claim and request, and
// extracts the vhost-user metadata.
func (o *OvsDpdkDriver) GetVhostMetadata(claimName, requestName string) (driver.VhostMetadata, error) {
	dm, err := o.meta.Read(DriverName, claimName, requestName)
	if err != nil {
		return driver.VhostMetadata{}, fmt.Errorf("read OVS-DPDK DRA metadata for claim %q request %q: %w", claimName, requestName, err)
	}

	var result *driver.VhostMetadata
	for _, req := range dm.Requests {
		for _, dev := range req.Devices {
			// Mandatory attribute: vhost-user-path
			attr, ok := dev.Attributes[resourceapi.QualifiedName(VhostPathKey)]
			if !ok {
				continue
			}
			if attr.StringValue == nil {
				return driver.VhostMetadata{}, fmt.Errorf("attribute %q in claim %q request %q is not a string",
					VhostPathKey, claimName, requestName)
			}
			if result != nil {
				klog.Warningf("ovsdpdk: multiple devices carry attribute %q in claim %q request %q; using the first one",
					VhostPathKey, claimName, requestName)
				break
			}
			vhostPath := *attr.StringValue
			result = &driver.VhostMetadata{VhostPath: vhostPath}

			// Mandatory attribute: mtu
			attr, ok = dev.Attributes[resourceapi.QualifiedName(MTUKey)]
			if !ok {
				return driver.VhostMetadata{}, fmt.Errorf("mandatory attribute %q not found in claim %q request %q",
					MTUKey, claimName, requestName)
			}
			if attr.IntValue == nil {
				return driver.VhostMetadata{}, fmt.Errorf("attribute %q in claim %q request %q is not an integer",
					MTUKey, claimName, requestName)
			}
			if *attr.IntValue < minMTU {
				return driver.VhostMetadata{}, fmt.Errorf("attribute %q value %d in claim %q request %q is below minimum MTU of %d",
					MTUKey, *attr.IntValue, claimName, requestName, minMTU)
			}
			if *attr.IntValue > maxMTU {
				return driver.VhostMetadata{}, fmt.Errorf("attribute %q value %d in claim %q request %q is above maximum MTU of %d",
					MTUKey, *attr.IntValue, claimName, requestName, maxMTU)
			}
			result.MTU = uint(*attr.IntValue)
		}
	}
	if result == nil {
		return driver.VhostMetadata{}, fmt.Errorf("ovsdpdk: DRA metadata for %q/%q containing mandatory attribute %q not found. Have: %+v",
			claimName, requestName, VhostPathKey, dm)
	}
	klog.Infof("DRA metadata resolved: %s", result)
	return *result, nil
}
