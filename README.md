# vhostuser-network-binding-plugin

A [KubeVirt network binding plugin](https://kubevirt.io/user-guide/network/network_binding_plugins/) that configures
vhost-user interfaces on virtual machines for high-performance DPDK networking.

## Why

DPDK networking bypasses the kernel network stack for performance. VMs communicate with OVS-DPDK through vhost-user
sockets rather than regular tap devices. KubeVirt needs to know where those sockets are and how to wire them into the
libvirt domain XML — this plugin does that.

## How It Works

The plugin runs as a sidecar container in the virt-launcher pod. KubeVirt calls it via gRPC during VM definition:

1. Receives the VMI spec and current libvirt domain XML via the `OnDefineDomain` hook
2. Filters VMI interfaces that use the `vhostuser` binding plugin
3. For each interface, resolves the vhost-user socket path from
   Kubernetes [DRA](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/) device
   metadata
4. Mutates the domain XML — injects `<interface type='vhostuser'>` entries with the UNIX domain socket path
   and sets `<memoryBacking>` to shared (required for DPDK)
5. Returns the modified domain XML to KubeVirt

Currently supports the [OVS-DPDK DRA driver](https://github.com/k8snetworkplumbingwg/dra-driver-ovsdpdk).
Supported architectures: arm64, amd64.

## Prerequisites

- Go 1.26+
- Podman or Docker (for container images)
- Kubernetes 1.36+
- KubeVirt v1.9+

## Build and Test

```bash
make build                  # Binary → build/vhostuser-network-binding-plugin
make test                   # Unit tests (Ginkgo/Gomega)
make lint                   # golangci-lint (auto-installed)
make build-image            # Container image via podman
```

Override defaults:

```bash
make build-image CONTAINER_TOOL=docker REGISTRY=my-registry.io/my-org
```

## KubeVirt Registration

Register the binding plugin in the KubeVirt CR, pointing `sidecarImage` to the official image or one built with
`make build-image` above. In KubeVirt v1.9, the `NetworkDevicesWithDRA` feature gate must be enabled:

```yaml
apiVersion: kubevirt.io/v1
kind: KubeVirt
metadata:
  name: kubevirt
  namespace: kubevirt
spec:
  configuration:
    developerConfiguration:
      featureGates:
        - NetworkDevicesWithDRA
    network:
      binding:
        vhostuser:
          sidecarImage: quay.io/kubevirt/vhostuser-network-binding-plugin:<tag>
```

Then reference it in a VirtualMachine spec:

```yaml
apiVersion: kubevirt.io/v1
kind: VirtualMachine
metadata:
  name: my-vm
spec:
  template:
    spec:
      domain:
        devices:
          interfaces:
            - name: dpdknet
              binding:
                name: vhostuser
      networks:
        - name: dpdknet
          resourceClaim:
            claimName: my-dpdk-claim
            requestName: vhost-port
```

## OVS-DPDK DRA Driver metadata API

Currently, the only DRA driver supported by the binding plugin is the
[OVS-DPDK DRA driver](https://github.com/k8snetworkplumbingwg/dra-driver-ovsdpdk).

These are the fields that are expected to be present in the DRA metadata:

| Attribute key | Type | Description |
|---|---|---|
| `vhost-user-path` | String | Container-side path of the vhost-user socket |
| `mtu` | Integer | MTU value |


## Compatibility matrix

The following table shows the combinations of version of each components that are known to work:


| vhost-user-binding-plugin | OVS-DPDK DRA driver | Kubevirt | Kubernetes |
|---|---|---|---|
| 0.1.0 | 0.1.0 | 1.9.0 | 1.36 |
