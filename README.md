# oc-batch-rollout

Rollout an image to many deployments across multiple projects.

## Rationale

An OpenShift cluster might have many projects. Some or all of those projects might contain deployments using the same image. Updating this image is tricky using built-in OpenShift functionality:

* Explicitly updating the image is only possible for one deployment at a time.
* Image triggers can be used to rollout an image to many deployments, but users do not have control over the rollout process. This may incur huge load on the cluster due to simultaneous restarts.

oc-batch-rollout is trying to solve those issues by offering a simple tool to select deployments across projects to update, and to specify which image should be rolled out and how. Selection of deployment is based on three filters: 

1. namespace pattern (required)
2. deployment name (required)
3. currently used image tag/SHA (optional)

The rollout of the new image can be controlled by specifying how many deployments to update simultaneously.

## Usage

oc-batch-rollout is available as a pre-built binary (`obr`) for various operating systems. Once installed, see `obr --help` for usage.

As an example, assume you have a Jenkins instance running in many projects, all of which are ending in `-cd`. The currently deployed image is `v1`, which you now want to update to`v2`, 10 instances at a time. To do this, simply run:

```
obr --projects ".*-cd\$" --deployment jenkins --current-image v1 --new-image v2 --batchsize 10
```

The tool is interactive and will ask for confirmation before applying the updates (disable with --non-interactive).

## Installation

TODO

## Development

TODO
