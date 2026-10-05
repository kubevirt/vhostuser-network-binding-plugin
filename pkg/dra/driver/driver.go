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

// Package driver defines the vhost-user DRA driver abstraction.
package driver

import (
	"fmt"
)

// VhostMetadata holds the vhost-user socket and MTU information extracted from a DRA
// driver's device metadata.
type VhostMetadata struct {
	// VhostPath is the absolute in-container path to the vhost-user socket.
	VhostPath string
	// MTU is the MTU value to configure on the interface, as reported by the
	// DRA driver's device metadata.
	MTU uint
}

func (m VhostMetadata) String() string {
	return fmt.Sprintf("{VhostPath: %s, MTU: %d}", m.VhostPath, m.MTU)
}

// DRADriver extracts vhost-user socket information from a DRA driver's device
// metadata for a ResourceClaimTemplate-generated claim.
type DRADriver interface {
	// GetVhostMetadata returns the vhost-user socket metadata for the given
	// pod-local claim name and DRA request name.
	GetVhostMetadata(claimName, requestName string) (VhostMetadata, error)
}
