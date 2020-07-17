# oc-batch-rollout

Rollout an image to many similar deployments across multiple projects.

## Rationale

An OpenShift cluster might have many projects. Some or all of those projects might contain `DeploymentConfig` resources having the same name and using the same image. Updating this image is tricky using built-in OpenShift functionality:

* Explicitly updating the image is only possible for one deployment at a time.
* Image triggers can be used to trigger rollouts when image tags change, but there is no control over the rollout process. This may incur huge load on the cluster due to simultaneous restarts.

`oc-batch-rollout` is trying to solve those issues by offering a simple tool to select deployments across projects to update, and to specify which image should be rolled out and how. Selection of deployment is based on three filters:

1. namespace pattern (required)
2. deployment name (required)
3. currently used image tag/SHA (optional)

The rollout of the new image can be controlled by specifying how many deployments to update simultaneously.

## Use Cases

1. Change the image of `DeploymentConfig` resources across multiple projects, e.g. to `cd/jenkins:v2`. Optionally, this can be applied only to deployments matching a different image tag such as `cd/jenkins:v1`.
2. `DeploymentConfig` resources without an image trigger are not affected when the image tag they are using is updated to point to a new SHA. `oc-batch-rollout` allows to roll out this new image SHA to all deployments in a controlled way. This functionality can also be used to check if all matching deployments use the latest image SHA of an image tag.

## Usage

`oc-batch-rollout` is available as a pre-built binary for various operating systems, see [Installation](#installation).

Once installed, see `obr --help` for usage.

It can make use of an active OC session (retrieved from `~/.kube/config`) or uses login params specified via `--host` and `--token`.

As an example for use case 1, assume you have a Jenkins instance running in many projects, all of which are ending in `-cd`. The currently deployed image is `cd/jenkins:v1`, which you now want to update to`cd/jenkins:v2`, 10 instances at a time. To do this, simply run:

```
obr --projects ".*-cd\$" --deployment jenkins \
    --current-image cd/jenkins:v1 --new-image cd/jenkins:v2 \
    --batchsize 10
```

To ensure that all deployments use the latest image SHA of a (moving) tag (use case 2):
```
obr --projects ".*-cd\$" --deployment jenkins \
    --current-image cd/jenkins:v2 --new-image cd/jenkins:v2 \
    --batchsize 10
```

The tool is interactive and will ask for confirmation before applying the updates.

## Installation

The latest release is 0.1.0.

MacOS:

```
curl -LO "https://github.com/opendevstack/oc-batch-rollout/releases/download/v0.1.0/obr-darwin-amd64" && \
chmod +x obr-darwin-amd64 && mv obr-darwin-amd64 /usr/local/bin/obr
```

Linux:

```
curl -LO "https://github.com/opendevstack/oc-batch-rollout/releases/download/v0.1.0/obr-linux-amd64" && \
chmod +x obr-linux-amd64 && mv obr-linux-amd64 /usr/local/bin/obr
```

Windows (using Git Bash):

```
curl -LO "https://github.com/opendevstack/oc-batch-rollout/releases/download/v0.1.0/obr-windows-amd64.exe" && \
chmod +x obr-windows-amd64.exe && mv obr-windows-amd64.exe /mingw64/bin/obr.exe
```

## Development

TODO
