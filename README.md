# Infrastructure charts

This repository hosts Helm charts for managing a multi-cluster internal platform with Kubernetes as the control plane and data plane.

Confused by this repository?
[Read the explanations below.](#explanations)

## How-tos

### How to get Kubeconfig for tenant cluster

Assuming your cluster is named `cluster`, use the following command to dump the tenant cluster's Kubeconfig to `kubeconfig.yaml`:

```
kgsec cluster-kubeconfig -o json | jq .data.value --raw-output | base64 -d > kubeconfig.yaml
```

Configure kubectl to use `kubeconfig.yaml`:

```
export KUBECONFIG=$(pwd)/kubeconfig.yaml
```

Kubectl should now be connected to the tenant cluster.
Run a command to make sure it works:

```
kubectl get pods -A
```

### How to SSH to a node in tenant cluster

Assuming your cluster is named `cluster`, use the following command to add the SSH key to SSH agent:

```
kgsec cluster-ssh-keys -o json | jq .data.key --raw-output | base64 -d | ssh-add -
```

List virtual machine instances to view IPs of the nodes:

```
kubectl get vmi
```

For purposes of this guide we'll assume the node we want to SSH to has the IP
`10.233.111.136`.
We'll also assume the host cluster has a node with the IP `10.1.0.10`, and the node has a user called `user` with your SSH key authorized.

SSH to the tenant node by using the host node as a jump host, and `capk` as the user for the tenant node:

```
ssh -J user@10.1.0.10 capk@10.233.111.136
```

You should now be connected with SSH into the tenant node.

## Explanations

### What is this?

This repository contains Helm charts for managing Kubernetes clusters with Cluster API, KubeVirt, and Argo CD.
Charts in this repository include configurations for addons such as a centralized telemetry system, Cert Manager, ExternalDNS and more.

This repository is part of a move towards removing Terraform and Ansible from my internal platform.
Instead using the Kubernetes API as a control plane.

### Disclaimer

Helm charts in this repository use internal tools that are not yet publicly available and are very specific to my internal platform.
It is recommended to use this repository as an example to build your own instead of relying on it for your setup.

I am working on open sourcing all parts eventually.

This repository is under active development, and you can expect breaking changes regularly.
Charts are versioned with semver to convey breaking changes.

### Directory structure

This repository contains the following directories:

- `charts` containing the Helm charts themselves. [See "Structure and architecture" for more information.](#structure-and-architecture)
- `tools` containing scripts used to generate documentation and publish charts.
- `docs` containing an Astro documentation website.

### Structure and architecture

This repository contains three types of Helm charts:

- `components` charts meant to be deployed on tenant clusters, noted by the `-components` suffix in the chart name.
- *regular* charts meant to be deployed on the host cluster, with no name suffix.
- `application` charts meant to be deployed on the host cluster in an Argo CD App-of-Apps approach.

You should only use `application` charts unless you want to do things manually.
The other chart types are used as building blocks for the `application` charts.

### Releases

Charts are released by creating Git tags formatted as `<chart-name>-<chart-version>`, for example `cluster-1.0.0`.
Scripts from the `tools` directory are used to release charts and generate documentation.

"Why not Chart Releaser?" you might ask.
I've been using it for a few years and have serveral reasons for deciding against it:

- Chart Releaser only works with GitHub (I'm using self-managed GitLab internally).
- Chart Releaser does not work with GitLab Helm repository and OCI registries. Only `index.yaml` files hosted on static pages.
- When Chart Releaser fails, it often leaves the chart repository in a broken state requiring manual fixes.
- Using Chart Releaser there is no easy way to use Git (without GitHub) to find which commit each chart release came from, making building multi-version documentation much harder.

These considerations led me to building my own approach for managing a charts repository:

- Releasing charts based on Git tags and not file changes.
- Using Frigate to generate documentation for charts.
- Custom Astro website for displaying multi-version chart documentation.

### Host vs. tenant clusters

The documentation in this repository talks a lot about the host cluster and tenant clusters.

The host cluster is a cluster running on bare matal with Cluster API, KubeVirt and Argo CD installed.
The host cluster hosts the tenant clusters in KubeVirt virtual machines and manages tenant clusters with Cluster API and Argo CD.

The tenant clusters are clusters managed inside the host cluster. Tenant clusters are used to run workloads.
